
{{site.data.keyword.attribute-definition-list}}

# Detecting errors and log collection

Use the CSI (Container Storage Interface) driver debug information for problem identification.

These procedures are applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.{: tip}

## Detecting errors

To help pinpoint potential causes for stateful pod failure:

1. Verify that all CSI pods are running.
```
kubectl get pods -n <namespace> -l product=ibm-block-csi-driver
```

2. If a pod is not in a _Running_ state, run the following command and view the logs:
```
kubectl describe -n <namespace> pod/<pod-name>
```

## Status and log collection
To collect and display status and logs related to the different components of IBM® block storage CSI driver, use the Kubernetes commands that are found in this section.

Before you begin collecting logs, create a directory for the logs on the control-plane or bastion server.

For example:

    mkdir logs

Save logs and status reports directly to the created directory by adding in the following string at the end of the collection command:

    > logs/<log_filename>.log

Be sure that the logs cover any relevant timeframes for the specific issues that you are trying to debug when gathering logs from the storage system.{: important}

All commands here are listed with the collection command with the `logs` folder name example. Change the folder name according as needed.{: tip}

### General status and log collection for IBM Support
Be sure to run the following steps and copy the output to an external file, when engaging IBM Support and sending log collections.

1. Check the node status.
```
kubectl get nodes
```

2. Check the CSI driver component status.
```
kubectl get -A pod -o wide | grep ibm-block-csi
```

3. Check if the PersistentVolumeClaims (PVCs) are _Bound_. The output of the following command should be similar to the following: `PVC NAME: demo-pvc-file-system PVC STATUS: Bound`
```
kubectl get -n <namespace> pvc -o=jsonpath='{range .items[?(@.metadata.annotations.volume\.beta\.kubernetes\.io/storage-provisioner=="block.csi.ibm.com")]}{"PVC NAME: "}{@.metadata.name}{" PVC STATUS: "}{@.status.phase}{"\n"}{end}'
```

If the PVCs are not in the _Bound_ state, collect the events of all unbound PVCs. (See [Details collection for unbound PVCs](#details-collection-for-unbound-pvcs).){: note}

#### Log collection for all CSI driver node pods and their containers

To collect logs for all CSI driver node pods, use the following commands (collection of 'previous' pod logs may fail if the pods have never restarted):

    nodepods=`kubectl get pods -n <namespace> -l product=ibm-block-csi-driver -l app.kubernetes.io/component=csi-node --output=jsonpath={.items..metadata.name}`
    
    for pod in $nodepods;do for container in `kubectl get -n <namespace> pod $pod -o jsonpath='{.spec.containers[*].name}'`;do kubectl logs -n <namespace> $pod -c $container > logs/${pod}_${container}.log;done;done
    for pod in $nodepods;do for container in `kubectl get -n <namespace> pod $pod -o jsonpath='{.spec.containers[*].name}'`;do kubectl logs --previous -n <namespace> $pod -c $container > logs/${pod}_${container}.previous.log;done;done

<br>

#### Log collection for all CSI controller containers

To collect logs for all controller containers, use the following commands (collection of 'previous' pod logs may fail if the pod has never restarted):
    
    for container in `kubectl get -n <namespace> pod ibm-block-csi-controller-0 -o jsonpath='{.spec.containers[*].name}'`;do kubectl logs -n <namespace> ibm-block-csi-controller-0 -c $container > logs/ibm-block-csi-controller-0_${container}.log;done
    for container in `kubectl get -n <namespace> pod ibm-block-csi-controller-0 -o jsonpath='{.spec.containers[*].name}'`;do kubectl logs --previous -n <namespace> ibm-block-csi-controller-0 -c $container > logs/ibm-block-csi-controller-0_${container}.previous.log;done

<br>

#### Log collection for CSI operator logs
To collect CSI operator logs, use the following commands (collection of 'previous' pod logs may fail if the pod has never restarted):

    operatorpod=`kubectl get pods -n <namespace> -l app.kubernetes.io/name=ibm-block-csi-operator --output=jsonpath='{.items..metadata.name}'`
    kubectl logs $operatorpod -n <namespace> > logs/operator.log
    kubectl logs --previous $operatorpod -n <namespace> > logs/operator.previous.log

<br>

#### Log collection when using host definer
When a host definer issue occurs, be sure to collect logs from the host definer pod where the error occurred, using the following command (collection of 'previous' pod logs may fail if the pods has never restarted):

    hostdefinerpod=`kubectl get pods -n <namespace> -l app.kubernetes.io/component=hostdefiner --output=jsonpath='{.items..metadata.name}'`
    kubectl logs $hostdefinerpod -n <namespace> > logs/hostdefiner.log
    kubectl logs --previous $hostdefinerpod -n <namespace> > logs/hostdefiner.previous.log

<br>

### Collecting details of all CSI objects and components
    kubectl describe all -l product=ibm-block-csi-driver -n <namespace> > logs/describe_ibm-block-csi-driver.log

<br>

### Status collection for CSI pods, daemonset, and statefulset
    kubectl get all -n <namespace> -l product=ibm-block-csi-driver > logs/get_all_ibm-block-csi-driver.log

<br>

### Details collection for unbound PVCs
    kubectl describe -n <pvc_namespace> pvc <pvc-name> > logs/pvc_not_bounded.log

<br>

### Details collection for pods not in the _Running_ state
    kubectl describe -n <pod_namespace> pod <not-running-pod-name> > logs/pod_not_running.log
<br>
