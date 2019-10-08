# IBM block storage CSI driver 

The IBM block storage CSI driver enables container orchestrators, such as Kubernetes and OpenShift, to manage the life-cycle of persistent storage.

Supported container platforms:
  - OpenShift v4.1
  - Kubernetes v1.13

Supported IBM storage systems:
  - IBM FlashSystem 9100
  - IBM Spectrum Virtualize
  - IBM Storwize
  - IBM FlashSystem A9000/R

Supported operating systems:
  - RHEL 7.x (x86 architecture)

DISCLAIMER: The driver is provided as is, without warranty. Version 0.9.0 of the IBM block storage CSI driver is a beta software version. Do not use this driver for production purposes and do not contact IBM for support. Any issue will be handled on a best-effort basis. 

## Table of content:
* [Prerequisites for driver installation](#prerequisites-for-driver-installation)
    - Install Fibre Channel and iSCSI connectivity rpms, multipath configurations, and configure storage system connectivity.
* [Installing the driver](#installing-the-driver)
* [Configuring k8s secret and storage class](#configuring-k8s-secret-and-storage-class)
    - Configure the k8s storage class - to define the storage system pool name, secret reference, SpaceEfficiency (Thin, Compressed or Deduplicated) and fstype(xfs\ext4)
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
3.1. Define the hostname of each Kubernetes node on the relevant storage systems with the valid WWPN or IQN of the node. 

3.2. For Fibre Channel, configure the relevant zoning from the storage to the host.

3.3. For iSCSI, perform the following steps:

3.3.1. Make sure that the login to the iSCSI targets is permanent and remains available after a reboot of the worker node. To do this, verify that the node.startup in the /etc/iscsi/iscsid.conf file is set to automatic. If not, set it as required and then restart the iscsid service `$> service iscsid restart`.

3.3.2. Discover and log into at least two iSCSI targets on the relevant storage
systems.

```sh
$> iscsiadm -m discoverydb -t st -p ${storage system iSCSI port IP}:3260
--discover
$> iscsiadm -m node -p ${storage system iSCSI port IP/hostname} --login
```

3.3.3. Verify that the login was successful and display all targets that you logged into. The portal value must be the iSCSI target IP address.

```sh
$> iscsiadm -m session --rescan
Rescanning session [sid: 1, target: {storage system IQN},
portal: {storage system iSCSI port IP},{port number}
```

End of worker node setup.


### Installing the CSIDriver CRD (optional)
Enable the CSIDriver on Kubernetes. For more details see https://kubernetes-csi.github.io/docs/csi-driver-object.html#enabling-csidriver-on-kubernetes
.
In Kubernetes v1.13, this feature was disabled by default (the feature was alpha). To enable the use of CSIDriver on this Kubernetes version, perform the the following steps:

**Note:** If the feature gate was not enabled, CSIDriver for the block.csi.ibm.com will not be created automatically.

1. Ensure the feature gate is enabled via the following Kubernetes feature flag: --feature-gates=CSIDriverRegistry=true
   For example, on kubeadm installation, add the flag inside `/etc/kubernetes/manifests/kube-apiserver.yaml`.
2. Perform one of the following:
-Ensure the CSIDriver CRD is automatically installed via the Kubernetes Storage CRD addon 
OR
-Manually install the CSIDriver CRD on the Kubernetes cluster with the following command:
   ```sh
   $> kubectl create -f https://raw.githubusercontent.com/kubernetes/csi-api/master/pkg/crd/manifests/csidriver.yaml
   ```




<br/>
<br/>
<br/>




## Installing the driver
This section describes how to install the CSI driver.

```sh
###### Download the driver yml file from github:
$> curl https://raw.githubusercontent.com/IBM/ibm-block-csi-driver/master/deploy/kubernetes/v1.13/ibm-block-csi-driver.yaml > ibm-block-csi-driver.yaml 

###### Optional: Only edit the `ibm-block-csi-driver.yaml` file if you need to change the driver IMAGE URL. By default, the URL is `ibmcom/ibm-block-csi-driver-controller:0.9.0` and `ibmcom/ibm-block-csi-driver-node:0.9.0`.

###### Install the driver:
$> kubectl apply -f ibm-block-csi-driver.yaml
```

Verify the driver is running. (Make sure the csi-controller pod status is Running):

```sh

$> kubectl get all -n kube-system  -l csi
NAME                             READY   STATUS    RESTARTS   AGE
pod/ibm-block-csi-controller-0   5/5     Running   0          9m36s
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
 1. Create a storage system secret - to define the storage credential (user and password) and its address.
 2. Configure the k8s storage class - to define the storage system pool name, secret reference, SpaceEfficiency (thin, compressed, or deduplicated) and fstype(xfs\ext4).

#### 1. Create an array secret 
Create a secret file as follows and update the relevant credentials:

```
kind: Secret
apiVersion: v1
metadata:
  name: <VALUE-1>
  namespace: kube-system
type: Opaque
data:
  username: <VALUE-2 base64>        # Array username.
  password: <VALUE-3 base64>        # Array password.
  management_address: <VALUE-4 base64,VALUE-5 base64> # Array managment addresses
```

Apply the secret:

```
$> kubectl apply -f array-secret.yaml
```

#### 2. Create storage classes

Create a storage class yaml file `storageclass-gold.yaml` as follows, with the relevant capabilities, pool and, array secret:

```sh
kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: gold
provisioner: block.csi.ibm.com
parameters:
  #SpaceEfficiency: <VALUE>    # Optional: Values applicable for Storwize are: thin, compressed, or deduplicated
  pool: <VALUE_POOL_NAME>

  csi.storage.k8s.io/provisioner-secret-name: <VALUE_ARRAY_SECRET>
  csi.storage.k8s.io/provisioner-secret-namespace: <VALUE_ARRAY_SECRET_NAMESPACE>
  csi.storage.k8s.io/controller-publish-secret-name: <VALUE_ARRAY_SECRET>
  csi.storage.k8s.io/controller-publish-secret-namespace: <VALUE_ARRAY_SECRET_NAMESPACE>

  csi.storage.k8s.io/fstype: xfs   # Optional: Values ext4/xfs. The default is ext4.
```

Apply the storage class:

```sh
$> kubectl apply -f storageclass-gold.yaml
storageclass.storage.k8s.io/gold created
```
You can now run stateful applications using IBM block storage systems.




<br/>
<br/>
<br/>



## Driver usage
Create PVC demo-pvc-gold using `demo-pvc-gold.yaml`:

```sh 
$> cat demo-pvc-gold.yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: pvc-demo
spec:
  accessModes:
  - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
  storageClassName: gold

$> kubectl apply -f demo-pvc-gold.yaml
persistentvolumeclaim/demo-pvc created
```


Create StatefulSet application `demo-statefulset` that uses the demo-pvc.

```sh
$> cat demo-statefulset-with-demo-pvc.yml
kind: StatefulSet
apiVersion: apps/v1
metadata:
  name: demo-statefulset
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
      - name: container1
        image: registry.access.redhat.com/ubi8/ubi:latest
        command: [ "/bin/sh", "-c", "--" ]
        args: [ "while true; do sleep 30; done;" ]
        volumeMounts:
          - name: demo-pvc
            mountPath: "/data"
      volumes:
      - name: demo-pvc
        persistentVolumeClaim:
          claimName: demo-pvc

      #nodeSelector:
      #  kubernetes.io/hostname: NODESELECTOR


$> kubectl create -f demo-statefulset-with-demo-pvc.yml
statefulset/demo-statefulset created

$> kubectl get pod demo-statefulset-0
NAME                 READY   STATUS    RESTARTS   AGE
demo-statefulset-0   1/1     Running   0          43s

###### Review the mountpoint inside the pod:
$> kubectl exec demo-statefulset-0 -- bash -c "df -h /data"
Filesystem          Size  Used Avail Use% Mounted on
/dev/mapper/mpathz 1014M   33M  982M   4% /data
```


Delete StatefulSet and PVC

```sh
$> kubectl delete statefulset/demo-statefulset
statefulset/demo-statefulset deleted

$> kubectl get statefulset/demo-statefulset
No resources found.

$> kubectl delete pvc/demo-pvc
persistentvolumeclaim/demo-pvc deleted

$> kubectl get pv,pvc
No resources found.
```

<br/>
<br/>
<br/>

## Uninstalling the driver

### Delete the storage class, secret, and driver

```sh
$> kubectl delete storageclass/gold
$> kubectl delete -n kube-system secret/a9000-array1
$> kubectl delete -f ibm-block-csi-driver.yaml

##### Kubernetes version 1.13 automatically creates the CSIDriver `block.csi.ibm.com`, but it does not delete it automatically when removing the driver manifest. In order to clean up the CSIDriver object, run the following command:
$> kubectl delete CSIDriver block.csi.ibm.com

```

<br/>
<br/>
<br/>

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

