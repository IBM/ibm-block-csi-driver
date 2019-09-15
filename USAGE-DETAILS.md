# IBM block storage CSI driver - Usage details



## Driver Usage details
This section shows how to:
- Create k8s secret `a9000-array1` for the storage system. (The example below uses FlashSystem A9000R as an example but the same can be used for FlashSystem 9100).
- Create storage class `gold`.
- Create PVC `demo-pvc`from the storage class `gold` and show some details on the created PVC and PV.
- Create StatefulSet application `demo-statefulset` and observe the mountpoint \ multipath device that was created by the driver. 
- Write some data inside the `demo-statefull`, delete the `demo-statefull` and then create it again, to validate that the data remains.

Create secret and storage class:

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
  username: <VALUE-2 base64>   # Replace with valid username
  password: <VALUE-3 base64>   # Replace with valid password
  management_address: <VALUE-4 base64>   # Replace with valid FlashSystem A9000 management address
  
$> kubectl create -f demo-secret-a9000-array1.yaml
secret/a9000-array1 created

###### Create storage class
$> cat demo-storageclass-gold-A9000R.yaml
kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: gold
provisioner: block.csi.ibm.com
parameters:
  pool: gold

  csi.storage.k8s.io/provisioner-secret-name: a9000-array1
  csi.storage.k8s.io/provisioner-secret-namespace: kube-system
  csi.storage.k8s.io/controller-publish-secret-name: a9000-array1
  csi.storage.k8s.io/controller-publish-secret-namespace: kube-system

  csi.storage.k8s.io/fstype: xfs   # Optional. values ext4/xfs. The default is ext4.
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
    VolumeAttributes:      array_address=<IP>
                           pool_name=gold
                           storage.kubernetes.io/csiProvisionerIdentity=1565550204603-8081-ibm-block-csi-driver
                           storage_type=A9000
                           volume_name=demo1_pvc-a04bd32f-bd0f-11e9-a1f5-005056a45d5f
Events:                <none>

##### View the newly created volume on the storage system side of thing (Using XCLI utility):
$> xcli vol_list pool=gold 
Name                                             Size (GB)   Master Name   Consistency Group   Pool   Creator   Written (GB)   
------------------------------------------------ ----------- ------------- ------------------- ------ --------- -------------- 
demo1_pvc-a04bd32f-bd0f-11e9-a1f5-005056a45d5f   1                                             gold   admin     0

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
```

Display the newly created pod (Make sure that the pod status is Running) and write data to its persistent volume. 

```sh
###### Wait for the pod Status to be Running.
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

Log into the worker node that has the running pod and display the newly attached volume on the node.

```sh
###### Verify which worker node is running the pod demo-statefulset-0 
$> kubectl describe pod demo-statefulset-0| grep "^Node:"
Node: k8s-node1/hostname

###### Establish an SSH connection and log into the worker node
$> ssh k8s-node1

###### List multipath devices on the worker node (view the same `mpathz` that was mentioned above) 
$>[k8s-node1]  multipath -ll
mpathz (36001738cfc9035eb0000000000d1f68f) dm-3 IBM     ,2810XIV         
size=1.0G features='1 queue_if_no_path' hwhandler='0' wp=rw
`-+- policy='service-time 0' prio=1 status=active
  |- 37:0:0:12 sdc 8:32 active ready running
  `- 36:0:0:12 sdb 8:16 active ready running

$>[k8s-node1] ls -l /dev/mapper/mpathz
lrwxrwxrwx. 1 root root 7 Aug 12 19:29 /dev/mapper/mpathz -> ../dm-3


###### List the physical devices of the multipath `mpathz` and its mountpoint on the host. (This is the /data inside the stateful pod). 
$>[k8s-node1]  lsblk /dev/sdb /dev/sdc
NAME     MAJ:MIN RM SIZE RO TYPE  MOUNTPOINT
sdb        8:16   0   1G  0 disk  
└─mpathz 253:3    0   1G  0 mpath /var/lib/kubelet/pods/d67d22b8-bd10-11e9-a1f5-005056a45d5f/volumes/kubernetes.io~csi/pvc-a04bd32f-bd0f-11e9-a1f5
sdc        8:32   0   1G  0 disk  
└─mpathz 253:3    0   1G  0 mpath /var/lib/kubelet/pods/d67d22b8-bd10-11e9-a1f5-005056a45d5f/volumes/kubernetes.io~csi/pvc-a04bd32f-bd0f-11e9-a1f5

###### View the PV mounted on this host 
######  (All PV mountpoints looks like the following: `/var/lib/kubelet/pods/*/volumes/kubernetes.io~csi/pvc-*/mount`) 
$>[k8s-node1]  df | egrep pvc
/dev/mapper/mpathz      1038336    32944   1005392   4% /var/lib/kubelet/pods/d67d22b8-bd10-11e9-a1f5-005056a45d5f/volumes/kubernetes.io~csi/pvc-a04bd32f-bd0f-11e9-a1f5-005056a45d5f/mount


##### Details about the driver internal metadata file `.stageInfo.json` are stored in the k8s PV node stage path `/var/lib/kubelet/plugins/kubernetes.io/csi/pv/<PVC-ID>/globalmount/.stageInfo.json`. The CSI driver creates it during the  NodeStage API, and it is used by the NodePublishVolume, NodeUnPublishVolume, and NodeUnStage CSI APIs later on.

$> cat /var/lib/kubelet/plugins/kubernetes.io/csi/pv/pvc-711b6fef-bcf9-11e9-a1f5-005056a45d5f/globalmount/.stageInfo.json
{"connectivity":"iscsi","mpathDevice":"dm-3","sysDevices":",sdb,sdc"}

```


Delete StatefulSet and restart it in order to validate data (`/data/FILE`) remains in the PV.

```sh
$> kubectl delete statefulset/demo-statefulset
statefulset/demo-statefulset deleted

### Wait until the pod no longer exists (receive return code `"demo-statefulset" not found`)
$> kubectl get statefulset/demo-statefulset
NAME                 READY   STATUS        RESTARTS   AGE
demo-statefulset-0   0/1     Terminating   0          91m


###### Establish an SSH connection and log into the worker node in order to see that the multipath was deleted and that the PV mountpoint no longer exists.
$> ssh k8s-node1

$>[k8s-node1] df | egrep pvc
$>[k8s-node1] multipath -ll
$>[k8s-node1] lsblk /dev/sdb /dev/sdc
lsblk: /dev/sdb: not a block device
lsblk: /dev/sdc: not a block device


###### Recreate the StatefulSet again in order to verify /data/FILE exists
$> kubectl create -f demo-statefulset-with-demo-pvc.yml
statefulset/demo-statefulset created

$> kubectl exec demo-statefulset-0 ls /data/FILE
File
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



## Troubleshooting
```
###### View the CSI pods, daemonset and statefulset:
$> kubectl get all -n kube-system  -l csi
NAME                             READY   STATUS    RESTARTS   AGE
pod/ibm-block-csi-controller-0   5/5     Running   0          9m36s
pod/ibm-block-csi-node-jvmvh     3/3     Running   0          9m36s
pod/ibm-block-csi-node-tsppw     3/3     Running   0          9m36s

NAME                                DESIRED   CURRENT   READY   UP-TO-DATE   AVAILABLE   NODE SELECTOR   AGE
daemonset.apps/ibm-block-csi-node   2         2         2       2            2           <none>          9m36s

NAME                                        READY   AGE
statefulset.apps/ibm-block-csi-controller   1/1     9m36s

###### If pod/ibm-block-csi-controller-0 Status is not Running, troubleshoot by running the following:
$> kubectl describe -n kube-system pod/ibm-block-csi-controller-0

###### View the CSI controller logs
$> kubectl log -f -n kube-system ibm-block-csi-controller-0 ibm-block-csi-controller

###### View the CSI daemonset node logs
$> kubectl log -f -n kube-system ibm-block-csi-node-<PODID> ibm-block-csi-node
```

Additional Driver details:
```
###### If `feature-gates=CSIDriverRegistry` was set to `true` then CSIDriver object for the driver will be automaticaly created. See this by running: 

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
```

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

