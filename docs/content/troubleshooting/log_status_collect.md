# Log and status collection

Use the CSI (Container Storage Interface) driver debug information for problem identification.

**Note:** These procedures are applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.

To collect and display status and logs related to the different components of IBM® block storage CSI driver, use the following Kubernetes commands:

## Status collection for CSI pods, daemonset, and statefulset

`kubectl get all -n <namespace>  -l csi`

## Log collection for IBM block storage CSI driver controller

`kubectl log -f -n <namespace> ibm-block-csi-controller-0 -c ibm-block-csi-controller`

## Log collection for IBM block storage CSI driver node (per worker node or PODID)

`kubectl log -f -n <namespace> ibm-block-csi-node-<PODID> -c ibm-block-csi-node`

## Log collection for Operator for IBM block storage CSI driver

`kubectl log -f -n <namespace> ibm-block-csi-operator-<PODID> -c ibm-block-csi-operator`

## Detecting errors

To help pinpoint potential causes for stateful pod failure:

1.  Verify that all CSI pods are running.

    ```
    kubectl get pods -n <namespace> -l csi
    ```

2.  If a pod is not in a _Running_ state, run the following command:

    ```
    kubectl describe -n <namespace> pod/<pod-name>
    ```

    View the logs.