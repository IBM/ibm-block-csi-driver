# Operator for IBM block storage CSI driver
The Container Storage Interface (CSI) Driver for IBM block storage systems enables container orchestrators such as Kubernetes to manage the life cycle of persistent storage.

This is the official operator to deploy and manage IBM block storage CSI driver.

Supported container platforms:
  - OpenShift v4.2
  - OpenShift v4.3
  - Kubernetes v1.14
  - Kubernetes v1.16

Supported IBM storage systems:
  - IBM Spectrum Virtualize Family (including IBM Flash family members built with IBM Spectrum Virtualize (FlashSystem 5010, 5030, 5100, 7200, 9100, 9200, 9200R) and IBM SAN Volume Controller (SVC) models SV2, SA2)
  - IBM FlashSystem A9000/R
  - IBM DS8880
  - IBM DS8900

Supported operating systems:
  - RHEL 7.x (x86 architecture)
  - RHCOS (x86 and IBM Z architecture)

Full documentation can be found on the [IBM Knowledge Center](https://www.ibm.com/support/knowledgecenter/SSRQ8T).

## Table of content:
* [Prerequisites for driver installation](#prerequisites-for-driver-installation)
    - Install Fibre Channel and iSCSI connectivity rpms, multipath configurations, and configure storage system connectivity.
* [Installing the driver](#installing-the-driver)
* [Configuring k8s secret and storage class](#configuring-k8s-secret-and-storage-class)
    - Configure the k8s storage class - to define the storage system pool name, secret reference, SpaceEfficiency (Thin, Compressed or Deduplicated) and fstype (xfs\ext4)
    - Storage system secret - to define the storage credential(user and password) and its address
* [Driver usage](#driver-usage)
    - Example of how to create PVC and StatefulSet application, with full detail behind the scenes
* [Uninstalling the driver](#uninstalling-the-driver)
* [More details and troubleshooting](#more-details-and-troubleshooting)


## Prerequisites for driver installation

### Preparing worker nodes
Perform these steps for each worker node in Kubernetes cluster:

#### 1. Install Linux packages to ensure Fibre Channel and iSCSI connectivity
Skip this step if the packages are already installed.

RHEL 7.x:
```sh
yum -y install iscsi-initiator-utils   # Only if iSCSI connectivity is required
yum -y install xfsprogs                # Only if XFS file system is required
```

#### 2. Configure Linux multipath devices on the host 
Create and set the relevant storage system parameters in the `/etc/multipath.conf` file. 
You can also use the default `multipath.conf` file, located in the `/usr/share/doc/device-mapper-multipath-*` directory.
Verify that the `systemctl status multipathd` output indicates that the multipath status is active and error-free.

RHEL 7.x:
```sh
yum install device-mapper-multipath
modprobe dm-multipath
systemctl enable multipathd
systemctl start multipathd
systemctl status multipathd
multipath -ll
```

**Important:** When configuring Linux multipath devices, verify that the `find_multipaths` parameter in the `multipath.conf` file is disabled. In RHEL 7.x, remove the`find_multipaths yes` string from the `multipath.conf` file.

#### 3. Configure storage system connectivity
3.1. Define the hostname of each Kubernetes node on the relevant storage systems with the valid WWPN (for Fibre Channel) or IQN (for iSCSI) of the node. 

3.2. For Fibre Channel, configure the relevant zoning from the storage to the host.

3.3. For iSCSI, perform the following steps:

3.3.1. Make sure that the login to the iSCSI targets is permanent and remains available after a reboot of the worker node. To do this, verify that the node.startup in the /etc/iscsi/iscsid.conf file is set to automatic. If not, set it as required and then restart the iscsid service `$> service iscsid restart`.

End of worker node setup.




<br/>
<br/>
<br/>




## Installing the driver
This section describes how to install the CSI driver.

From version 1.0.0, the deployment method of the driver is done via `Operator for IBM Block CSI Driver` -> https://github.com/ibm/ibm-block-csi-operator.


## Configuring k8s secret and storage class
In order to use the driver, create the relevant storage classes and secrets, as needed.

This section describes how to:
 1. Create a storage system secret - to define the storage system credentials (user and password) and its address.
 2. Configure the k8s storage class - to define the storage system pool name, secret reference, SpaceEfficiency (thin, compressed, or deduplicated) and fstype (xfs\ext4).

#### 1. Create an array secret 
Create a secret file as follows `array-secret.yaml` and update the relevant credentials:

```
kind: Secret
apiVersion: v1
metadata:
  name:  <NAME>
  namespace: <NAMESPACE>
type: Opaque
stringData:
  management_address: <ADDRESS_1,ADDRESS_2> # Array management addresses
  username: <USERNAME>                      # Array username
data:
  password: <PASSWORD base64>               # Array password
```

Apply the secret:

```
$> kubectl apply -f array-secret.yaml
```

#### 2. Create storage classes

Create a storage class yaml file `storageclass-gold-svc.yaml` as follows, with the relevant capabilities, pool and, array secret:

```sh
kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: gold
provisioner: block.csi.ibm.com
parameters:
  SpaceEfficiency: deduplicated
  pool: gold

  csi.storage.k8s.io/provisioner-secret-name: svc-array
  csi.storage.k8s.io/provisioner-secret-namespace: csi-ns
  csi.storage.k8s.io/controller-publish-secret-name: svc-array
  csi.storage.k8s.io/controller-publish-secret-namespace: csi-ns

  csi.storage.k8s.io/fstype: xfs   # Optional. values ext4\xfs. The default is ext4.
  volume_name_prefix: demo         # Optional.
```

Apply the storage class:

```sh
$> kubectl apply -f storageclass-gold-svc.yaml
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
#        kubernetes.io/hostname: NODESELECTOR
      

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

## Uninstalling the driver

From version 1.0.0, the deployment method of the driver is done via `Operator for IBM Block CSI Driver` -> https://github.com/ibm/ibm-block-csi-operator.


## More details and troubleshooting
[USAGE-DETAILS.md](USAGE-DETAILS.md)

<br/>
<br/>
<br/>

## Licensing

Copyright 2019 IBM Corp.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

