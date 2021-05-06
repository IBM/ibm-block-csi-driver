# Recovering a pod volume attachment from a crashed Kubernetes node

This section details a manual operation required to revive Kubernetes pods that reside on a crashed node due to an existing Kubernetes limitation.

## Identifying a crashed node
**Note:** These procedures are applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.

When a worker node shuts down or crashes, all pods in a StatefulSet that reside on it become unavailable. In these scenarios, the node status is _NotReady_, and the pod status appears as _Terminating_.

For example:

```screen
$> kubectl get nodes
NAME STATUS ROLES AGE VERSION
k8s-master Ready master 6d <your k8s version>
k8s-node1 Ready <none> 6d <your k8s version>
k8s-node3 NotReady <none> 6d <your k8s version>

$> kubectl get pods --all-namespaces -o wide | grep default
default sanity-statefulset-0 1/1 Terminating 0 19m 10.244.2.37 k8s-node3
```

## Recovering a crashed node

**Attention:** In order to avoid data loss, before continuing, verify that there are no pods connected to this volume.

Follow the following procedure to recover from a crashed node \(see a [full example](#full_example) at the end of the procedure\):

1.  Find for the `volumeattachment` of the created pod:

    ```
    kubectl get volumeattachment
    ```

2.  Copy the `volumeattachment` name.
3.  Delete the `volumeattachment`:

    ```
    kubectl delete volumeattachment <volumeattachment name>
    ```

4.  Delete the pod:

    ```
    kubectl delete pod <pod name> --grace-period=0 --force
    ```

5.  Verify that the pod is now in a Running state and that the pod has moved to worker-node1.

<a name="full_example">For example:</a>

```screen
$> kubectl get nodes
NAME STATUS ROLES AGE VERSION
k8s-master Ready master 6d <your k8s version>
k8s-node1 Ready <none> 6d <your k8s version>
k8s-node3 NotReady <none> 6d <your k8s version>

$> kubectl get pods --all-namespaces -o wide | grep default
default sanity-statefulset-0 1/1 Terminating 0 19m 10.244.2.37 k8s-node3

$> kubectl get volumeattachment
NAME AGE
csi-5944e1c742d25e7858a8e48311cdc6cc85218f1156dd6598d4cf824fb1412143 10m

$> kubectl delete volumeattachment csi-5944e1c742d25e7858a8e48311cdc6cc85218f1156dd6598d4cf824fb1412143
volumeattachment.storage.k8s.io "csi-5944e1c742d25e7858a8e48311cdc6cc85218f1156dd6598d4cf824fb1412143" deleted

$> kubectl delete pod sanity-statefulset-0 --grace-period=0 --force
warning: Immediate deletion does not wait for confirmation that the running resource has been terminated. The resource may continue to run on the cluster indefinitely.
pod "sanity-statefulset-0" deleted

$> kubectl get pods --all-namespaces -o wide | grep default
default sanity-statefulset-0 1/1 Running 0 26s 10.244.1.210 k8s-node1
```

