/**
 * Copyright 2026 IBM Corp.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package driver

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

const (
	drVrgReplicationStateSecondary = "secondary"
	drVrgActionFailover            = "Failover"
	drPvcVRProtectionFinalizer     = "volumereplicationgroups.ramendr.openshift.io/pvc-vr-protection"
	drCheckInterval                = 15 * time.Second
	drVrgApiRecheckInterval        = 5 * time.Minute
	drStuckPodSlack                = 30 * time.Second
	drApiCallTimeout               = 10 * time.Second
	drLockMsg                      = "DRFailoverWatcher"
)

var drVrgGVR = schema.GroupVersionResource{
	Group:    "ramendr.openshift.io",
	Version:  "v1alpha1",
	Resource: "volumereplicationgroups",
}

// drFailoverWatcher handles pods that are stuck terminating on this node during a Ramen Failover:
// the container can not exit because I/O to a volume made unavailable by the Failover is queued forever,
// so kubelet never calls NodeUnpublishVolume/NodeUnstageVolume for it.
// Pods whose containers already exited are left to NodeUnstageVolume, which handles that case itself.
type drFailoverWatcher struct {
	node       *NodeService
	kubeClient kubernetes.Interface
	dynClient  dynamic.Interface
	vrgLister  cache.GenericLister
	// VRG keys (namespace/name) to reconcile, from VRG events or a scheduled re-check
	queue workqueue.TypedDelayingInterface[string]
	// per VRG key: volumes whose queueing was already disabled, kept only while they still pass all gates.
	// Used only to log a stuck pod once; the disable itself is re-applied on every check (see handleVolume).
	// Only the single worker goroutine accesses it.
	handled map[string]map[string]bool
}

// StartDRFailoverWatcher starts the watcher in the background. It stays idle while the
// VolumeReplicationGroup API (Ramen) is not served in the cluster.
func (d *NodeService) StartDRFailoverWatcher(ctx context.Context) {
	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		logger.Warningf("DR failover watcher is disabled, unable to load in-cluster configuration: %v", err)
		return
	}
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		logger.Warningf("DR failover watcher is disabled, unable to create Kubernetes client: %v", err)
		return
	}
	dynClient, err := dynamic.NewForConfig(kubeConfig)
	if err != nil {
		logger.Warningf("DR failover watcher is disabled, unable to create dynamic client: %v", err)
		return
	}

	watcher := &drFailoverWatcher{
		node:       d,
		kubeClient: kubeClient,
		dynClient:  dynClient,
		queue:      workqueue.NewTypedDelayingQueue[string](),
		handled:    map[string]map[string]bool{},
	}
	go watcher.run(ctx)
}

func (w *drFailoverWatcher) run(ctx context.Context) {
	defer w.queue.ShutDown()
	if !w.waitForVrgApi(ctx) {
		return
	}

	factory := dynamicinformer.NewDynamicSharedInformerFactory(w.dynClient, 0)
	vrgInformer := factory.ForResource(drVrgGVR)
	w.vrgLister = vrgInformer.Lister()
	enqueue := func(obj interface{}) {
		if key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj); err == nil {
			w.queue.Add(key)
		}
	}
	// Only entering or leaving a Failover cleanup window is an event; inside a window the VRG is
	// re-checked on its own schedule, so the frequent Ramen status updates do not add pod LISTs.
	if _, err := vrgInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if w.isInFailoverWindow(obj) {
				enqueue(obj)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if w.isInFailoverWindow(oldObj) != w.isInFailoverWindow(newObj) {
				enqueue(newObj)
			}
		},
		DeleteFunc: enqueue,
	}); err != nil {
		logger.Errorf("DR failover watcher failed to register VolumeReplicationGroup event handler: %v", err)
		return
	}
	factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), vrgInformer.Informer().HasSynced) {
		logger.Errorf("DR failover watcher failed to sync the VolumeReplicationGroup cache")
		return
	}
	logger.Infof("DR failover watcher started on node {%v}", w.node.Hostname)

	go func() {
		<-ctx.Done()
		w.queue.ShutDown()
	}()
	for w.processNextVrg(ctx) {
	}
}

func (w *drFailoverWatcher) isInFailoverWindow(obj interface{}) bool {
	vrg, ok := obj.(*unstructured.Unstructured)
	return ok && len(getFailoverSecondaryProtectedPvcs(vrg, w.node.ConfigYaml.Identity.Name)) > 0
}

func (w *drFailoverWatcher) processNextVrg(ctx context.Context) bool {
	key, shutdown := w.queue.Get()
	if shutdown {
		return false
	}
	defer w.queue.Done(key)

	if recheckAfter := w.reconcileVrg(ctx, key); recheckAfter > 0 {
		w.queue.AddAfter(key, recheckAfter)
	}
	return true
}

// waitForVrgApi blocks until the VolumeReplicationGroup API is served, so clusters without Ramen pay no cost.
func (w *drFailoverWatcher) waitForVrgApi(ctx context.Context) bool {
	logged := false
	for {
		if w.isVrgApiServed() {
			return true
		}
		if !logged {
			logger.Infof("VolumeReplicationGroup API is not served, DR failover watcher is idle")
			logged = true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(drVrgApiRecheckInterval):
		}
	}
}

func (w *drFailoverWatcher) isVrgApiServed() bool {
	resources, err := w.kubeClient.Discovery().ServerResourcesForGroupVersion(drVrgGVR.GroupVersion().String())
	if err != nil {
		return false
	}
	for _, resource := range resources.APIResources {
		if resource.Name == drVrgGVR.Resource {
			return true
		}
	}
	return false
}

// reconcileVrg checks the pods of the PVCs protected by one VRG and returns when to check it again,
// or 0 when the VRG is out of a Failover cleanup window (a later VRG event brings it back).
func (w *drFailoverWatcher) reconcileVrg(ctx context.Context, key string) time.Duration {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return 0
	}
	object, err := w.vrgLister.ByNamespace(namespace).Get(name)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			logger.Warningf("DR failover watcher failed to get VolumeReplicationGroup {%v} from cache: %v", key, err)
			return drCheckInterval
		}
		delete(w.handled, key)
		return 0
	}
	vrg, ok := object.(*unstructured.Unstructured)
	if !ok {
		return 0
	}

	protectedPvcs := getFailoverSecondaryProtectedPvcs(vrg, w.node.ConfigYaml.Identity.Name)
	if len(protectedPvcs) == 0 {
		delete(w.handled, key)
		return 0
	}
	pvcSelector, err := getVrgPvcSelector(vrg)
	if err != nil {
		logger.Warningf("Failed to get pvcSelector of VolumeReplicationGroup {%v}: %v", key, err)
		return drCheckInterval
	}

	handled := w.handled[key]
	if handled == nil {
		handled = map[string]bool{}
		w.handled[key] = handled
	}
	seen := map[string]bool{}
	recheckAfter := drCheckInterval
	for pvcNamespace, pvcNames := range protectedPvcs {
		recheckAfter = min(recheckAfter, w.checkNamespace(ctx, pvcNamespace, pvcNames, pvcSelector, handled, seen))
	}
	for volumeId := range handled {
		if !seen[volumeId] {
			delete(handled, volumeId)
		}
	}
	return recheckAfter
}

// getFailoverSecondaryProtectedPvcs returns the PVCs (namespace -> names) of this driver that the VRG still
// protects while a Failover moves it to secondary. It is empty once Ramen completes the demote.
func getFailoverSecondaryProtectedPvcs(vrg *unstructured.Unstructured, driverName string) map[string]map[string]bool {
	replicationState, _, _ := unstructured.NestedString(vrg.Object, "spec", "replicationState")
	action, _, _ := unstructured.NestedString(vrg.Object, "spec", "action")
	if replicationState != drVrgReplicationStateSecondary || action != drVrgActionFailover {
		return nil
	}

	protectedPvcs, _, _ := unstructured.NestedSlice(vrg.Object, "status", "protectedPVCs")
	result := map[string]map[string]bool{}
	for _, item := range protectedPvcs {
		protectedPvc, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		provisioner, _, _ := unstructured.NestedString(protectedPvc, "csiProvisioner")
		namespace, _, _ := unstructured.NestedString(protectedPvc, "namespace")
		name, _, _ := unstructured.NestedString(protectedPvc, "name")
		if provisioner != driverName || namespace == "" || name == "" {
			continue
		}
		if result[namespace] == nil {
			result[namespace] = map[string]bool{}
		}
		result[namespace][name] = true
	}
	return result
}

func getVrgPvcSelector(vrg *unstructured.Unstructured) (labels.Selector, error) {
	rawSelector, found, err := unstructured.NestedMap(vrg.Object, "spec", "pvcSelector")
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("spec.pvcSelector is not set")
	}
	labelSelector := &metav1.LabelSelector{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(rawSelector, labelSelector); err != nil {
		return nil, err
	}
	return metav1.LabelSelectorAsSelector(labelSelector)
}

// checkNamespace handles the stuck pods on this node that use the protected PVCs, and returns when to check again:
// at the deletion deadline (+ drStuckPodSlack) of the earliest pod not yet due, otherwise after drCheckInterval
// (a pod deletion produces no VRG event, so the window is re-checked until Ramen completes it).
func (w *drFailoverWatcher) checkNamespace(ctx context.Context, namespace string, pvcNames map[string]bool,
	pvcSelector labels.Selector, handled map[string]bool, seen map[string]bool) time.Duration {
	recheckAfter := drCheckInterval
	listCtx, cancel := context.WithTimeout(ctx, drApiCallTimeout)
	defer cancel()
	pods, err := w.kubeClient.CoreV1().Pods(namespace).List(listCtx, metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("spec.nodeName", w.node.Hostname).String(),
	})
	if err != nil {
		logger.Warningf("DR failover watcher failed to list pods in namespace {%v}: %v", namespace, err)
		return recheckAfter
	}

	now := time.Now()
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.DeletionTimestamp == nil || !hasNonTerminatedContainer(pod) {
			continue
		}
		for _, volume := range pod.Spec.Volumes {
			if volume.PersistentVolumeClaim == nil || !pvcNames[volume.PersistentVolumeClaim.ClaimName] {
				continue
			}
			// the pod outlived its deletion deadline (request time + grace period) by drStuckPodSlack
			if wait := pod.DeletionTimestamp.Add(drStuckPodSlack).Sub(now); wait > 0 {
				recheckAfter = min(recheckAfter, wait)
				continue
			}
			w.handleVolume(ctx, pod, volume.PersistentVolumeClaim.ClaimName, pvcSelector, handled, seen)
		}
	}
	return recheckAfter
}

// hasNonTerminatedContainer reports whether any container of the pod (init, including native sidecars,
// regular or ephemeral) is not terminated, so the pod can still be blocked on I/O to its volumes.
func hasNonTerminatedContainer(pod *corev1.Pod) bool {
	for _, statuses := range [][]corev1.ContainerStatus{pod.Status.InitContainerStatuses,
		pod.Status.ContainerStatuses, pod.Status.EphemeralContainerStatuses} {
		for _, containerStatus := range statuses {
			if containerStatus.State.Terminated == nil {
				return true
			}
		}
	}
	return false
}

func (w *drFailoverWatcher) handleVolume(ctx context.Context, pod *corev1.Pod, pvcName string,
	pvcSelector labels.Selector, handled map[string]bool, seen map[string]bool) {
	getCtx, cancel := context.WithTimeout(ctx, drApiCallTimeout)
	defer cancel()

	pvc, err := w.kubeClient.CoreV1().PersistentVolumeClaims(pod.Namespace).Get(getCtx, pvcName, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			logger.Warningf("DR failover watcher failed to get PVC {%v/%v}: %v", pod.Namespace, pvcName, err)
		}
		return
	}
	if !slices.Contains(pvc.Finalizers, drPvcVRProtectionFinalizer) ||
		!pvcSelector.Matches(labels.Set(pvc.Labels)) || pvc.Spec.VolumeName == "" {
		return
	}

	pv, err := w.kubeClient.CoreV1().PersistentVolumes().Get(getCtx, pvc.Spec.VolumeName, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			logger.Warningf("DR failover watcher failed to get PV {%v}: %v", pvc.Spec.VolumeName, err)
		}
		return
	}
	if pv.Spec.CSI == nil || pv.Spec.CSI.Driver != w.node.ConfigYaml.Identity.Name {
		return
	}

	volumeId := pv.Spec.CSI.VolumeHandle
	seen[volumeId] = true

	if err := w.node.VolumeIdLocksMap.AddVolumeLock(volumeId, drLockMsg); err != nil {
		logger.Debugf("DR failover watcher skips volume {%v} for now: %v", volumeId, err)
		return
	}
	defer w.node.VolumeIdLocksMap.RemoveVolumeLock(volumeId, drLockMsg)

	if !handled[volumeId] {
		logger.Infof("Pod {%v/%v} is stuck terminating with volume {%v} of PVC {%v} that Ramen Failover moves to secondary",
			pod.Namespace, pod.Name, volumeId, pvcName)
	}
	// Called again on every check while the pod is still stuck, even after a successful disable: a reload of the
	// map (e.g. "multipath -r") brings back no_path_retry from the config (queue for IBM 2145) and multipathd's
	// setup_map clears its disable_queueing flag. The call re-verifies all its gates and is idempotent.
	// SCSI (FC/iSCSI): disables queueing only if all paths failed and the LUN is unavailable on all of them.
	// It returns early for NVMe devices (native and DM-multipath): the watcher takes no action for NVMe.
	if mpathDevice := w.node.disableQueueingIfLunUnavailable(w.node.NodeUtils.GetVolumeUuid(volumeId)); mpathDevice != "" {
		handled[volumeId] = true
	}
}
