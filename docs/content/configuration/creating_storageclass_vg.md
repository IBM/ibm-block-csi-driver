# Creating a StorageClass with volume groups

Use the following procedure to create and apply the storage classes when using policy-based replication and volume groups.

**Note:** For information and parameter definitions that are not related to topology awareness, be sure to see the information provided in [Creating a StorageClass](creating_volumestorageclass.md), in addition to the current section.

**Note:** This procedure is applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.

**Attention:** Volume groups can only be managed by **either** the associated VolumeGroup **or** the associated StorageClass (with the `volume_group` parameter). If a volume group is already associated with a VolumeGroup, then each volume of this StorageClass can be automatically deleted.

  
      kind: StorageClass
      apiVersion: storage.k8s.io/v1
      metadata:
        name: demo-storageclass
      provisioner: block.csi.ibm.com
      parameters:
        pool: demo-pool
        io_group: demo-iogrp             # Optional.
        volume_group: demo-volumegroup   # Optional.
        SpaceEfficiency: thin            # Optional.
        volume_name_prefix: demo-prefix  # Optional.
        virt_snap_func: "false"          # Optional. Values "true"/"false". The default is "false".

        csi.storage.k8s.io/fstype: xfs   # Optional. Values ext4/xfs. The default is ext4.
        csi.storage.k8s.io/secret-name: demo-secret
        csi.storage.k8s.io/secret-namespace: default
      allowVolumeExpansion: true
    

Apply the storage class.

  ```
  kubectl apply -f <filename>.yaml
  ```

  The `storageclass.storage.k8s.io/<storageclass-name> created` message is emitted.

