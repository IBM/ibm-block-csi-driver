# Running a stateful container with raw block volume configurations

1. Create an array secret, as described in [Creating a Secret](../content/configuration/csi_ug_config_create_secret.md).

2. Create a storage class, as described in [Creating a StorageClass](../content/configuration/csi_ug_config_create_storageclasses.md).

3. Create a PVC with the size of 1 Gb, as described in [Creating a PersistentVolumeClaim (PVC)](../content/configuration/csi_ug_config_create_pvc.md).

4. Display the existing PVC and the created persistent volume (PV).

    <pre>
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
    Labels:          <none>
    Annotations:     pv.kubernetes.io/provisioned-by: block.csi.ibm.com
    Finalizers:      [kubernetes.io/pv-protection external-attacher/block-csi-ibm-com]
    StorageClass:    demo-storageclass
    Status:          Bound
    Claim:           default/demo-pvc-raw-block
    Reclaim Policy:  Delete
    Access Modes:    RWO
    VolumeMode:      Block
    Capacity:        1Gi
    Node Affinity:   <none>
    Message:
    Source:
        Type:              CSI (a Container Storage Interface (CSI) volume source)
        Driver:            block.csi.ibm.com
        VolumeHandle:      SVC:60050760718106998000000000000543
        ReadOnly:          false
        VolumeAttributes:      array\address=baremetal10-cluster.xiv.ibm.com
                              pool\name=demo-pool
                              storage.kubernetes.io/csiProvisionerIdentity=1585146948772-8081-block.csi.ibm.com
                              storage\type=SVC
                              volume\name=demoPVC-828ce909-6eb2-11ea-abc8-005056a49b44
    Events:                <none>

5. Create a StatefulSet.

    <pre>
    kubectl create -f demo-statefulset-raw-block.yaml
    statefulset.apps/demo-statefulset-raw-block created

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
            command: [ "/bin/sh", "-c", "--" ]
            args: [ "while true; do sleep 30; done;" ]
            volumeDevices:
              - name: demo-volume-raw-block
                devicePath: "/dev/block"
          volumes:
          - name: demo-volume-raw-block
            persistentVolumeClaim:
              claimName: demo-pvc-raw-block
        
        # nodeSelector:
        # kubernetes.io/hostname: HOSTNAME

6. Check the newly created pod.

    Display the newly created pod (make sure the pod status is _Running_).

    <pre>
    kubectl get pod demo-statefulset-raw-block-0
    NAME                 READY   STATUS    RESTARTS   AGE
    demo-statefulset-raw-block-0   1/1     Running   0          43s
    </pre>

7. Write data to the persistent volume of the pod.

    The PV should be mounted inside the pod at /dev.

    <pre>
    kubectl exec demo-statefulset-raw-block-0 -- bash -c " echo "test_block" | dd conv=unblock of=/dev/block"
    0+1 records in
    0+1 records out
    11 bytes copied, 9.3576e-05 s, 118 kB/s
        
    kubectl exec demo-statefulset-raw-block-0 -- bash -c "od -An -c -N 10 /dev/block"
    t e s t _ b l o c k
    </pre>

8. Delete StatefulSet and then recreate, in order to validate data (test\block in /dev/block) remains in the persistent volume.

    1. Delete the StatefulSet.

        <pre>
        $> kubectl delete statefulset/demo-statefulset-raw-block
        statefulset/demo-statefulset-raw-block deleted
        </pre>

    2. Wait until the pod is deleted. Once deleted, the `"demo-statefulset-file-system" not found` is returned.

        <pre>
        $> kubectl get statefulset/demo-statefulset-raw-block
        Error from server (NotFound): statefulsets.apps <StatefulSet name> not found

    3. Recreate the StatefulSet and verify that the content written to /dev/block exists.

        <pre>
        $> kubectl create -f demo-statefulset-raw-block.yaml
        statefulset/demo-statefulset-raw-block created
            
        $> kubectl exec demo-statefulset-raw-block-0 -- bash -c "od -An -c -N 10 /dev/block"
        t e s t \ b l o c k
        </pre>

9. Delete StatefulSet and the PVC.
  
    <pre>
    $> kubectl delete statefulset/demo-statefulset-raw-block
    statefulset/demo-statefulset-raw-block deleted
        
    $> kubectl get statefulset/demo-statefulset-raw-block
    No resources found.
        
    $> kubectl delete pvc/demo-pvc-raw-block
    persistentvolumeclaim/demo-pvc-raw-block deleted
        
    $> kubectl get pv,pvc
    No resources found.
    </pre>