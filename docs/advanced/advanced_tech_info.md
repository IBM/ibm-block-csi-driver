# Advanced technical information

This document provides advanced technical information. Use it to help with advanced configuration settings and troubleshooting that you may need while installing and using the IBM block storage CSI driver.

**NOTE:** This is not a complete user guide. For full documentation, see [IBM block storage CSI driver documentation](https://www.ibm.com/docs/en/stg-block-csi-driver).

- [Compatibility and Requirements](#Compatibility-and-Requirements)
- [Running a stateful container with file system configurations](#Running-a-stateful-container-with-file-system-configurations)
- [Running a stateful container with raw block volume configurations](#Running-a-stateful-container-with-raw-block-volume-configurations)
- [Troubleshooting](#troubleshooting)

## Compatibility and Requirements

- For iSCSI single path users (RHEL only) verify that multipathing is installed and running.

  Define a virtual multipath. For example, remove `find_multipaths yes` from the multipath.conf file.

  For example, to configure a Linux multipath device, verify that the `find_multipaths` parameter in the multipath.conf file is disabled by removing the `find_multipaths yes` string from the file.

  Be sure that there is at least one multipath defined. If not, define a virtual multipath \(if single\) - for example, for RHEL.

- Ensure iSCSI connectivity for RHEL users:

    -   `iscsi-initiator-utils` \(if iSCSI connection is required\)
    -   `xfsprogs` \(if XFS file system is required\)
    ```screen
    yum -y install iscsi-initiator-utils
    yum -y install xfsprogs
    ```


## Running a stateful container with file system configurations

1.  Create an array secret, as described in [Creating a Secret](docs/csi_ug_config_create_secret.md).

2.  Create a storage class, as described in [Creating a StorageClass](docs/csi_ug_config_create_storageclasses.md).

3.  Create a PVC demo-pvc-file-system.yaml with the size of 1 Gb, as described in [Creating a PersistentVolumeClaim \(PVC\)](docs/csi_ug_config_create_pvc.md).

4.  Display the existing PVC and the created persistent volume \(PV\).

    ```screen
    $> kubectl get pv,pvc
    NAME                                                        CAPACITY   ACCESS MODES
    persistentvolume/pvc-828ce909-6eb2-11ea-abc8-005056a49b44   1Gi        RWO
    
    RECLAIM POLICY   STATUS   CLAIM              STORAGECLASS   REASON   AGE
    Delete           Bound    default/demo-pvc-file-system   demo-storageclass 109m
    
    NAME                             STATUS   VOLUME                                     CAPACITY   
    persistentvolumeclaim/demo-pvc-file-system   Bound    pvc-828ce909-6eb2-11ea-abc8-005056a49b44   1Gi
    
    ACCESS MODES   STORAGECLASS   AGE
    RWO            demo-storageclass           78s
    
    $\> kubectl describe persistentvolume/pvc-828ce909-6eb2-11ea-abc8-005056a49b44
    Name:            pvc-828ce909-6eb2-11ea-abc8-005056a49b44
    Labels:          <none\>
    Annotations:     pv.kubernetes.io/provisioned-by: block.csi.ibm.com
    Finalizers:      \[kubernetes.io/pv-protection external-attacher/block-csi-ibm-com\]
    StorageClass:    demo-storageclass
    Status:          Bound
    Claim:           default/demo-pvc-file-system
    Reclaim Policy:  Delete
    Access Modes:    RWO
    VolumeMode:      Filesystem
    Capacity:        1Gi
    Node Affinity:   <none\>
    Message:
    Source:
        Type:              CSI \(a Container Storage Interface \(CSI\) volume source\)
        Driver:            block.csi.ibm.com
        VolumeHandle:      SVC:60050760718106998000000000000543
        ReadOnly:          false
        VolumeAttributes:      array\_address=baremetal10-cluster.xiv.ibm.com
                               pool\_name=demo-pool
                               storage.kubernetes.io/csiProvisionerIdentity=1585146948772-8081-block.csi.ibm.com
                               storage\_type=SVC
                               volume\_name=demoPVC-828ce909-6eb2-11ea-abc8-005056a49b44
    Events:                <none\>
    ```

5.  Create a StatefulSet.

    ```
    $> kubectl create -f demo-statefulset-file-system.yaml
    statefulset.apps/demo-statefulset-file-system created
    ```

    <pre>
    $> cat demo-statefulset-file-system.yaml
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
            command: \[ "/bin/sh", "-c", "--" \]
            args: \[ "while true; do sleep 30; done;" \]
            <b>volumeMounts:
              - name: demo-volume-file-system
                mountPath: "data"</b>
          volumes:
          - name: demo-volume-file-system
            persistentVolumeClaim:
              claimName: demo-pvc-file-system​
    #      nodeSelector:
    #        kubernetes.io/hostname: HOSTNAME
    </pre>

6.  Check the newly created pod.

    Display the newly created pod \(make sure the pod status is _Running_\).

    ```
    $> kubectl get pod demo-statefulset-file-system-0
    NAME                 READY   STATUS    RESTARTS   AGE
    demo-statefulset-file-system-0   1/1     Running   0          43s
    ```

7.  Write data to the persistent volume of the pod.

    The PV should be mounted inside the pod at `/data`.

    ```
    $> kubectl exec demo-statefulset-file-system-0​​ touch /data/FILE
    $> kubectl exec demo-statefulset-file-system-0​​ ls /data/FILE
    /data/FILE
    ```

8.  Log into the worker node that has the running pod and display the newly attached volume on the node.

    1.  Verify which worker node is running the `pod demo-statefulset-file-system-0​​`.

        ```screen
        $> kubectl describe pod demo-statefulset-file-system-0​​| grep "^Node:"
        Node: k8s-node1/hostname
        ```

    2.  Establish an SSH connection and log into the worker node.

        ```
        $> ssh root@k8s-node1
        ```

    3.  List the multipath devices on the worker node.

        ```screen
        $>[k8s-node1]  multipath -ll
        mpathz (828ce9096eb211eaabc8005056a49b44) dm-3 IBM     ,2145 \(for SVC\)         
        size=1.0G features='1 queue_if_no_path' hwhandler='0' wp=rw
        `-+- policy='service-time 0' prio=1 status=active
          |- 37:0:0:12 sdc 8:32 active ready running
          `- 36:0:0:12 sdb 8:16 active ready running
        
        $>[k8s-node1] ls -l /dev/mapper/mpathz
        lrwxrwxrwx. 1 root root 7 Aug 12 19:29 /dev/mapper/mpathz -> ../dm-3
        ```

    4.  List the physical devices of the multipath mpathz and its mountpoint on the host. \(This is the `/data` inside the stateful pod\).

        ```screen
        $>[k8s-node1]  lsblk /dev/sdb /dev/sdc
        NAME     MAJ:MIN RM SIZE RO TYPE  MOUNTPOINT
        sdb        8:16   0   1G  0 disk  
        └─mpathz 253:3    0   1G  0 mpath /var/lib/kubelet/pods/d67d22b8-bd10-11e9-a1f5-005056a45d5f/volumes/kubernetes.io~csi/pvc-828ce909-6eb2-11ea-abc8-005056a49b44
        sdc        8:32   0   1G  0 disk  
        └─mpathz 253:3    0   1G  0 mpath /var/lib/kubelet/pods/d67d22b8-bd10-11e9-a1f5-005056a45d5f/volumes/kubernetes.io~csi/pvc-828ce909-6eb2-11ea-abc8-005056a49b44
        ```

    5.  View the PV mounted on this host.

        **Note:** All PV mountpoints look like: `/var/lib/kubelet/pods/\*/volumes/kubernetes.io~csi/pvc-\*/mount`

        ```screen
        $>[k8s-node1]  df | egrep pvc
        /dev/mapper/mpathz      1038336    32944   1005392   4% /var/lib/kubelet/pods/d67d22b8-bd10-11e9-a1f5-005056a45d5f/volumes/kubernetes.io~csi/pvc-828ce909-6eb2-11ea-abc8-005056a49b44/mount
        ```

    6.  Details about the driver internal metadata file .stageInfo.json is stored in the k8s PV node stage path `/var/lib/kubelet/plugins/kubernetes.io/csi/pv/<PVC-ID>/globalmount/.stageInfo.json`. The CSI driver creates the metadata file during the NodeStage API and is used at later stages by the NodePublishVolume, NodeUnPublishVolume and NodeUnStage CSI APIs later on.

        ```screen
        $> cat /var/lib/kubelet/plugins/kubernetes.io/csi/pv/pvc-828ce909-6eb2-11ea-abc8-005056a49b44/globalmount/.stageInfo.json
        {"connectivity":"iscsi","mpathDevice":"dm-3","sysDevices":",sdb,sdc"}
        ```

9.  Delete StatefulSet and then recreate, in order to validate data \(/data/FILE\) remains in the persistent volume.

    1.  Delete the StatefulSet.

        ```screen
        $> kubectl delete statefulset/demo-statefulset-file-system
        statefulset/demo-statefulset-file-system deleted
        ```

    2.  Wait until the pod is deleted. Once deleted, the `"demo-statefulset-file-system" not found` is returned.

        ```screen
        $> kubectl get statefulset/demo-statefulset-file-system
        Error from server (NotFound): statefulsets.apps <StatefulSet name> not found
        ```

    3.  Verify that the multipath was deleted and that the PV mountpoint no longer exists by establishing an SSH connection and logging into the worker node.

        ```screen
        $> ssh root@k8s-node1
        
        $>[k8s-node1] df | egrep pvc
        $>[k8s-node1] multipath -ll
        $>[k8s-node1] lsblk /dev/sdb /dev/sdc
        lsblk: /dev/sdb: not a block device
        lsblk: /dev/sdc: not a block device
        ```

    4.  Recreate the StatefulSet and verify that /data/FILE exists.

        ```screen
        $> kubectl create -f demo-statefulset-file-system.yaml
        statefulset/demo-statefulset-file-system created
        
        $> kubectl exec demo-statefulset-file-system-0 ls /data/FILE
        File
        ```

10. Delete StatefulSet and the PVC.

    ```screen
    $> kubectl delete statefulset/demo-statefulset-file-system
    statefulset/demo-statefulset-file-system deleted
    
    $> kubectl get statefulset/demo-statefulset-file-system
    No resources found.
    
    $> kubectl delete pvc/demo-pvc-file-system
    persistentvolumeclaim/demo-pvc-file-system deleted
    
    $> kubectl get pv,pvc
    No resources found.
    ```


## Running a stateful container with raw block volume configurations

1.  Create an array secret, as described in [Creating a Secret](csi_ug_config_create_secret.md).

2.  Create a storage class, as described in [Creating a StorageClass](csi_ug_config_create_storageclasses.md).

3.  Create a PVC with the size of 1 Gb, as described in [Creating a PersistentVolumeClaim \(PVC\)](csi_ug_config_create_pvc.md).

4.  Display the existing PVC and the created persistent volume \(PV\).

    ```screen
    $> kubectl get pv,pvc
    NAME                                                        CAPACITY   ACCESS MODES
    persistentvolume/pvc-828ce909-6eb2-11ea-abc8-005056a49b44   1Gi        RWO
    
    RECLAIM POLICY   STATUS   CLAIM              STORAGECLASS   REASON   AGE
    Delete           Bound    default/demo-pvc-raw-block   demo-storageclass   109m
    
    NAME                             STATUS   VOLUME                                     CAPACITY   
    persistentvolumeclaim/demo-pvc-raw-block   Bound    pvc-828ce909-6eb2-11ea-abc8-005056a49b44   1Gi
    
    ACCESS MODES   STORAGECLASS       AGE
    RWO            demo-storageclass  78s
    
    kubectl describe persistentvolume/pvc-828ce909-6eb2-11ea-abc8-005056a49b44
    Name:            pvc-828ce909-6eb2-11ea-abc8-005056a49b44
    Labels:          <none\>
    Annotations:     pv.kubernetes.io/provisioned-by: block.csi.ibm.com
    Finalizers:      \[kubernetes.io/pv-protection external-attacher/block-csi-ibm-com\]
    StorageClass:    demo-storageclass
    Status:          Bound
    Claim:           default/demo-pvc-raw-block
    Reclaim Policy:  Delete
    Access Modes:    RWO
    VolumeMode:      Block
    Capacity:        1Gi
    Node Affinity:   <none\>
    Message:
    Source:
        Type:              CSI \(a Container Storage Interface \(CSI\) volume source\)
        Driver:            block.csi.ibm.com
        VolumeHandle:      SVC:60050760718106998000000000000543
        ReadOnly:          false
        VolumeAttributes:      array\_address=baremetal10-cluster.xiv.ibm.com
                               pool\_name=demo-pool
                               storage.kubernetes.io/csiProvisionerIdentity=1585146948772-8081-block.csi.ibm.com
                               storage\_type=SVC
                               volume\_name=demoPVC-828ce909-6eb2-11ea-abc8-005056a49b44
    Events:                <none\>
    
    ```

5.  Create a StatefulSet.

    ```
    kubectl create -f demo-statefulset-raw-block.yaml
    statefulset.apps/demo-statefulset-raw-block created
    ```

    <pre>
    $> cat demo-statefulset-raw-block.yaml
    
    kind: StatefulSet
    apiVersion: apps/v1
    metadata:
      name: demo-statefulset-raw-block
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
            command: \[ "/bin/sh", "-c", "--" \]
            args: \[ "while true; do sleep 30; done;" \]
            <b>volumeDevices:
              - name: demo-volume-raw-block
                devicePath: "/dev/block"</b>
          volumes:
          - name: demo-volume-raw-block
            persistentVolumeClaim:
              claimName: demo-pvc-raw-block
    ​
    #      nodeSelector:
    #        kubernetes.io/hostname: HOSTNAME
    </pre>

6.  Check the newly created pod.

    Display the newly created pod \(make sure the pod status is _Running_).

    ```
    kubectl get pod demo-statefulset-raw-block-0
    NAME                 READY   STATUS    RESTARTS   AGE
    demo-statefulset-raw-block-0   1/1     Running   0          43s
    ```

7.  Write data to the persistent volume of the pod.

    The PV should be mounted inside the pod at /dev.

    ```
    kubectl exec demo-statefulset-raw-block-0 -- bash -c " echo "test_block" | dd conv=unblock of=/dev/block"
    0+1 records in
    0+1 records out
    11 bytes copied, 9.3576e-05 s, 118 kB/s
    
    kubectl exec demo-statefulset-raw-block-0 -- bash -c "od -An -c -N 10 /dev/block"
    t e s t _ b l o c k  
    ```

8.  Delete StatefulSet and then recreate, in order to validate data \(test\_block in /dev/block\) remains in the persistent volume.

    1.  Delete the StatefulSet.

        ```screen
        $> kubectl delete statefulset/demo-statefulset-raw-block
        statefulset/demo-statefulset-raw-block deleted
        ```

    2.  Wait until the pod is deleted. Once deleted, the `"demo-statefulset-file-system" not found` is returned.

        ```screen
        $> kubectl get statefulset/demo-statefulset-raw-block
        Error from server (NotFound): statefulsets.apps <StatefulSet name\> not found
        ```

    3.  Recreate the StatefulSet and verify that the content written to /dev/block exists.

        ```screen
        $> kubectl create -f demo-statefulset-raw-block.yaml
        statefulset/demo-statefulset-raw-block created
        
        $\> kubectl exec demo-statefulset-raw-block-0 -- bash -c "od -An -c -N 10 /dev/block"
        t e s t \_ b l o c k 
        ```

9.  Delete StatefulSet and the PVC.

    ```screen
    $> kubectl delete statefulset/demo-statefulset-raw-block
    statefulset/demo-statefulset-raw-block deleted
    
    $> kubectl get statefulset/demo-statefulset-raw-block
    No resources found.
    
    $> kubectl delete pvc/demo-pvc-raw-block
    persistentvolumeclaim/demo-pvc-raw-block deleted
    
    $> kubectl get pv,pvc
    No resources found.
    ```


## Troubleshooting

### Log collection for CSI pods, daemonset, and StatefulSet

```
kubectl get all -n <namespace>  -l csi
```

For example:

```screen
$> kubectl get all -n <namespace> -l csi
NAME READY STATUS RESTARTS AGE
pod/ibm-block-csi-controller-0 6/6 Running 0 2h
pod/ibm-block-csi-node-nbtsg 3/3 Running 0 2h
pod/ibm-block-csi-node-wd5tm 3/3 Running 0 2h
pod/ibm-block-csi-operator-7684549698-hzmfh 1/1 Running 0 2h

NAME DESIRED CURRENT READY UP-TO-DATE AVAILABLE NODE SELECTOR AGE
daemonset.apps/ibm-block-csi-node 2 2 2 2 2 <none> 2h

NAME DESIRED CURRENT UP-TO-DATE AVAILABLE AGE
deployment.apps/ibm-block-csi-operator 1 1 1 1 2h

NAME DESIRED CURRENT READY AGE
replicaset.apps/ibm-block-csi-operator-7684549698 1 1 1 2h

NAME DESIRED CURRENT AGE
statefulset.apps/ibm-block-csi-controller 1 1 2h
```

### Verifying the CSI driver is running

Verify that the CSI driver is running. \(Make sure the csi-controller pod status is Running\).

```screen
$> kubectl get all -n <namespace> -l csi
NAME                                        READY STATUS  RESTARTS  AGE
pod/ibm-block-csi-controller-0              6/6   Running 0         2h
pod/ibm-block-csi-node-nbtsg                3/3   Running 0         2h
pod/ibm-block-csi-node-wd5tm                3/3   Running 0         2h
pod/ibm-block-csi-operator-7684549698-hzmfh 1/1   Running 0         2h

NAME                              DESIRED CURRENT READY UP-TO-DATE  AVAILABLE NODE SELECTOR AGE
daemonset.apps/ibm-block-csi-node 2        2      2     2           2         <none>        2h

NAME                                    DESIRED CURRENT UP-TO-DATE  AVAILABLE AGE
deployment.apps/ibm-block-csi-operator  1       1       1           1         2h

NAME                                              DESIRED CURRENT READY AGE
replicaset.apps/ibm-block-csi-operator-7684549698 1       1       1     2h

NAME                                      DESIRED CURRENT AGE
statefulset.apps/ibm-block-csi-controller 1       1       2h
```

### Multipath troubleshooting

Use this information to help pinpoint potential causes for multipath failures.

-   **Display multipath information \(FC and iSCSI\)**

    Display multipath information, using the sudo multipath -ll command.

    ```screen
    mpathb (3600507680283851530000000000000a6) dm-0 IBM,2145
    size=1.0G features='1 queue_if_no_path' hwhandler='1 alua' wp=rw
    |-+- policy='service-time 0' prio=50 status=active
    | `- 3:0:0:0 sda 8:0  active ready running
    `-+- policy='service-time 0' prio=10 status=enabled
      `- 2:0:0:0 sdb 8:16 active ready running
    ```

-   **Display device attachment**

    Display device attachment information, using the sudo lsblk command.

    ```screen
    NAME     MAJ:MIN RM SIZE RO TYPE  MOUNTPOINT
    sda        8:0    0   1G  0 disk  
    `-mpathb 253:0    0   1G  0 mpath /var/lib/kubelet/pods/c9fee230-6227-11ea-a0b6-52fdfc072182/volumes/kubernetes.io~csi/pvc-32a7e21b-6227-11ea-a0b6-52fdfc
    sdb        8:16   0   1G  0 disk  
    `-mpathb 253:0    0   1G  0 mpath /var/lib/kubelet/pods/c9fee230-6227-11ea-a0b6-52fdfc072182/volumes/kubernetes.io~csi/pvc-32a7e21b-6227-11ea-a0b6-52fdfc
    vda      252:0    0  31G  0 disk  
    |-vda1   252:1    0   1M  0 part  
    |-vda2   252:2    0   1G  0 part  /boot
    `-vda3   252:3    0  30G  0 part  /sysroot
    ```

    To display device attachment information, together with SCSI ID information, use the `sudo lsblk -S` command.

    ```screen
    NAME HCTL       TYPE VENDOR   MODEL             REV TRAN
    sda  3:0:0:0    disk IBM      2145             0000 iscsi
    sdb  2:0:0:0    disk IBM      2145             0000 iscsi
    ```

-   **Check for multipath daemon availability \(FC and iSCSI\)**

    Check for multipath daemon availability, using the `systemctl status multipathd` command.

    ```screen
    multipathd.service - Device-Mapper Multipath Device Controller
       Loaded: loaded (/usr/lib/systemd/system/multipathd.service; enabled; vendor preset: enabled)
       Active: active (running) since Mon 2020-03-09 16:28:37 UTC; 22min ago
     Main PID: 1235 (multipathd)
       Status: "up"
        Tasks: 7
       Memory: 14.1M
          CPU: 131ms
       CGroup: /system.slice/multipathd.service
               └─1235 /sbin/multipathd -d -s
    ```

-   **Check for iSCSI daemon availability**

    Check for iSCSI daemon availability, using the `systemctl status iscsid` command.

    ```screen
    iscsid.service - Open-iSCSI
       Loaded: loaded (/usr/lib/systemd/system/iscsid.service; enabled; vendor preset: disabled)
       Active: active (running) since Mon 2020-03-09 16:28:37 UTC; 22min ago
         Docs: man:iscsid(8)
               man:iscsiadm(8)
     Main PID: 1440 (iscsid)
       Status: "Ready to process requests"
        Tasks: 1 (limit: 26213)
       Memory: 4.7M
          CPU: 27ms
       CGroup: /system.slice/iscsid.service
               └─1440 /usr/sbin/iscsid -f
    ```


### General troubleshooting

Use the following command for general troubleshooting:

```
kubectl get -n <namespace>  csidriver,sa,clusterrole,clusterrolebinding,statefulset,pod,daemonset | grep ibm-block-csi
```

For example:

```screen
$> kubectl get -n csi-ns csidriver,sa,clusterrole,clusterrolebinding,statefulset,pod,daemonset |
grep ibm-block-csi
csidriver.storage.k8s.io/ibm-block-csi-driver 7d

serviceaccount/ibm-block-csi-controller-sa 1 2h
serviceaccount/ibm-block-csi-node-sa 1 2h
serviceaccount/ibm-block-csi-operator 1 2h

clusterrole.rbac.authorization.k8s.io/ibm-block-csi-external-attacher-clusterrole 2h
clusterrole.rbac.authorization.k8s.io/ibm-block-csi-external-provisioner-clusterrole 2h
clusterrole.rbac.authorization.k8s.io/ibm-block-csi-operator 2h

clusterrolebinding.rbac.authorization.k8s.io/ibm-block-csi-external-attacher-clusterrolebinding 2h
clusterrolebinding.rbac.authorization.k8s.io/ibm-block-csi-external-provisioner-clusterrolebinding 2h
clusterrolebinding.rbac.authorization.k8s.io/ibm-block-csi-operator 2h


statefulset.apps/ibm-block-csi-controller 1 1 2h
pod/ibm-block-csi-controller-0 6/6 Running 0 2h
pod/ibm-block-csi-node-nbtsg 3/3 Running 0 2h
pod/ibm-block-csi-node-wd5tm 3/3 Running 0 2h
pod/ibm-block-csi-operator-7684549698-hzmfh 1/1 Running 0 2h

daemonset.extensions/ibm-block-csi-node 2 2 2 2 2 <none> 2h

```

### Error during automatic iSCSI login

If an error during automatic iSCSI login occurs, perform the following steps for manual login:

**Note:** These procedures are applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.

**Note:** This procedure is applicable for both RHEL and RHCOS users. When using RHCOS, use the following:

-   Log into the RHCOS node with the core user \(for example, `ssh core@worker1.apps.openshift.mycluster.net`\)
-   iscsiadm commands must start with sudo

1.  Verify that the node.startup in the /etc/iscsi/iscsid.conf file is set to automatic. If not, set it as required and then restart the iscsid service \(service iscsid restart\).
2.  Discover and log into at least two iSCSI targets on the relevant storage systems.

    **Note:** A multipath device can't be created without at least two ports.

    ```screen
    $> iscsiadm -m discoverydb -t st -p ${STORAGE-SYSTEM-iSCSI-PORT-IP1}:3260 --discover
    $> iscsiadm -m node  -p ${STORAGE-SYSTEM-iSCSI-PORT-IP1} --login
    
    $> iscsiadm -m discoverydb -t st -p ${STORAGE-SYSTEM-iSCSI-PORT-IP2}:3260 --discover
    $> iscsiadm -m node  -p ${STORAGE-SYSTEM-iSCSI-PORT-IP2} --login
    ```

3.  Verify that the login was successful and display all targets that you logged into. The portal value must be the iSCSI target IP address.

    ```screen
    $> iscsiadm -m session -rescanRescanning session [sid: 1, target: {storage system IQN},portal: {storage system iSCSI port IP},{port number}
    ```


