# IBM block storage CSI driver 
The Container Storage Interface (CSI) Driver for IBM block storage systems enables container orchestrators such as Kubernetes to manage the life cycle of persistent storage.

## Supported orchestration platforms

The following table details orchestration platforms suitable for deployment of the IBM® block storage CSI driver.

|Orchestration platform|Version|Architecture|
|----------------------|-------|------------|
|Kubernetes|1.20|x86|
|Kubernetes|1.21|x86|
|Red Hat® OpenShift®|4.7|x86, IBM Z®, IBM Power Systems™<sup>1</sup>|
|Red Hat OpenShift|4.8|x86, IBM Z, IBM Power Systems<sup>1</sup>|

<sup>1</sup>IBM Power Systems architecture is only supported on Spectrum Virtualize Family storage systems.

**Note:** As of this document's publication date, IBM Cloud® Satellite only supports RHEL 7 on x86 architecture for Red Hat OpenShift. For the latest support information, see [cloud.ibm.com/docs/satellite](https://cloud.ibm.com/docs/satellite).

## Supported storage systems

IBM® block storage CSI driver 1.6.0 supports different IBM storage systems as listed in the following table.

|Storage system|Microcode version|
|--------------|-----------------|
|IBM FlashSystem™ A9000|12.x|
|IBM FlashSystem A9000R|12.x|
|IBM Spectrum Virtualize™ Family including IBM SAN Volume Controller (SVC) and IBM FlashSystem® family members built with IBM Spectrum® Virtualize (including FlashSystem 5xxx, 7200, 9100, 9200, 9200R)|7.8 and above, 8.x|
|IBM Spectrum Virtualize as software only|7.8 and above, 8.x|
|IBM DS8000® Family|8.x and higher with same API interface|

**Note:**

-   Newer microcode versions may also be compatible. When a newer microcode version becomes available, contact IBM Support to inquire whether the new microcode version is compatible with the current version of the CSI driver.
-   The IBM Spectrum Virtualize Family and IBM SAN Volume Controller storage systems run the IBM Spectrum Virtualize software. In addition, IBM Spectrum Virtualize package is available as a deployable solution that can be run on any compatible hardware.

## Supported operating systems

The following table lists operating systems required for deployment of the IBM® block storage CSI driver.

|Operating system|Architecture|
|----------------|------------|
|Red Hat® Enterprise Linux® (RHEL) 7.x|x86, IBM Z®|
|Red Hat Enterprise Linux CoreOS (RHCOS)|x86, IBM Z®<sup>2</sup>, IBM Power Systems™<sup>1</sup>|

<sup>1</sup>IBM Power Systems architecture is only supported on Spectrum Virtualize Family storage systems.      <br />
<sup>2</sup>IBM Z and IBM Power Systems architectures are only supported using CLI installation.

For full product information, see [IBM block storage CSI driver documentation](https://www.ibm.com/docs/en/stg-block-csi-driver).

<br/>
<br/>
<br/>

## Prerequisites
Perform these steps for each worker node in Kubernetes cluster to prepare your environment for installing the CSI (Container Storage Interface) driver.

1. **For RHEL OS users:** Ensure iSCSI connectivity. If using RHCOS or if the packages are already installed, skip this step and continue to step 2.

2. Configure Linux® multipath devices on the host.

   **Important:** Be sure to configure each worker with storage connectivity according to your storage system instructions. For more information, find your storage system documentation in [IBM Documentation](http://www.ibm.com/docs/).

   **Additional configuration steps for OpenShift® Container Platform users (RHEL and RHCOS).** Other users can continue to step 3.

   Download and save the following yaml file:

   ```
   curl https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/master/deploy/99-ibm-attach.yaml > 99-ibm-attach.yaml
   ```

   This file can be used for both Fibre Channel and iSCSI configurations. To support iSCSI, uncomment the last two lines in the file.

   **Important:** The 99-ibm-attach.yaml configuration file overrides any files that already exist on your system. Only use this file if the files mentioned are not already created. <br />If one or more have been created, edit this yaml file, as necessary.

   Apply the yaml file.

   `oc apply -f 99-ibm-attach.yaml`
    
3. If needed, enable support for volume snapshots (FlashCopy® function) on your Kubernetes cluster.

   For more information and instructions, see the Kubernetes blog post, [Kubernetes 1.20: Kubernetes Volume Snapshot Moves to GA](https://kubernetes.io/blog/2020/12/10/kubernetes-1.20-volume-snapshot-moves-to-ga/).

   Install both the Snapshot CRDs and the Common Snapshot Controller once per cluster.

   The instructions and relevant yaml files to enable volume snapshots can be found at: [https://github.com/kubernetes-csi/external-snapshotter#usage](https://github.com/kubernetes-csi/external-snapshotter#usage)

4. Configure storage system connectivity.

    1.  Define the host of each Kubernetes node on the relevant storage systems with the valid WWPN (for Fibre Channel) or IQN (for iSCSI) of the node.

    2.  For Fibre Channel, configure the relevant zoning from the storage to the host.

<br/>
<br/>
<br/>

## Installing the driver

The operator for IBM® block storage CSI driver can be installed directly with GitHub. Installing the CSI (Container Storage Interface) driver is part of the operator installation process.

Use the following steps to install the operator and driver, with [GitHub](https://github.com/IBM/ibm-block-csi-operator).

**Note:** Before you begin, you may need to create a user-defined namespace. Create the project namespace, using the `kubectl create ns <namespace>` command.

1.  Install the operator.

    1. Download the manifest from GitHub.

        ```
        curl https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/v1.6.0/deploy/installer/generated/ibm-block-csi-operator.yaml > ibm-block-csi-operator.yaml
        ```

    2.  **Optional:** Update the image fields in the ibm-block-csi-operator.yaml.

    3. Install the operator, using a user-defined namespace.

        ```
        kubectl -n <namespace> apply -f ibm-block-csi-operator.yaml
        ```

    4. Verify that the operator is running. (Make sure that the Status is _Running_.)

        ```screen
        $ kubectl get pod -l app.kubernetes.io/name=ibm-block-csi-operator -n <namespace>
        NAME                                    READY   STATUS    RESTARTS   AGE
        ibm-block-csi-operator-5bb7996b86-xntss 1/1     Running   0          10m
        ```

2.  Install the IBM block storage CSI driver by creating an IBMBlockCSI custom resource.

    1.  Download the manifest from GitHub.

        ```
        curl https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/v1.6.0/deploy/crds/csi.ibm.com_v1_ibmblockcsi_cr.yaml > csi.ibm.com_v1_ibmblockcsi_cr.yaml
        ```

    2.  **Optional:** Update the image repository field, tag field, or both in the csi.ibm.com_v1_ibmblockcsi_cr.yaml.

    3.  Install the csi.ibm.com_v1_ibmblockcsi_cr.yaml.

        ```
        kubectl -n <namespace> apply -f csi.ibm.com_v1_ibmblockcsi_cr.yaml
        ```
    
    4.  Verify that the driver is running:
        ```bash
        $ kubectl get pods -n <namespace> -l csi
        NAME                                    READY   STATUS  RESTARTS AGE
        ibm-block-csi-controller-0              6/6     Running 0        9m36s
        ibm-block-csi-node-jvmvh                3/3     Running 0        9m36s
        ibm-block-csi-node-tsppw                3/3     Running 0        9m36s
        ibm-block-csi-operator-5bb7996b86-xntss 1/1     Running 0        10m
        ```

<br/>
<br/>
<br/>

## Configuring k8s secret and storage class 
In order to use the driver, create the relevant storage classes and secrets, as needed.

This section describes how to:
 1. Create a storage system secret - to define the storage system credentials (user and password) and its address.
 2. Configure the storage class - to define the storage system pool name, secret reference, `SpaceEfficiency`, and `fstype`.

### Creating a Secret

Create an array secret YAML file in order to define the storage credentials (username and password) and address.

**Important:** When your storage system password is changed, be sure to also change the passwords in the corresponding secrets, particularly when LDAP is used on the storage systems. <br /><br />Failing to do so causes mismatched passwords across the storage systems and the secrets, causing the user to be locked out of the storage systems.

Use one of the following procedures to create and apply the secret:

#### Creating an array secret file
1. Create the secret file, similar to the following demo-secret.yaml:

    The `management_address` field can contain more than one address, with each value separated by a comma.

    
      ```
      kind: Secret
      apiVersion: v1
      metadata:
        name:  demo-secret
        namespace: default
      type: Opaque
      stringData:
        management_address: demo-management-address  # Array management addresses
        username: demo-username                      # Array username
      data:
        password: ZGVtby1wYXNzd29yZA==               # base64 array password
      ```
    
2. Apply the secret using the following command:

    `kubectl apply -f demo-secret.yaml`
    

     The `secret/<NAME> created` message is emitted.


#### Creating an array secret via command line
**Note:** This procedure is applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.

Create the secret using the following command:

 ```
 kubectl create secret generic <NAME> --from-literal=username=<USER> --from-literal=password=<PASSWORD>--from-literal=management_address=<ARRAY_MGMT> -n <namespace>
 ```
 

### Creating a StorageClass

Create a storage class yaml file in order to define the storage system pool name, secret reference, `SpaceEfficiency`, and `fstype`.

Use the following procedure to create and apply the storage classes.

**Note:** This procedure is applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.

Create a storage class yaml file, similar to the following demo-storageclass.yaml.

Update the capabilities, pools, and array secrets, as needed.

Use the `SpaceEfficiency` parameters for each storage system, as defined in [the following table](#spaceefficiency). These values are not case-sensitive.

_<a name=spaceefficiency>**Table:**</a> `SpaceEfficiency` parameter definitions per storage system type_

|Storage system type|SpaceEfficiency parameter options|
|-------------------|---------------------------------|
|IBM FlashSystem® A9000 and A9000R|Always includes deduplication and compression. No need to specify during configuration.|
|IBM Spectrum® Virtualize Family|- thick (default value)<br />- thin<br />- compressed<br />- deduplicated <br /><br /> **Note:** If not specified, the default value is thick.|
|IBM® DS8000® Family| - none (default value) <br />- thin<br /><br /> **Note:** If not specified, the default value is none.|

- The IBM DS8000 Family `pool` value is the pool ID and not the pool name as is used in other storage systems.
- Be sure that the `pool` value is the name of an existing pool on the storage system.
- The `allowVolumeExpansion` parameter is optional but is necessary for using volume expansion. The default value is _false_.

**Note:** Be sure to set the value to true to allow volume expansion.

- The `csi.storage.k8s.io/fstype` parameter is optional. The values that are allowed are _ext4_ or _xfs_. The default value is _ext4_.
- The `volume_name_prefix` parameter is optional.

**Note:** For IBM DS8000 Family, the maximum prefix length is five characters. The maximum prefix length for other systems is 20 characters. <br /><br />For storage systems that use Spectrum Virtualize, the `CSI_` prefix is added as default if not specified by the user.

    
      kind: StorageClass
      apiVersion: storage.k8s.io/v1
      metadata:
        name: demo-storageclass
      provisioner: block.csi.ibm.com
      parameters:
        SpaceEfficiency: deduplicated   # Optional.
        pool: demo-pool
      
        csi.storage.k8s.io/provisioner-secret-name: demo-secret
        csi.storage.k8s.io/provisioner-secret-namespace: default
        csi.storage.k8s.io/controller-publish-secret-name: demo-secret
        csi.storage.k8s.io/controller-publish-secret-namespace: default
        csi.storage.k8s.io/controller-expand-secret-name: demo-secret
        csi.storage.k8s.io/controller-expand-secret-namespace: default
      
        csi.storage.k8s.io/fstype: xfs   # Optional. Values ext4\xfs. The default is ext4.
        volume_name_prefix: demoPVC      # Optional.
      allowVolumeExpansion: true
    

Apply the storage class.

  ```
  kubectl apply -f demo-storageclass.yaml
  ```

The `storageclass.storage.k8s.io/demo-storageclass created` message is emitted.

<br/>
<br/>
<br/>

## Driver usage
### <a name=PVC-fs></a>Creating PVC for volume with Filesystem

Create a PVC yaml file, similar to the following demo-pvc-file-system.yaml file, with the size of 1 Gb.

**Note:** `volumeMode` is an optional field. `Filesystem` is the default if the value is not added.

<pre>
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: demo-pvc-file-system
spec:
  volumeMode: <b>Filesystem</b>  # Optional. The default is Filesystem.
  accessModes:
  - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
  storageClassName: demo-storageclass
</pre>
      

```
kubectl apply -f <filename>.yaml
```
The `persistentvolumeclaim/<filename> created` message is emitted.

### Creating a StatefulSet with file system volumes

Create a StatefulSet yaml file, similar to the following demo-statefulset-file-system.yaml file.

<pre>
kind: StatefulSet
apiVersion: apps/v1
metadata:
  name: demo-statefulset-file-system
spec:
  selector:
    matchLabels:
      app: demo-statefulset
  serviceName: demo-statefulset
  replicas: 1
  template:
    metadata:
      labels:
        app: demo-statefulset
    spec:
      containers:
      - name: demo-container
        image: registry.access.redhat.com/ubi8/ubi:latest
        command: [ "/bin/sh", "-c", "--" ]
        args: [ "while true; do sleep 30; done;" ]
        <b>volumeMounts:
          - name: demo-volume-file-system
            mountPath: "/data"</b>
      volumes:
      - name: demo-volume-file-system
        persistentVolumeClaim:
          claimName: demo-pvc-file-system
</pre>

```
kubectl apply -f <filename>.yaml
```

The `statefulset.apps/<filename> created` message is emitted.

```
kubectl get pod demo-statefulset-0
NAME                             READY   STATUS    RESTARTS   AGE
demo-statefulset-file-system-0   1/1     Running   0          43s
```

Review the mountpoint inside the pod:
```
kubectl exec demo-statefulset-file-system-0 -- bash -c "df -h /data"
Filesystem          Size  Used Avail Use% Mounted on
/dev/mapper/mpathz 1014M   33M  982M   4% /data
```


Delete StatefulSet and PVC

```sh
$> kubectl delete statefulset/demo-statefulset-file-system
statefulset/demo-statefulset-file-system deleted

$> kubectl get statefulset/demo-statefulset-file-system
No resources found.

$> kubectl delete pvc/demo-pvc-file-system
persistentvolumeclaim/demo-pvc-file-system deleted

$> kubectl get pv,pvc
No resources found.
```

<br/>
<br/>
<br/>

## Upgrading

To manually upgrade the CSI (Container Storage Interface) driver from a previous version with GitHub, perform step 1 of the [installation procedure](#installing-the-driver) for the latest version.

## Uninstalling the driver

Use this information to uninstall the IBM® CSI (Container Storage Interface) operator and driver with GitHub.

Perform the following steps in order to uninstall the CSI driver and operator.
1.  Delete the IBMBlockCSI custom resource.

    ```
    kubectl -n <namespace> delete -f csi.ibm.com_v1_ibmblockcsi_cr.yaml
    ```

2.  Delete the operator.

    ```
    kubectl -n <namespace> delete -f ibm-block-csi-operator.yaml
    ```



<br/>
<br/>
<br/>

## Licensing

Copyright 2020 IBM Corp.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

