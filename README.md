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

DISCLAIMER: The code is provided as is, without warranty. Any issue will be handled on a best-effort basis.


## Prerequisite for Driver Installation

### Worker nodes preparation
Perform these steps for each worker node in Kubernetes cluster:

### 1. Install Linux packages to ensure Fibre Channel and iSCSI
connectivity. Skip this step, if the packages are already installed.

RHEL 7.x:
 sg3_utils.
 iscsi-initiator-utils (if iSCSI connection is required).

```sh
sudo yum -y install sg3_utils
sudo yum -y install iscsi-initiator-utils
```

#### 2. Configure Linux multipath devices on the host. Create and set the relevant storage system parameters in the /etc/multipath.conf file. 
You can also use the default multipath.conf file located in the /usr/share/doc/device-mapper-multipath-* directory.
Verify that the systemctl status multipathd output indicates that the multipath status is active and error-free.

RHEL 7.x:

```sh
yum install device-mapper-multipath
sudo modprobe dm-multipath
systemctl start multipathd
systemctl status multipathd
multipath -ll
```

Important: When configuring Linux multipath devices, verify that the find_multipaths parameter in the multipath.conf file is disabled. 
  - RHEL 7.x: Remove the find_multipaths yes string from the multipath.conf file.

#### 3. Configure storage system connectivity.
a. Define the hostname of each Kubernetes node on the relevant storage systems with the valid WWPN or IQN of the node. 

b. For Fiber Chanel, configure the relevant zoning from the storage to the host.

c. For iSCSI, perform these three steps.
– Make sure that the login used to log in to the iSCSI targets is permanent and remains available after a reboot of the worker node. To do this, verify that the node.startup in the /etc/iscsi/iscsid.conf file is set to automatic. If not, set it as required and then restart the iscsid service `$> service iscsid restart`.

– Discover and log into at least two iSCSI targets on the relevant storage
systems.

```sh
$> iscsiadm -m discoverydb -t st -p ${storage system iSCSI port IP}:3260
--discover
$> iscsiadm -m node -p ${storage system iSCSI port IP/hostname} --login
```

– Verify that the login was successful and display all targets that you
logged in. The portal value must be the iSCSI target IP address.

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
   #> kubectl create -f https://raw.githubusercontent.com/kubernetes/csi-api/master/pkg/crd/manifests/csidriver.yaml
   ```

If the feature gate was not enabled then CSIDriver for the ibm-block-csi-driver will not be created automatically.




## Driver Installation

### 1. Install the CSI driver
Heads up: For now the driver can be installed by ibm-block-csi-driver.yaml BUT Soon we will allow to install the CSI driver via formal Operator. Stay tune.

```sh

#> curl https://raw.githubusercontent.com/IBM/ibm-block-csi-driver/develop/deploy/kubernetes/v1.13/ibm-block-csi-driver.yaml > ibm-block-csi-driver.yaml 

### Optional: Edit the `ibm-block-csi-driver.yaml` file only if you need to change the driver IMAGE URL. By default its `ibmcom/ibm-block-csi-controller-driver:1.0.0`.

#> kubectl apply -f ibm-block-csi-driver.yaml
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
#> kubectl get -n kube-system pod --selector=app=ibm-block-csi-controller
NAME                         READY   STATUS    RESTARTS   AGE
ibm-block-csi-controller-0   5/5     Running   0          10m

#> kubectl get -n kube-system pod --selector=app=ibm-block-csi-node
NAME                       READY   STATUS    RESTARTS   AGE
ibm-block-csi-node-xnfgp   3/3     Running   0          10m
ibm-block-csi-node-zgh5h   3/3     Running   0          10m

### NOTE if pod/ibm-block-csi-controller-0 is not in Running state, then troubleshoot by running:
#> kubectl describe -n kube-system pod/ibm-block-csi-controller-0

```

Additional info on the driver:
```sh
### if `feature-gates=CSIDriverRegistry` was set to `true` then CSIDriver object for the driver will be automaticaly created. Can be viewed by running: 
#> kubectl describe csidriver ibm-block-csi-driver
Name:         ibm-block-csi-driver
Namespace:    
Labels:       <none>
Annotations:  <none>
API Version:  storage.k8s.io/v1beta1
Kind:         CSIDriver
Metadata:
  Creation Timestamp:  2019-05-22T10:16:03Z
  Resource Version:    6465312
  Self Link:           /apis/storage.k8s.io/v1beta1/csidrivers/ibm-block-csi-driver
  UID:                 9a7c6fd4-7c7a-11e9-a7c0-005056a41609
Spec:
  Attach Required:    true
  Pod Info On Mount:  false
Events:               <none>


#> kubectl get -n kube-system  csidriver,sa,clusterrole,clusterrolebinding,statefulset,pod,daemonset | grep ibm-block-csi
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


#> kubectl get -n kube-system -o jsonpath="{..image}" statefulset.apps/ibm-block-csi-controller | tr -s '[[:space:]]' '\n'; echo ""
ibm/ibm-block-csi-controller-driver:1.0.0
quay.io/k8scsi/csi-cluster-driver-registrar:v1.0.1
quay.io/k8scsi/csi-provisioner:v1.1.1
quay.io/k8scsi/csi-attacher:v1.0.1
quay.io/k8scsi/livenessprobe:v1.1.0

#> kubectl get -n kube-system -o jsonpath="{..image}" daemonset.apps/ibm-block-csi-node | tr -s '[[:space:]]' '\n'; echo ""
ibm/ibm-block-csi-node-driver:1.0.0
quay.io/k8scsi/csi-node-driver-registrar:v1.0.2
quay.io/k8scsi/livenessprobe:v1.1.0

### Watch the CSI controller logs
#> kubectl log -f -n kube-system ibm-block-csi-controller-0 ibm-block-csi-controller

### Watch the CSI node logs (per worker node)
#> kubectl log -f -n kube-system ibm-block-csi-node-<PODID> ibm-block-csi-node

```

#### 2. Create array secret
The driver is running but in order to use it, one should create relevant storage classes.
First create the secret of the array for this cluster.
 
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
#> kubectl apply -f array-secret.yaml
```

#### 3. Create storage classes

Create a storage class yaml file as follow with the relevant capabilities, pool and array secret:
```
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
```
#> kubectl apply -f storage-class.yaml
TODO add out put
```





## Driver Usage

Create storage class with A9000R system using `demo-storageclass-gold-A9000R.yaml`:
```
#> cat demo-storageclass-gold-A9000R.yaml
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

#> kubectl create -f demo-storageclass-gold-A9000R.yaml
TODO output
```

Create `demo-pvc-gold.yaml` file as follow:
```
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

```

Apply the PVC:

```sh
#> kubectl apply -f demo-pvc-gold.yaml
persistentvolumeclaim/pvc-demo created
```

View the PVC and the created PV:

```sh
#> kubectl get pv,pvc
NAME                                                        CAPACITY   ACCESS MODES   RECLAIM POLICY   STATUS   CLAIM          STORAGECLASS   REASON   AGE
persistentvolume/pvc-efc3aae8-7c96-11e9-a7c0-005056a41609   1Gi       RWO            Delete           Bound    default/pvc1   gold                    5s

NAME                         STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS   AGE
persistentvolumeclaim/pvc-demo   Bound    pvc-efc3aae8-7c96-11e9-a7c0-005056a41609  1Gi    RWO            gold           3m51s


#> kubectl describe persistentvolume/pvc-efc3aae8-7c96-11e9-a7c0-005056a41609
Name:            pvc-efc3aae8-7c96-11e9-a7c0-005056a41609
Labels:          <none>
Annotations:     pv.kubernetes.io/provisioned-by: ibm-block-csi-driver
Finalizers:      [kubernetes.io/pv-protection]
StorageClass:    gold
Status:          Bound
Claim:           default/pvc-demo
Reclaim Policy:  Delete
Access Modes:    RWO
VolumeMode:      Filesystem
Capacity:        1Gi
Node Affinity:   <none>
Message:         
Source:
    Type:              CSI (a Container Storage Interface (CSI) volume source)
    Driver:            ibm-block-csi-driver
    VolumeHandle:      A9000:6001738CFC9035EB0000000000D1F111
    ReadOnly:          false
    VolumeAttributes:      array_name=IP
                           pool_name=gold
                           storage.kubernetes.io/csiProvisionerIdentity=1558522090494-8081-ibm-block-csi-driver
                           storage_type=A9000
                           volume_name=pvc-efc3aae8-7c96-11e9-a7c0-005056a41609
Events:                <none>

```



Create statefulset application that uses the demo-pvc.

```sh
#> cat demo-statefulset-with-demo-pvc.yml
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


#> kubectl create demo-statefulset-with-demo-pvc.yml
statefulset/demo-statefulset created
```

Display the newly created pod(make sure that the pod status is Running) and write data to its persistent volume. 

```sh
## Wait for the pod to be in Running state.
#> kubectl get pod demo-statefulset-0
TODO

### Review the mountpoint inside the pod:
#> kubectl exec pod demo-statefulset-0 -- bash -c "df -h /data"
Filesystem
Size Used Avail Use% Mounted on
/dev/mapper/mpathi 951M 33M 919M 4% /data

#> kubectl exec demo-statefulset-0 -- bash -c "mount | grep /data"
/dev/mapper/mpathi on /data type xfs (rw,relatime,seclabel,attr2,inode64,noquota)



### Write data inside the mountpoint in the pod
#> kubectl exec pod demo-statefulset-0 touch /data/FILE

#> kubectl exec pod demo-statefulset-0 ls /data/FILE
File

```

Log in to the worker node that has the running pod and display the newly attached volume on the node.

```sh
#> kubectl describe pod demo-statefulset-0| grep "^Node:"
Node: k8s-node1/hostname

> multipath -ll
mpathi (36001738cfc9035eb0ccccc5) dm-12 IBM
,2810XIV
size=954M features=’1 queue_if_no_path’ hwhandler=’0’ wp=rw
`-+- policy=’service-time 0’ prio=1 status=active
|- 3:0:0:1 sdb 8:16 active ready running
`- 4:0:0:1 sdc 8:32 active ready running

#> df | egrep pvc
TODO
```


Delete statefulset and start it again to validate data remain in the PV.

```sh
#> kubectl delete demo-statefulset
statefulset/demo-statefulset deleted
```




Delete PVC:
```
#> kubectl delete pvc-demo
persistentvolumeclaim/pvc-demo deleted
```




## Un-installation

#### 1. Delete storage class and secret
```
#> kubectl delete -f storage-class.yaml
#> kubectl delete -f array-secret.yaml
```


#### 2. Delete the driver

```sh
#> kubectl delete -f ibm-block-csi-driver.yaml
```

Kubernetes version 1.13 automatically creates the CSIDriver `ibm-block-csi-driver`, but it does not delete it automatically when removing the driver manifest.
So in order to clean up CSIDriver object, run the following command:
```sh
kubectl delete CSIDriver ibm-block-csi-driver
```

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

