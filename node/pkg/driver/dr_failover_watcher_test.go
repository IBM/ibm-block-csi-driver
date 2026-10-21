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
	"testing"
	"time"

	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes/fake"
)

func TestHasNonTerminatedContainer(t *testing.T) {
	running := corev1.ContainerStatus{State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}
	terminated := corev1.ContainerStatus{State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{}}}

	testCases := []struct {
		name     string
		status   corev1.PodStatus
		expected bool
	}{
		{name: "all containers terminated", expected: false, status: corev1.PodStatus{
			InitContainerStatuses: []corev1.ContainerStatus{terminated},
			ContainerStatuses:     []corev1.ContainerStatus{terminated}}},
		{name: "regular container running", expected: true, status: corev1.PodStatus{
			InitContainerStatuses: []corev1.ContainerStatus{terminated},
			ContainerStatuses:     []corev1.ContainerStatus{running}}},
		{name: "native sidecar (init container) running", expected: true, status: corev1.PodStatus{
			InitContainerStatuses: []corev1.ContainerStatus{running},
			ContainerStatuses:     []corev1.ContainerStatus{terminated}}},
		{name: "ephemeral container running", expected: true, status: corev1.PodStatus{
			ContainerStatuses:          []corev1.ContainerStatus{terminated},
			EphemeralContainerStatuses: []corev1.ContainerStatus{running}}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasNonTerminatedContainer(&corev1.Pod{Status: tc.status}); got != tc.expected {
				t.Fatalf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

// fakeDrHelper overrides only DisableQueueingIfLunUnavailable; calling any other method of the embedded nil
// interface panics, which proves the watcher calls nothing else on it.
type fakeDrHelper struct {
	device_connectivity.OsDeviceConnectivityHelperScsiGenericInterface
	result string
	calls  []string
}

func (f *fakeDrHelper) DisableQueueingIfLunUnavailable(volumeUuid string) (string, error) {
	f.calls = append(f.calls, volumeUuid)
	return f.result, nil
}

type fakeDrNodeUtils struct {
	NodeUtilsInterface
}

func (fakeDrNodeUtils) GetVolumeUuid(volumeId string) string {
	return "uuid-of-" + volumeId
}

func TestDrFailoverWatcherReappliesDisableWhilePodStuck(t *testing.T) {
	const (
		driverName = "block.csi.ibm.com"
		namespace  = "app-ns"
		pvcName    = "pvc-writer"
		pvName     = "pv-writer"
		volumeId   = "SVC:15;600507680C8E06BE78000000000008DC"
	)
	pvcSelector := labels.SelectorFromSet(labels.Set{"appname": "app"})

	testCases := []struct {
		name          string
		helperResult  string
		expectHandled bool
	}{
		{name: "disable succeeded: re-applied on the next check", helperResult: "/dev/dm-3", expectHandled: true},
		{name: "gates not met: tried again on the next check", helperResult: "", expectHandled: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			kubeClient := fake.NewClientset(
				&corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{Name: pvcName, Namespace: namespace,
						Labels: map[string]string{"appname": "app"}, Finalizers: []string{drPvcVRProtectionFinalizer}},
					Spec: corev1.PersistentVolumeClaimSpec{VolumeName: pvName},
				},
				&corev1.PersistentVolume{
					ObjectMeta: metav1.ObjectMeta{Name: pvName},
					Spec: corev1.PersistentVolumeSpec{PersistentVolumeSource: corev1.PersistentVolumeSource{
						CSI: &corev1.CSIPersistentVolumeSource{Driver: driverName, VolumeHandle: volumeId}}},
				},
			)
			helper := &fakeDrHelper{result: tc.helperResult}
			node := &NodeService{
				ConfigYaml:                 ConfigFile{Identity: Identity{Name: driverName}},
				Hostname:                   "test-node",
				NodeUtils:                  fakeDrNodeUtils{},
				VolumeIdLocksMap:           NewSyncLock(10, true),
				OsDeviceConnectivityHelper: helper,
			}
			w := &drFailoverWatcher{node: node, kubeClient: kubeClient, handled: map[string]map[string]bool{}}
			deletionTimestamp := metav1.NewTime(time.Now().Add(-time.Minute))
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "writer", Namespace: namespace,
				DeletionTimestamp: &deletionTimestamp}}
			handled := map[string]bool{}

			for check := 1; check <= 2; check++ {
				seen := map[string]bool{}
				w.handleVolume(context.Background(), pod, pvcName, pvcSelector, handled, seen)
				if !seen[volumeId] {
					t.Fatalf("check %d: volume not marked as seen", check)
				}
				if handled[volumeId] != tc.expectHandled {
					t.Fatalf("check %d: handled = %v, want %v", check, handled[volumeId], tc.expectHandled)
				}
			}
			if len(helper.calls) != 2 {
				t.Fatalf("DisableQueueingIfLunUnavailable called %d times, want 2 (once per check)", len(helper.calls))
			}
			if helper.calls[0] != "uuid-of-"+volumeId {
				t.Fatalf("called with %q, want the volume uuid", helper.calls[0])
			}
		})
	}
}
