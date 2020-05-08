# IBM block storage CSI driver - Usage Details



## Driver Usage Details
This section shows how to:
- Create k8s secret `svc-secret` for the storage system.
- Create storage class `gold`.
- Create PVC `demo-pvc-file-system`from the storage class `gold` and show some details on the created PVC and PV.
- Create StatefulSet application `demo-statefulset-file-system` and observe the mountpoint \ multipath device that was created by the driver. 
- Write some data inside the 'demo-statefulset-file-system', delete the 'demo-statefulset-file-system' and then create it again, to validate that the data remains.

Create secret and storage class:

```sh
###### Create secret 
$> cat demo-secret.yaml
kind: Secret
apiVersion: v1
metadata:
  name: svc-array
  namespace: kube-system
type: Opaque
stringData:
  management_address: <ADDRESS_1,ADDRESS_2> # Array management addresses
  username: <USERNAME>                      # Array username
data:
  password: <PASSWORD base64>               # Array password
  
$> kubectl create -f demo-secret.yaml
secret/svc-array created

###### Create storage class
$> cat demo-storageclass-gold-svc.yaml
kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: gold
provisioner: block.csi.ibm.com
parameters:
  SpaceEfficiency: deduplicated
  pool: gold

  csi.storage.k8s.io/provisioner-secret-name: svc-array
  csi.storage.k8s.io/provisioner-secret-namespace: kube-system
  csi.storage.k8s.io/controller-publish-secret-name: svc-array
  csi.storage.k8s.io/controller-publish-secret-namespace: kube-systen

  csi.storage.k8s.io/fstype: xfs   # Optional. values ext4\xfs. The default is ext4.
  volume_name_prefix: demo         # Optional.

$> kubectl create -f demo-storageclass-gold.yaml
storageclass.storage.k8s.io/gold created
```

Create PVC demo-pvc-gold using `demo-pvc-gold.yaml`:

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

View the PVC and the created PV:

```sh
$> kubectl get pv,pvc
NAME                                                    CAPACITY   ACCESS MODES   RECLAIM POLICY   STATUS   CLAIM              STORAGECLASS   REASON   AGE
persistentvolume/pvc-828ce909-6eb2-11ea-abc8-005056a49b44   1Gi        RWO            Delete           Bound    default/demo-pvc   gold                    78s

NAME                                         STATUS   VOLUME                                 CAPACITY   ACCESS MODES   STORAGECLASS   AGE
persistentvolumeclaim/demo-pvc-file-system   Bound    pvc-828ce909-6eb2-11ea-abc8-005056a49b44   1Gi        RWO            gold           78s


$> kubectl describe persistentvolume/pvc-828ce909-6eb2-11ea-abc8-005056a49b44
Name:            pvc-828ce909-6eb2-11ea-abc8-005056a49b44
Labels:          <none>
Annotations:     pv.kubernetes.io/provisioned-by: block.csi.ibm.com
Finalizers:      [kubernetes.io/pv-protection external-attacher/block-csi-ibm-com]
StorageClass:    gold
Status:          Bound
Claim:           default/demo-pvc-file-system
Reclaim Policy:  Delete
Access Modes:    RWO
VolumeMode:      Filesystem
Capacity:        1Gi
Node Affinity:   <none>
Message:
Source:
    Type:              CSI (a Container Storage Interface (CSI) volume source)
    Driver:            block.csi.ibm.com
    VolumeHandle:      SVC:60050760718106998000000000000543
    ReadOnly:          false
    VolumeAttributes:      array_address=baremetal10-cluster.xiv.ibm.com
                           pool_name=csi_svcPool
                           storage.kubernetes.io/csiProvisionerIdentity=1585146948772-8081-
                           block.csi.ibm.com
                           storage_type=SVC
                           volume_name=demo_pvc-828ce909-6eb2-11ea-abc8-005056a49b44
Events:                <none>

##### View the newly created volume on the storage system side of thing (Using XCLI utility):
$> lsvdisk
id  name                                             IO_group_id IO_group_name status mdisk_grp_id mdisk_grp_name capacity  type    FC_id FC_name RC_id RC_name vdisk_UID                        fc_map_count copy_count fast_write_state se_copy_count RC_change compressed_copy_count parent_mdisk_grp_id parent_mdisk_grp_name formatting encrypt volume_id volume_name    function protocol
0   demo_pvc-828ce909-6eb2-11ea-abc8-005056a49b44    0           io_grp0       online 0            gold           1GB       striped                             60050768018F82A010000000000001C4 0            1          not_empty        0             no        1                     0                   gold                  no         no      0                        demo_pvc-828ce909-6eb2-11ea-abc8-005056a49b44     scsi
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
```

Display the newly created pod (make sure that the pod status is Running) and write data to its persistent volume. 

```sh
###### Wait for the pod Status to be Running.
$> kubectl get pod demo-statefulset-file-system-0
NAME                             READY   STATUS    RESTARTS   AGE
demo-statefulset-file-system-0   1/1     Running   0          43s


###### Review the mountpoint inside the pod:
$> kubectl exec demo-statefulset-file-system-0 -- bash -c "df -h /data"
Filesystem          Size  Used Avail Use% Mounted on
/dev/mapper/mpathz 1014M   33M  982M   4% /data

$> kubectl exec demo-statefulset-file-system-0 -- bash -c "mount | grep /data"
/dev/mapper/mpathz on /data type xfs (rw,relatime,seclabel,attr2,inode64,noquota)


###### Write data in the PV inside the demo-statefulset-file-system-0 pod  (the PV mounted inside the pod at /data)
$> kubectl exec demo-statefulset-file-system-0 touch /data/FILE
$> kubectl exec demo-statefulset-file-system-0 ls /data/FILE
File

```

Log into the worker node that has the running pod and display the newly attached volume on the node.

```sh
###### Verify which worker node is running the pod demo-statefulset-file-system-0 
$> kubectl describe pod demo-statefulset-file-system-0| grep "^Node:"
Node: k8s-node1/hostname

###### Establish an SSH connection and log into the worker node
$> ssh k8s-node1

###### List multipath devices on the worker node (view the same `mpathz` that was mentioned above) 
$>[k8s-node1]  multipath -ll
mpathz (828ce9096eb211eaabc8005056a49b44) dm-3 IBM     ,2810XIV         
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
└─mpathz 253:3    0   1G  0 mpath /var/lib/kubelet/pods/d67d22b8-bd10-11e9-a1f5-005056a45d5f/volumes/kubernetes.io~csi/pvc-828ce909-6eb2-11ea-abc8-005056a49b44
sdc        8:32   0   1G  0 disk  
└─mpathz 253:3    0   1G  0 mpath /var/lib/kubelet/pods/d67d22b8-bd10-11e9-a1f5-005056a45d5f/volumes/kubernetes.io~csi/pvc-828ce909-6eb2-11ea-abc8-005056a49b44

###### View the PV mounted on this host 
######  (All PV mountpoints looks like the following: `/var/lib/kubelet/pods/*/volumes/kubernetes.io~csi/pvc-*/mount`) 
$>[k8s-node1]  df | egrep pvc
/dev/mapper/mpathz      1038336    32944   1005392   4% /var/lib/kubelet/pods/d67d22b8-bd10-11e9-a1f5-005056a45d5f/volumes/kubernetes.io~csi/pvc-828ce909-6eb2-11ea-abc8-005056a49b44/mount


##### Details about the driver internal metadata file `.stageInfo.json` are stored in the k8s PV node stage path `/var/lib/kubelet/plugins/kubernetes.io/csi/pv/<PVC-ID>/globalmount/.stageInfo.json`. The CSI driver creates it during the  NodeStage API, and it is used by the NodePublishVolume, NodeUnPublishVolume, and NodeUnStage CSI APIs later on.

$> cat /var/lib/kubelet/plugins/kubernetes.io/csi/pv/pvc-828ce909-6eb2-11ea-abc8-005056a49b44/globalmount/.stageInfo.json
{"connectivity":"iscsi","mpathDevice":"dm-3","sysDevices":",sdb,sdc"}

```


Delete StatefulSet and then restart, in order to validate data (/data/FILE) remains in the persistent volume.

```sh
$> kubectl delete statefulset/demo-statefulset-file-system
statefulset/demo-statefulsetfile-system deleted

### Wait until the pod is deleted. Once deleted the '"demo-statefulset-file-system" not found' is returned.
$> kubectl get statefulset/demo-statefulset-file-system
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
$> kubectl create -f demo-statefulset-file-system.yml
statefulset/demo-statefulset-file-system created

$> kubectl exec demo-statefulset-file-system-0 ls /data/FILE
File
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
$> kubectl log -f -n kube-system ibm-block-csi-controller-0 -c ibm-block-csi-controller

###### View the CSI daemonset node logs
$> kubectl log -f -n kube-system ibm-block-csi-node-<PODID> -c ibm-block-csi-node
```

Additional driver details:
```
###### If `feature-gates=CSIDriverRegistry` was set to `true` then CSIDriver object for the driver will be automatically created. See this by running: 

$> kubectl describe csidriver block.csi.ibm.com
Name:         block.csi.ibm.com
Namespace:    
Labels:       <none>
Annotations:  <none>
API Version:  csi.storage.k8s.io/v1alpha1
Kind:         CSIDriver
Metadata:
  Creation Timestamp:  2019-07-15T12:04:32Z
  Generation:          1
  Resource Version:    1404
  Self Link:           /apis/csi.storage.k8s.io/v1alpha1/csidrivers/block.csi.ibm.com
  UID:                 b46db4ed-a6f8-11e9-b93e-005056a45d5f
Spec:
  Attach Required:            true
  Pod Info On Mount Version:  
Events:                       <none>


$> kubectl get -n kube-system  csidriver,sa,clusterrole,clusterrolebinding,statefulset,pod,daemonset | grep ibm-block-csi
csidriver.storage.k8s.io/block.csi.ibm.com   2019-06-02T09:30:36Z
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

