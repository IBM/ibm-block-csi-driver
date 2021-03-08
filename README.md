# IBM block storage CSI driver 
The Container Storage Interface (CSI) Driver for IBM block storage systems enables container orchestrators such as Kubernetes to manage the life cycle of persistent storage.

Supported container platforms (and architectures):
  - OpenShift v4.6 (x86, IBM Z, and IBM Power Systems)
  - OpenShift v4.7 (x86, IBM Z, and IBM Power Systems)
  - Kubernetes v1.19 (x86)
  - Kubernetes v1.20 (x86)

Supported IBM storage systems:
  - IBM Spectrum Virtualize Family including IBM SAN Volume Controller (SVC) and IBM FlashSystem® family members built with IBM Spectrum® Virtualize (FlashSystem 5010, 5030, 5100, 5200, 7200, 9100, 9200, 9200R)
  - IBM FlashSystem A9000 and A9000R
  - IBM DS8000 Family

Supported operating systems (and architectures):
  - RHEL 7.x (x86)
  - RHCOS (x86, IBM Z, and IBM Power Systems)

Full documentation can be found on the [IBM Knowledge Center](https://www.ibm.com/support/knowledgecenter/SSRQ8T).

<br/>
<br/>
<br/>

## Prerequisites
> **Note**: The operator can be installed directly from the RedHat OpenShift web console, through the OperatorHub. The prerequisites below also mentioned and can be viewed via OpenShift. 

### Preparing worker nodes
Perform these steps for each worker node in Kubernetes cluster:

#### 1. Perform this step to ensure iSCSI connectivity, when using RHEL OS.
If using RHCOS or if the packages are already installed, continue to the next step.

RHEL 7.x:
```bash
yum -y install iscsi-initiator-utils   # Only if iSCSI connectivity is required
yum -y install xfsprogs                # Only if XFS file system is required
```

#### 2. Configure Linux® multipath devices on the host.

**Important:** Be sure to configure each worker with storage connectivity according to your storage system instructions. 
For more information, find your storage system documentation on [IBM Knowledge Center](https://www.ibm.com/support/knowledgecenter).

##### 2.1 Additional configuration steps for OpenShift® Container Platform users (RHEL and RHCOS). Other users can continue to step 3.

Download and save the following yaml file:
```bash
curl https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/master/deploy/99-ibm-attach.yaml > 99-ibm-attach.yaml
```
This file can be used for both Fibre Channel and iSCSI configurations. To support iSCSI, uncomment the last two lines in the file.

**Important:** The  `99-ibm-attach.yaml` configuration file overrides any files that already exist on your system. Only use this file if the files mentioned in the yaml below are not already created. If one or more have been created, edit this yaml file, as necessary.

Apply the yaml file.
```bash
oc apply -f 99-ibm-attach.yaml
```

#### 3. If needed, enable support for volume snapshots (FlashCopy® function) on your Kubernetes cluster.
For more information and instructions, see the Kubernetes blog post, [Kubernetes 1.17 Feature: Kubernetes Volume Snapshot Moves to Beta](https://kubernetes.io/blog/2019/12/09/kubernetes-1-17-feature-cis-volume-snapshot-beta/).

#### 4. Configure storage system connectivity
##### 4.1. Define the hostname of each Kubernetes node on the relevant storage systems with the valid WWPN (for Fibre Channel) or IQN (for iSCSI) of the node.

##### 4.2. For Fibre Channel, configure the relevant zoning from the storage to the host.


<br/>
<br/>
<br/>


## Installation

### Install the operator


#### 1. Download the manifest from GitHub.

```bash
curl https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/master/deploy/installer/generated/ibm-block-csi-operator.yaml > ibm-block-csi-operator.yaml
```

#### 2. (Optional): If required, update the image fields in the ibm-block-csi-operator.yaml.
**Note**: Updating the namespace to a user-defined namespace may be necessary in order to ensure consistency and avoid trouble installing the operator.

#### 3. Create a namespace.

```bash
$ kubectl create ns <namespace>
```

#### 4. Install the operator, while using a user-defined namespace.

```bash
$ kubectl -n <namespace> apply -f ibm-block-csi-operator.yaml
```

### Verify the operator is running:

```bash
$ kubectl get pod -l app.kubernetes.io/name=ibm-block-csi-operator -n <namespace>
NAME                                    READY   STATUS    RESTARTS   AGE
ibm-block-csi-operator-5bb7996b86-xntss 1/1     Running   0          10m
```

### Create an IBMBlockCSI custom resource


#### 1. Download the manifest from GitHub.
```bash
curl https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/master/deploy/crds/csi.ibm.com_v1_ibmblockcsi_cr.yaml > csi.ibm.com_v1_ibmblockcsi_cr.yaml
```

#### 2. (Optional): If required, update the image fields in the csi.ibm.com_v1_ibmblockcsi_cr.yaml.

#### 3. Install the csi.ibm.com_v1_ibmblockcsi_cr.yaml.

```bash
$ kubectl -n <namespace> apply -f csi.ibm.com_v1_ibmblockcsi_cr.yaml
```

### Verify the driver is running:

```bash
$ kubectl get all -n <namespace>  -l csi
NAME                             READY   STATUS    RESTARTS   AGE
pod/ibm-block-csi-controller-0   6/6     Running   0          9m36s
pod/ibm-block-csi-node-jvmvh     3/3     Running   0          9m36s
pod/ibm-block-csi-node-tsppw     3/3     Running   0          9m36s

NAME                                DESIRED   CURRENT   READY   UP-TO-DATE   AVAILABLE   NODE SELECTOR   AGE
daemonset.apps/ibm-block-csi-node   2         2         2       2            2           <none>          9m36s

NAME                                        READY   AGE
statefulset.apps/ibm-block-csi-controller   1/1     9m36s
```



<br/>
<br/>
<br/>

## Configuring k8s secret and storage class 
In order to use the driver, create the relevant storage classes and secrets, as needed.

This section describes how to:
 1. Create a storage system secret - to define the storage system credentials (user and password) and its address.
 2. Configure the storage class - to define the storage system pool name, secret reference, `SpaceEfficiency`, and `fstype`.

#### 1. Create an array secret 
Create a secret file as follows `array-secret.yaml` and update the relevant credentials:

```
kind: Secret
apiVersion: v1
metadata:
  name: <NAME>
  namespace: <NAMESPACE>
type: Opaque
stringData:
  management_address: <ADDRESS-1, ADDRESS-2> # Array management addresses
  username: <USERNAME>                   # Array username
data:
  password: <PASSWORD base64>            # Array password
```

Apply the secret:

```
$ kubectl apply -f array-secret.yaml
```

To create the secret using a command line terminal, use the following command:
```bash
kubectl create secret generic <NAME> --from-literal=username=<USER> --fromliteral=password=<PASSWORD> --from-literal=management_address=<ARRAY_MGMT> -n <namespace>
```

#### 2. Create storage classes

Create a storage class `demo-storageclass-gold-pvc.yaml` file as follows, with the relevant capabilities, pool and, array secret.

Use the `SpaceEfficiency` parameters for each storage system. These values are not case sensitive:
* IBM FlashSystem A9000 and A9000R
	* Always includes deduplication and compression.
	No need to specify during configuration.
* IBM Spectrum Virtualize Family
	* `thick` (default value, if not specified)
	* `thin`
	* `compressed`
	* `deduplicated`
* IBM DS8000 Family
	* `none` (default value, if not specified)
	* `thin`

```
kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: gold
provisioner: block.csi.ibm.com
parameters:
  SpaceEfficiency: <VALUE>          # Optional: Values applicable for Spectrum Virtualize Family are: thin, compressed, or deduplicated
  pool: <POOL_NAME>	                # DS8000 Family paramater is pool ID

  csi.storage.k8s.io/provisioner-secret-name: <ARRAY_SECRET>
  csi.storage.k8s.io/provisioner-secret-namespace: <ARRAY_SECRET_NAMESPACE>
  csi.storage.k8s.io/controller-publish-secret-name: <ARRAY_SECRET>
  csi.storage.k8s.io/controller-publish-secret-namespace: <ARRAY_SECRET_NAMESPACE>
  csi.storage.k8s.io/controller-expand-secret-name: <ARRAY_SECRET>
  csi.storage.k8s.io/controller-expand-secret-namespace: <ARRAY_SECRET_NAMESPACE>

  csi.storage.k8s.io/fstype: xfs    # Optional: Values ext4/xfs. The default is ext4.
  volume_name_prefix: <prefix_name> # Optional: DS8000 Family maximum prefix length is 5 characters. Maximum prefix length for other systems is 20 characters.
allowVolumeExpansion: true
```

#### 3. Apply the storage class:

```bash
$ kubectl apply -f demo-storageclass-gold-pvc.yaml
storageclass.storage.k8s.io/gold created
```




<br/>
<br/>
<br/>


## Driver usage
Create PVC demo-pvc-file-system using `demo-pvc-file-system.yaml`:

```sh 
$> cat demo-pvc-file-system.yaml
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: demo-pvc-file-system
spec:
  volumeMode: Filesystem
  accessModes:
  - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
  storageClassName: gold
      
$> kubectl apply -f demo-pvc-file-system.yaml
persistentvolumeclaim/demo-pvc-file-system created
```


Create StatefulSet application `demo-statefulset-file-system` that uses the demo-pvc-file-system.

```sh
$> cat demo-statefulset-file-system.yml
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
      - name: container-demo
        image: registry.access.redhat.com/ubi8/ubi:latest
        command: [ "/bin/sh", "-c", "--" ]
        args: [ "while true; do sleep 30; done;" ]
        volumeMounts:
          - name: demo-volume
            mountPath: "/data"
      volumes:
      - name: demo-volume
        persistentVolumeClaim:
          claimName: demo-pvc-file-system

#      nodeSelector:
#        kubernetes.io/hostname: HOSTNAME
      

$> kubectl create -f demo-statefulset-file-system.yml
statefulset/demo-statefulset-file-system created

$> kubectl get pod demo-statefulset-0
NAME                             READY   STATUS    RESTARTS   AGE
demo-statefulset-file-system-0   1/1     Running   0          43s

###### Review the mountpoint inside the pod:
$> kubectl exec demo-statefulset-file-system-0 -- bash -c "df -h /data"
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

In order to upgrade the CSI operator and driver from a previous version, perform [step 1](#1-download-the-manifest-from-github) and [step 4](#4-install-the-operator-while-using-a-user-defined-namespace) from [Installation](#Installation)

## Uninstalling

### 1. Delete the IBMBlockCSI custom resource.
```bash
$ kubectl delete -f csi.ibm.com_v1_ibmblockcsi_cr.yaml
```

### 2. Delete the operator.
```bash
$ kubectl delete -f ibm-block-csi-operator.yaml
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

