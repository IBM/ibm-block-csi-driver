# Log collection

Use the CSI \(Container Storage Interface\) driver logs for problem identification.

**Note:** These procedures are applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.

To collect and display logs, related to the different components of IBM® block storage CSI driver, use the following Kubernetes commands:

## Log collection for CSI pods, daemonset, and StatefulSet

`kubectl get all -n <namespace>  -l csi`

## Log collection for IBM block storage CSI driver controller

`kubectl log -f -n <namespace> ibm-block-csi-controller-0 -c ibm-block-csi-controller`

## Log collection for IBM block storage CSI driver node \(per worker node or PODID\)

`kubectl log -f -n <namespace> ibm-block-csi-node-<PODID> -c ibm-block-csi-node`

## Log collection for Operator for IBM block storage CSI driver

`kubectl log -f -n <namespace> ibm-block-csi-operator-<PODID> -c ibm-block-csi-operator`