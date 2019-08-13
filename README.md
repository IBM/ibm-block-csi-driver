# ibm-block-csi-drive

The Container Storage Interface (CSI) Driver for IBM block storage systems enables container orchestrators such as Kubernetes to manage the life-cycle of persistent storage.

Supported container platforms:
  - Openshift v4.1
  - Kubernetes v1.13

Supported IBM storage systems:
  - IBM FlashSystem 9100
  - IBM Spectrum Virtualize
  - IBM Storwize
  - IBM FlashSystem A9000\R

Supported operating systems:
  - RHEL 7.x (x86 architecture)

DISCLAIMER: The cDriver Installationode is provided as is, without warranty. Any issue will be handled on a best-effort basis.


## Table of content:
* [Prerequisite for Driver Installation](#prerequisite-for-driver-installation)
    - Install Fibre Channel and iSCSI connectivity rpms, multipathing configuration and Configure storage system connectivity.
* [Driver Installation](#driver-installation)
* [Configure k8s storage class and secret](#configure-k8s-storage-class-and-secret)
    - Configure the k8s storage class - to define the storage system pool name, secret referance, SpaceEfficiency(Thin, compressed or Deduplicated) and fstype(xfs\ext4).
    - Storage system secret - to define the storage credential(user and password) and its address.
* [Driver Usage](#driver-usage)
    - Example of how to create PVC and statefulset application, with full detail behind the scenes.
* [Driver Uninstallation](#driver-uninstallation)
* [Roadmap](#roadmap)


## Prerequisite for Driver Installation

### Worker nodes preparation
Perform these steps for each worker node in Kubernetes cluster:

### 1. Install Linux packages to ensure Fibre Channel and iSCSI connectivity
Skip this step, if the packages are already installed.

RHEL 7.x:
```sh
yum -y install sg3_utils
yum -y install iscsi-initiator-utils   # only if iSCSI connectivity is required
yum -y install xfsprogs                # Only if xfs filesystem is required.
```

#### 2. Configure Linux multipath devices on the host. 
Create and set the relevant storage system parameters in the /etc/multipath.conf file. 
You can also use the default multipath.conf file located in the /usr/share/doc/device-mapper-multipath-* directory.
Verify that the `systemctl status multipathd` output indicates that the multipath status is active and error-free.

RHEL 7.x:
```sh
yum install device-mapper-multipath
modprobe dm-multipath
systemctl start multipathd
systemctl status multipathd
multipath -ll
```

Important: When configuring Linux multipath devices, verify that the find_multipaths parameter in the multipath.conf file is disabled. 
  - RHEL 7.x: Remove the find_multipaths yes string from the multipath.conf file.

#### 3. Configure storage system connectivity.
3.1. Define the hostname of each Kubernetes node on the relevant storage systems with the valid WWPN or IQN of the node. 

3.2. For Fiber Chanel, configure the relevant zoning from the storage to the host.

3.3. For iSCSI, perform these three steps.
3.3.1. Make sure that the login used to log in to the iSCSI targets is permanent and remains available after a reboot of the worker node. To do this, verify that the node.startup in the /etc/iscsi/iscsid.conf file is set to automatic. If not, set it as required and then restart the iscsid service `$> service iscsid restart`.

3.3.2. Discover and log into at least two iSCSI targets on the relevant storage
systems.

```sh
$> iscsiadm -m discoverydb -t st -p ${storage system iSCSI port IP}:3260
--discover
$> iscsiadm -m node -p ${storage system iSCSI port IP/hostname} --login
```

3.3.3. Verify that the login was successful and display all targets that you logged in. The portal value must be the iSCSI target IP address.

```sh
$> iscsiadm -m session --rescan
Rescanning session [sid: 1, target: {storage system IQN},
portal: {storage system iSCSI port IP},{port number}
```

End of worker node setup.


### Install CSIDriver CRD - optional
Enabling CSIDriver on Kubernetes (more details -> https://kubernetes-csi.github.io/docs/csi-driver-object.html#enabling-csidriver-on-kubernetes)

In Kubernetes v1.13, because the feature was alpha, it was disabled by default. To enable the use of CSIDriver on these versions, do the following:

1. Ensure the feature gate is enabled via the following Kubernetes feature flag: --feature-gates=CSIDriverRegistry=true
   For example on kubeadm installation add the flag inside the `/etc/kubernetes/manifests/kube-apiserver.yaml`.
2. Either ensure the CSIDriver CRD is automatically installed via the Kubernetes Storage CRD addon OR manually install the CSIDriver CRD on the Kubernetes cluster with the following command:
   ```sh
   $> kubectl create -f https://raw.githubusercontent.com/kubernetes/csi-api/master/pkg/crd/manifests/csidriver.yaml
   ```

If the feature gate was not enabled then CSIDriver for the ibm-block-csi-driver will not be created automatically.







## Driver Installation
This section describe how to install the CSI driver.

Heads up: Soon the driver install method will be via new CSI operator (work in progress at github.com/ibm/ibm-block-csi-driver-operator). But for now the installation method is basic yaml file (`ibm-block-csi-driver.yaml`) with all the driver resources. 

```sh
###### Download the driver yml file from github:
$> curl https://raw.githubusercontent.com/IBM/ibm-block-csi-driver/master/deploy/kubernetes/v1.13/ibm-block-csi-driver.yaml > ibm-block-csi-driver.yaml 

###### Optional: Edit the `ibm-block-csi-driver.yaml` file only if you need to change the driver IMAGE URL. By default its `ibmcom/ibm-block-csi-controller-driver:1.0.0`.

###### Install the driver:
$> kubectl apply -f ibm-block-csi-driver.yaml
serviceaccount/ibm-block-csi-controller-sa created
clusterrole.rbac.authorization.k8s.io/ibm-block-csi-external-provisioner-role created
clusterrolebinding.rbac.authorization.k8s.io/ibm-block-csi-external-provisioner-binding created
clusterrole.rbac.authorization.k8s.io/ibm-block-csi-external-attacher-role created
clusterrolebinding.rbac.authorization.k8s.io/ibm-block-csi-external-attacher-binding created
clusterrole.rbac.authorization.k8s.io/ibm-block-csi-cluster-driver-registrar-role created
clusterrolebinding.rbac.authorization.k8s.io/ibm-block-csi-cluster-driver-registrar-binding created
clusterrole.rbac.authorization.k8s.io/ibm-block-csi-external-snapshotter-role created
clusterrolebinding.rbac.authorization.k8s.io/ibm-block-csi-external-snapshotter-binding created
statefulset.apps/ibm-block-csi-controller created
daemonset.apps/ibm-block-csi-node created
```

Verify driver is running (The csi-controller pod should be in Running state):

```sh
$> kubectl get -n kube-system pod --selector=app=ibm-block-csi-controller
NAME                         READY   STATUS    RESTARTS   AGE
ibm-block-csi-controller-0   5/5     Running   0          10m

$> kubectl get -n kube-system pod --selector=app=ibm-block-csi-node
NAME                       READY   STATUS    RESTARTS   AGE
ibm-block-csi-node-jvmvh   3/3     Running   0          74m
ibm-block-csi-node-tsppw   3/3     Running   0          74m

###### if pod/ibm-block-csi-controller-0 is not in Running state, then troubleshoot by running:
$> kubectl describe -n kube-system pod/ibm-block-csi-controller-0

```

More detail on the installed driver can be viewed as below:

```sh
###### if `feature-gates=CSIDriverRegistry` was set to `true` then CSIDriver object for the driver will be automaticaly created. Can be viewed by running: 

$> kubectl describe csidriver ibm-block-csi-driver
Name:         ibm-block-csi-driver
Namespace:    
Labels:       <none>
Annotations:  <none>
API Version:  csi.storage.k8s.io/v1alpha1
Kind:         CSIDriver
Metadata:
  Creation Timestamp:  2019-07-15T12:04:32Z
  Generation:          1
  Resource Version:    1404
  Self Link:           /apis/csi.storage.k8s.io/v1alpha1/csidrivers/ibm-block-csi-driver
  UID:                 b46db4ed-a6f8-11e9-b93e-005056a45d5f
Spec:
  Attach Required:            true
  Pod Info On Mount Version:  
Events:                       <none>


$> kubectl get -n kube-system  csidriver,sa,clusterrole,clusterrolebinding,statefulset,pod,daemonset | grep ibm-block-csi
csidriver.storage.k8s.io/ibm-block-csi-driver   2019-06-02T09:30:36Z
serviceaccount/ibm-block-csi-controller-sa          1         2m16s
clusterrole.rbac.authorization.k8s.io/ibm-block-csi-cluster-driver-registrar-role                            2m16s
clusterrole.rbac.authorization.k8s.io/ibm-block-csi-external-attacher-role                                   2m16s
clusterrole.rbac.authorization.k8s.io/ibm-block-csi-external-provisioner-role                                2m16s
clusterrole.rbac.authorization.k8s.io/ibm-block-csi-external-snapshotter-role                                2m16s
clusterrolebinding.rbac.authorization.k8s.io/ibm-block-csi-cluster-driver-registrar-binding         2m16s
clusterrolebinding.rbac.authorization.k8s.io/ibm-block-csi-external-attacher-binding                2m16s
clusterrolebinding.rbac.authorization.k8s.io/ibm-block-csi-external-provisioner-binding             2m16s
clusterrolebinding.rbac.authorization.k8s.io/ibm-block-csi-external-snapshotter-binding             2m16s
statefulset.apps/ibm-block-csi-controller   1/1     2m16s
pod/ibm-block-csi-controller-0              5/5     Running   0          2m16s
pod/ibm-block-csi-node-xnfgp                3/3     Running   0          13m
pod/ibm-block-csi-node-zgh5h                3/3     Running   0          13m
daemonset.extensions/ibm-block-csi-node     2       2         2          2            2           <none>                        13m

###### Watch the CSI controller logs
$> kubectl log -f -n kube-system ibm-block-csi-controller-0 ibm-block-csi-controller

###### Watch the CSI node logs (per worker node \ PODID)
$> kubectl log -f -n kube-system ibm-block-csi-node-<PODID> ibm-block-csi-node

```


## Configure k8s storage class and secret
The driver is running but in order to use it, one should create the relevant storage classes and secrets as needed.

This section describe how to:
 1. Configure the k8s storage class - to define the storage system pool name, secret referance, SpaceEfficiency(Thin, compressed or Deduplicated) and fstype(xfs\ext4).
 2. Storage system secret - to define the storage credential(user and password) and its address.

#### 1. Create array secret 
Create a secret file as follow and update the relevant credentials:

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

Create a storage class yaml file as follow with the relevant capabilities, pool and array secret:

```sh
kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: gold
provisioner: ibm-block-csi-driver
parameters:
  SpaceEfficiency=<VALUE>   # Values applicable for Storewize: Thin, compressed or Deduplicated
  pool=<VALUE_POOL_NAME>

  csi.storage.k8s.io/provisioner-secret-name: <VALUE_ARRAY_SECRET>
  csi.storage.k8s.io/provisioner-secret-namespace: <VALUE_ARRAY_SECRET_NAMESPACE>
  csi.storage.k8s.io/controller-publish-secret-name: <VALUE_ARRAY_SECRET>
  csi.storage.k8s.io/controller-publish-secret-namespace: <VALUE_ARRAY_SECRET_NAMESPACE>

  #csi.storage.k8s.io/fstype: <VALUE_FSTYPE>   # Optional. values ext4\xfs. The default is ext4.
```

Apply the storage class:

```sh
$> kubectl apply -f array-secret.yaml
storageclass.storage.k8s.io/gold created
```
Now you can run stateful applications using IBM block storage systems.




## Driver Usage
This section describes:
- Create k8s secret `a9000-array1` for the storage system (Using A9000R as example, same can be used for FS9100).
- Create storage class `gold`.
- Create PVC `demo-pvc`from the storage class `gold` and show some detail on the created PVC and PV.
- Create statefulset application `demo-statefulset` and observe the mountpoint \ multipath device that was created by the driver. 
- Write some data inside the `demo-statefull`, delete the `demo-statefull` and create it again to validate that the data remains.

```sh
###### Create secret 
$> cat demo-secret-a9000-array1.yaml
kind: Secret
apiVersion: v1
metadata:
  name: a9000-array1
  namespace: kube-system
type: Opaque
data:
  username: <VALUE-2 base64>   # replace with valid user
  password: <VALUE-3 base64>   # replace with valid password
  management_address: <VALUE-4 base64>   # replace with valid A9000 managment address
  
$> kubectl create -f demo-secret-a9000-array1.yaml
secret/a9000-array1 created

###### Create storage class
$> cat demo-storageclass-gold-A9000R.yaml
kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: gold
provisioner: ibm-block-csi-driver
parameters:
  pool: gold

  csi.storage.k8s.io/provisioner-secret-name: a9000-array1
  csi.storage.k8s.io/provisioner-secret-namespace: kube-system
  csi.storage.k8s.io/controller-publish-secret-name: a9000-array1
  csi.storage.k8s.io/controller-publish-secret-namespace: kube-system

  csi.storage.k8s.io/fstype: xfs   # Optional. values ext4\xfs. The default is ext4.
  volume_name_prefix: demo1        # Optional.

$> kubectl create -f demo-storageclass-gold-A9000R.yaml
storageclass.storage.k8s.io/gold created
```

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

View the PVC and the created PV:

```sh
$> kubectl get pv,pvc
NAME                                                        CAPACITY   ACCESS MODES   RECLAIM POLICY   STATUS   CLAIM              STORAGECLASS   REASON   AGE
persistentvolume/pvc-a04bd32f-bd0f-11e9-a1f5-005056a45d5f   1Gi        RWO            Delete           Bound    default/demo-pvc   gold                    78s

NAME                             STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS   AGE
persistentvolumeclaim/demo-pvc   Bound    pvc-a04bd32f-bd0f-11e9-a1f5-005056a45d5f   1Gi        RWO            gold           78s


$> kubectl describe persistentvolume/pvc-a04bd32f-bd0f-11e9-a1f5-005056a45d5f
Name:            pvc-a04bd32f-bd0f-11e9-a1f5-005056a45d5f
Labels:          <none>
Annotations:     pv.kubernetes.io/provisioned-by: ibm-block-csi-driver
Finalizers:      [kubernetes.io/pv-protection]
StorageClass:    gold
Status:          Bound
Claim:           default/demo-pvc
Reclaim Policy:  Delete
Access Modes:    RWO
VolumeMode:      Filesystem
Capacity:        1Gi
Node Affinity:   <none>
Message:         
Source:
    Type:              CSI (a Container Storage Interface (CSI) volume source)
    Driver:            ibm-block-csi-driver
    VolumeHandle:      A9000:6001738CFC9035EB0000000000D1F68F
    ReadOnly:          false
    VolumeAttributes:      array_name=<IP>
                           pool_name=gold
                           storage.kubernetes.io/csiProvisionerIdentity=1565550204603-8081-ibm-block-csi-driver
                           storage_type=A9000
                           volume_name=demo1_pvc-a04bd32f-bd0f-11e9-a1f5-005056a45d5f
Events:                <none>
```



Create statefulset application `demo-statefulset` that uses the demo-pvc.

```sh
$> cat demo-statefulset-with-demo-pvc.yml
kind: StatefulSet
apiVersion: apps/v1
metadata:
  name: demo-statefulset
  namespace: kube-system
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
```

Display the newly created pod(make sure that the pod status is Running) and write data to its persistent volume. 

```sh
###### Wait for the pod to be in Running state.
$> kubectl get pod demo-statefulset-0
NAME                 READY   STATUS    RESTARTS   AGE
demo-statefulset-0   1/1     Running   0          43s


###### Review the mountpoint inside the pod:
$> kubectl exec demo-statefulset-0 -- bash -c "df -h /data"
Filesystem          Size  Used Avail Use% Mounted on
/dev/mapper/mpathz 1014M   33M  982M   4% /data

$> kubectl exec demo-statefulset-0 -- bash -c "mount | grep /data"
/dev/mapper/mpathz on /data type xfs (rw,relatime,seclabel,attr2,inode64,noquota)


###### Write data in the PV inside the demo-statefulset-0 pod  (the PV mounted inside the pod at /data)
$> kubectl exec demo-statefulset-0 touch /data/FILE
$> kubectl exec demo-statefulset-0 ls /data/FILE
File

```

Log in to the worker node that has the running pod and display the newly attached volume on the node.

```sh
###### Verify which worker node runs the pod demo-statefulset-0 
$> kubectl describe pod demo-statefulset-0| grep "^Node:"
Node: k8s-node1/hostname

###### ssh login to the worker node
$> ssh k8s-node1

###### List multipath devices on the worker node (watch the same `mpathz` that was mentioned above) 
$>[k8s-node1]  multipath -ll
mpathz (36001738cfc9035eb0000000000d1f68f) dm-3 IBM     ,2810XIV         
size=1.0G features='1 queue_if_no_path' hwhandler='0' wp=rw
`-+- policy='service-time 0' prio=1 status=active
  |- 37:0:0:12 sdc 8:32 active ready running
  `- 36:0:0:12 sdb 8:16 active ready running

$>[k8s-node1] ls -l /dev/mapper/mpathz
lrwxrwxrwx. 1 root root 7 Aug 12 19:29 /dev/mapper/mpathz -> ../dm-3


###### List the physical devices of the multipath `mpathz` and its mountpoint on the host (which is the /data inside the statefull pod). 
$>[k8s-node1]  lsblk /dev/sdb /dev/sdc
NAME     MAJ:MIN RM SIZE RO TYPE  MOUNTPOINT
sdb        8:16   0   1G  0 disk  
└─mpathz 253:3    0   1G  0 mpath /var/lib/kubelet/pods/d67d22b8-bd10-11e9-a1f5-005056a45d5f/volumes/kubernetes.io~csi/pvc-a04bd32f-bd0f-11e9-a1f5
sdc        8:32   0   1G  0 disk  
└─mpathz 253:3    0   1G  0 mpath /var/lib/kubelet/pods/d67d22b8-bd10-11e9-a1f5-005056a45d5f/volumes/kubernetes.io~csi/pvc-a04bd32f-bd0f-11e9-a1f5

###### View the PV mounted on this host 
######  (All PV mountpoints looks like : `/var/lib/kubelet/pods/*/volumes/kubernetes.io~csi/pvc-*/mount`) 
$>[k8s-node1]  df | egrep pvc
/dev/mapper/mpathz      1038336    32944   1005392   4% /var/lib/kubelet/pods/d67d22b8-bd10-11e9-a1f5-005056a45d5f/volumes/kubernetes.io~csi/pvc-a04bd32f-bd0f-11e9-a1f5-005056a45d5f/mount


##### Detail about the driver internal metadata file `.stageInfo.json` stored in the k8s PV node stage path `/var/lib/kubelet/plugins/kubernetes.io/csi/pv/<PVC-ID>/globalmount/.stageInfo.json`. The CSI driver creates it during the  NodeStage API, and it been used by the NodePublishVolume, NodeUnPublishVolume and NodeUnStage CSI APIs later on.

$> cat /var/lib/kubelet/plugins/kubernetes.io/csi/pv/pvc-711b6fef-bcf9-11e9-a1f5-005056a45d5f/globalmount/.stageInfo.json
{"connectivity":"iscsi","mpathDevice":"dm-3","sysDevices":",sdb,sdc"}

```


Delete statefulset and start it again to validate data(`/data/FILE`) remains in the PV.

```sh
$> kubectl delete statefulset/demo-statefulset
statefulset/demo-statefulset deleted

### Wait till pod is no longer exist (get returns `"demo-statefulset" not found`)
$> kubectl get statefulset/demo-statefulset
NAME                 READY   STATUS        RESTARTS   AGE
demo-statefulset-0   0/1     Terminating   0          91m


###### ssh login to the worker node just to see that multipath was deleted and the PV mountpoint is no longer exist.
$> ssh k8s-node1

$>[k8s-node1] df | egrep pvc
$>[k8s-node1] multipath -ll
$>[k8s-node1] lsblk /dev/sdb /dev/sdc
lsblk: /dev/sdb: not a block device
lsblk: /dev/sdc: not a block device


###### Now lets create the statefulset again and verify /data/FILE exist
$> kubectl create -f demo-statefulset-with-demo-pvc.yml
statefulset/demo-statefulset created

$> kubectl exec demo-statefulset-0 ls /data/FILE
File
```


Delete statefulset and PVC

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




## Driver Uninstallation

### Delete the storage class, secret and the driver

```sh
$> kubectl delete storageclass/gold
$> kubectl delete -n kube-system secret/a9000-array1
$> kubectl delete -f ibm-block-csi-driver.yaml

##### Kubernetes version 1.13 automatically creates the CSIDriver `ibm-block-csi-driver`, but it does not delete it automatically when removing the driver manifest. So in order to clean up CSIDriver object, run the following command:
$> kubectl delete CSIDriver ibm-block-csi-driver

```


## Roadmap
- CSI Operator as improved deployment method -> [github.com/ibm/ibm-block-csi-driver-operator](github.com/ibm/ibm-block-csi-driver-operator)
- Openshift 4.2 support (+CoreOS worker nodes)
- CSI Snapshots
- NVME support
- Stay tune...

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

