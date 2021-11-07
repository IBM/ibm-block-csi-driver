# Running a stateful container

Use this for advanced information on running a stateful container for raw block and file system configurations.

1. Follow the instructions for running a stateful container, as detailed in [Sample configurations for running a stateful container](../content/using/csi_ug_using_sample.md).
2. Check the newly created pod.

    Display the newly created pod (make sure the pod status is _Running_).
    
        $> kubectl get pod <stateful-name>-0
        NAME                READY   STATUS    RESTARTS   AGE
        <stateful-name>-0   1/1     Running   0          43s  
3. Write data to the persistent volume of the pod.

    - For raw block configurations, the PV should be mounted inside the pod at `/dev/block`.
        
            $> kubectl exec <stateful-name>-0 -- bash -c "echo "test_block" | dd conv=unblock of=/dev/block"
            0+1 records in
            0+1 records out
            11 bytes copied, 9.3576e-05 s, 118 kB/s
                
            $> kubectl exec <stateful-name>-0 -- bash -c "od -An -c -N 10 /dev/block"
            t e s t _ b l o c k

    - For file system configurations, the PV should be mounted inside the pod at `/data`.

            $> kubectl exec <stateful-name>-0 -- touch /data/FILE
            $> kubectl exec <stateful-name>-0 -- ls /data/FILE
            /data/FILE
4. Delete StatefulSet and then recreate, in order to validate that the data remains in the persistent volume.
    
    For raw block configurations, the data is `test_block` in `/dev/block`. For file system configurations, the data is `/data/FILE`.
    1. Delete the StatefulSet.
        <pre>
        $> kubectl delete statefulset/statefulset-name
        statefulset/statefulset-name deleted
    2. Wait until the pod is deleted. Once deleted, the `"statefulset-name" not found` is returned.
           
            $> kubectl get statefulset/statefulset-name
            Error from server (NotFound): statefulsets.apps <statefulset-name> not found

    3. **For file system configuration only:** Verify that the multipath was deleted and that the PV mountpoint no longer exists by establishing an SSH connection and logging into the worker node.
          
        <pre>
        $> ssh root@k8s-node1
            
        $>[k8s-node1] df | egrep pvc
        $>[k8s-node1] multipath -ll
        $>[k8s-node1] lsblk /dev/sdb /dev/sdc
        lsblk: /dev/sdb: not a block device
        lsblk: /dev/sdc: not a block device

    4. Recreate the StatefulSet and verify that the data still exists.

        - For raw block configurations, verify that the content written to `/dev/block` exists.
            
                $> kubectl create -f demo-statefulset-raw-block.yaml
                statefulset/statefulset-name created
                    
                $> kubectl exec <stateful-name>-0 -- bash -c "od -An -c -N 10 /dev/block"
                t   e   s   t   _   b   l   o   c   k
    
        - For file system configurations, verify that `/data/FILE` exists.
            
                $> kubectl create -f demo-statefulset-file-system.yaml
                statefulset/statefulset-name created
                    
                $> kubectl exec <stateful-name>-0 -- ls /data/FILE
                /data/FILE
      
5. Delete StatefulSet and the PVC.
    <pre>
    $> kubectl delete statefulset/statefulset-name
    statefulset/statefulset-name deleted
        
    $> kubectl get statefulset/statefulset-name
    Error from server (NotFound): statefulsets.apps "statefulset-name" not found
        
    $> kubectl delete pvc/demo-pvc-raw-block
    persistentvolumeclaim/demo-pvc-raw-block deleted
        
    $> kubectl get pv,pvc
    No resources found.