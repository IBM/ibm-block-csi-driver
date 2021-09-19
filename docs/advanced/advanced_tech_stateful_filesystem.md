## Running a stateful container with file system configurations

1. Create an array secret, as described in [Creating a Secret](../content/configuration/csi_ug_config_create_secret.md). 

2. Create a storage class, as described in [Creating a StorageClass](../content/configuration/csi_ug_config_create_storageclasses.md).

3. Create a PVC demo-pvc-file-system.yaml with the size of 1 Gb, as described in [Creating a PersistentVolumeClaim (PVC)](../content/configuration/csi_ug_config_create_pvc.md).

4. Display the existing PVC and the created persistent volume (PV).

     <pre>
      $> kubectl get pv,pvc
      NAME                                                        CAPACITY   ACCESS MODES
      persistentvolume/pvc-828ce909-6eb2-11ea-abc8-005056a49b44   1Gi        RWO
        
      RECLAIM POLICY   STATUS   CLAIM                          STORAGECLASS     REASON  AGE
      Delete           Bound    default/demo-pvc-file-system   demo-storageclass        109m
        
      NAME                                         STATUS   VOLUME                                     CAPACITY   
      persistentvolumeclaim/demo-pvc-file-system   Bound    pvc-828ce909-6eb2-11ea-abc8-005056a49b44   1Gi
        
      ACCESS MODES   STORAGECLASS       AGE
      RWO            demo-storageclass  78s
        
      $> kubectl describe persistentvolume/pvc-b5336053-46a0-4eb0-a4d8-1d8ef309e108
      Name:            pvc-b5336053-46a0-4eb0-a4d8-1d8ef309e108
      Labels:          <none>
      Annotations:     pv.kubernetes.io/provisioned-by: block.csi.ibm.com
      Finalizers:      [kubernetes.io/pv-protection]
      StorageClass:    demo-storageclass
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
          FSType:            xfs
          VolumeHandle:      SVC:26;60050760718186998000000000005E93
          ReadOnly:          false
          VolumeAttributes:     array_address=demo-management-address
                                pool_name=demo-pool
                                storage.kubernetes.io/csiProvisionerIdentity=1631546133261-8081-block.csi.ibm.com
                                storage_type=SVC
                                volume_name=demo-prefix_pvc-b5336053-46a0-4eb0-a4d8-1d8ef309e108
      Events:                <none>
      </pre>
5. Create a StatefulSet.

    <pre>
    $> kubectl create -f demo-statefulset-file-system.yaml
    statefulset.apps/demo-statefulset-file-system created
    
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
            command: [ "/bin/sh", "-c", "--" ]
              args: [ "while true; do sleep 30; done;" ]
            <b>volumeMounts:
              - name: demo-volume-file-system
                mountPath: "data"</b>
          volumes:
          - name: demo-volume-file-system
            persistentVolumeClaim:
              claimName: demo-pvc-file-system
      #      nodeSelector:
      #        kubernetes.io/hostname: HOSTNAME
    

6. Check the newly created pod.

    Display the newly created pod (make sure the pod status is _Running_).

    <pre>
    $> kubectl get pod demo-statefulset-file-system-0
    NAME                 READY   STATUS    RESTARTS   AGE
    demo-statefulset-file-system-0   1/1     Running   0          43s

7. Write data to the persistent volume of the pod.

    The PV should be mounted inside the pod at `/data`.

    <pre>
    $> kubectl exec demo-statefulset-file-system-0 -- touch /data/FILE
    $> kubectl exec demo-statefulset-file-system-0 -- ls /data/FILE
    /data/FILE

8. Log into the worker node that has the running pod and display the newly attached volume on the node.

    1. Verify which worker node is running the `pod demo-statefulset-file-system-0​​`.

        <pre>
        $> kubectl describe pod demo-statefulset-file-system-0 | grep "^Node:"
        Node: k8s-node1/hostname

    2. Establish an SSH connection and log into the worker node.

        `$> ssh root@k8s-node1`

    3. List the multipath devices on the worker node.

        <pre>
        $>[k8s-node1]  multipath -ll
        mpathz (828ce9096eb211eaabc8005056a49b44) dm-3 IBM     ,2145 (for SVC)         
        size=1.0G features='1 queue_if_no_path' hwhandler='0' wp=rw
        `-+- policy='service-time 0' prio=1 status=active
        |- 37:0:0:12 sdc 8:32 active ready running
        `- 36:0:0:12 sdb 8:16 active ready running
            
        $>[k8s-node1] ls -l /dev/mapper/mpathz
        lrwxrwxrwx. 1 root root 7 Aug 12 19:29 /dev/mapper/mpathz -> ../dm-3

    4. List the physical devices of the multipath mpathz and its mountpoint on the host. (This is the `/data` inside the stateful pod).

       <pre>
       $>[k8s-node1]  lsblk /dev/sdb /dev/sdc
       NAME      MAJ:MIN RM  SIZE  RO  TYPE    MOUNTPOINT
       sdb       8:16    0   1G    0   disk  
       └─mpathz 253:3    0   1G    0   mpath   /var/lib/kubelet/pods/d67d22b8-bd10-11e9-a1f5-005056a45d5f/volumes/kubernetes.io~csi/pvc-828ce909-6eb2-11ea-abc8-005056a49b44
       sdc       8:32    0   1G    0   disk  
       └─mpathz 253:3    0   1G    0   mpath   /var/lib/kubelet/pods/d67d22b8-bd10-11e9-a1f5-005056a45d5f/volumes/kubernetes.io~csi/pvc-828ce909-6eb2-11ea-abc8-005056a49b44

    5. View the PV mounted on this host.

        **Note:** All PV mountpoints look like: `/var/lib/kubelet/pods/\*/volumes/kubernetes.io~csi/pvc-\*/mount`

        <pre>
        $>[k8s-node1]  df | egrep pvc
        /dev/mapper/mpathz      1038336    32944   1005392   4% /var/lib/kubelet/pods/d67d22b8-bd10-11e9-a1f5-005056a45d5f/volumes/kubernetes.io~csi/pvc-828ce909-6eb2-11ea-abc8-005056a49b44/mount

    6. Details about the driver internal metadata file .stageInfo.json is stored in the k8s PV node stage path `/var/lib/kubelet/plugins/kubernetes.io/csi/pv/<PVC-ID>/globalmount/.stageInfo.json`. The CSI driver creates the metadata file during the NodeStage API and is used at later stages by the NodePublishVolume, NodeUnPublishVolume and NodeUnStage CSI APIs later on.

      <pre>        
      $> cat /var/lib/kubelet/plugins/kubernetes.io/csi/pv/pvc-828ce909-6eb2-11ea-abc8-005056a49b44/globalmount/.stageInfo.json
      {"connectivity":"iscsi","mpathDevice":"dm-3","sysDevices":",sdb,sdc"}

9. Delete StatefulSet and then recreate, in order to validate data (/data/FILE) remains in the persistent volume.

    1. Delete the StatefulSet.

        <pre>
        $> kubectl delete statefulset/demo-statefulset-file-system
        statefulset/demo-statefulset-file-system deleted

    2. Wait until the pod is deleted. Once deleted, the `"demo-statefulset-file-system" not found` is returned.

        <pre>
        $> kubectl get statefulset/demo-statefulset-file-system
        Error from server (NotFound): statefulsets.apps <StatefulSet name> not found

    3. Verify that the multipath was deleted and that the PV mountpoint no longer exists by establishing an SSH connection and logging into the worker node.
          
        <pre>
        $> ssh root@k8s-node1
            
        $>[k8s-node1] df | egrep pvc
        $>[k8s-node1] multipath -ll
        $>[k8s-node1] lsblk /dev/sdb /dev/sdc
        lsblk: /dev/sdb: not a block device
        lsblk: /dev/sdc: not a block device

    4. Recreate the StatefulSet and verify that /data/FILE exists.

        <pre>
        $> kubectl create -f demo-statefulset-file-system.yaml
        statefulset/demo-statefulset-file-system created
            
        $> kubectl exec demo-statefulset-file-system-0 -- ls /data/FILE
        File

10. Delete StatefulSet and the PVC.

    <pre>
    $> kubectl delete statefulset/demo-statefulset-file-system
    statefulset/demo-statefulset-file-system deleted
        
    $> kubectl get statefulset/demo-statefulset-file-system
    Error from server (NotFound): statefulsets.apps <StatefulSet name> not found.
        
    $> kubectl delete pvc/demo-pvc-file-system
    persistentvolumeclaim/demo-pvc-file-system deleted
        
    $> kubectl get pv,pvc
    No resources found.