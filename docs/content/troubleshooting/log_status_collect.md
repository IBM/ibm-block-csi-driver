# Detecting errors and log collection

Use the CSI (Container Storage Interface) driver debug information for problem identification.

**Note:** These procedures are applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.

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

## Status and log collection
To collect and display status and logs related to the different components of IBM® block storage CSI driver, use the Kubernetes found in this section.

Before you begin collecting logs, at the master host level, create a directory for the logs.

For example:

```
mkdir logs
```
Save logs and status reports directly to the created directory by adding in the `> logs/<log_filename>` syntax to the end of collection command.

**Important:** When gathering logs from the storage system, be sure that the logs cover any relevant time frames for the specific issues that you are trying to debug.

### General status and log collection for IBM Support
When engaging with IBM Support, be sure to run the following steps and copy the output to an external file, when sending log collections:

1. Check the node status.
    
    `kubectl get nodes`
2. Check the CSI driver component status.

    `kubectl get -n <namespace> pod -o wide | grep ibm-block-csi`
3. Check if the PersistentVolumeClaims (PVCs) are _Bound_.

    `kubectl get pvc`

    - If the PVCs are not bound, (in the _XXX_ state) collect the events of all unbound PVCs. (See [Log collection for unbound PVCs](#log-collection-for-unbound-pvcs).)

### Log collection for all pods and containers
To collect logs for all pods and containers, use the following commands:

    nodepods=`kubectl get -n <namespace> pod -l app.kubernetes.io/component=csi-node --output=jsonpath={.items..metadata.name}`
    
    for pod in $nodepods; do  kubectl logs -n <namespace> --all-containers $pod > logs/$pod; done

### Log collection for all operator logs
To collect all Operator logs, use the following commands:

    operatorpod=`kubectl get pods --all-namespaces |grep ibm-block-csi-operator|awk '{print $2}'`
    kubectl logs $operatorpod -n <namespace> > logs/operator

### Log collection for all CSI component details

`kubectl describe all -l csi -n <namespace>`

For example:

    kubectl describe all -l csi -n <namespace> > logs/describe_csi

### Status collection for CSI pods, daemonset, and statefulset
`kubectl get all -n <namespace>  -l csi`


 ### Log collection for the CSI driver controller
`kubectl log -f -n <namespace> ibm-block-csi-controller-0 -c ibm-block-csi-controller`

For example:

    kubectl log -f -n <namespace> ibm-block-csi-controller-0 -c ibm-block-csi-controller > logs/ibm-block-csi-controller

### Log collection for the CSI driver node (per worker node or PODID)
`kubectl log -f -n <namespace> ibm-block-csi-node-<PODID> -c ibm-block-csi-node`

For example:
    
    kubectl log -f -n <namespace> ibm-block-csi-node-<PODID> -c ibm-block-csi-node > logs/csi-node-<PODID>

### Log collection for unbound PVCs
`kubectl describe pvc <pvc-name>`

For example:

    kubectl describe pvc <pvc-name> > logs/pvc_not_bounded

### Log collection for pods not in the _Running_ state
`kubectl describe pod <pod-name>`

For example:

    kubectl describe pod <pod-name> > logs/pod_not_running

### Log collection for Operator for IBM block storage CSI driver
`kubectl log -f -n <namespace> ibm-block-csi-operator-<PODID> -c ibm-block-csi-operator`



