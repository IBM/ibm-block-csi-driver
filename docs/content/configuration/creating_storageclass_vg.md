
{{site.data.keyword.attribute-definition-list}}

# Creating a StorageClass with volume groups

Use the following procedure to create and apply the storage classes when using policy-based replication and volume groups.

Create a storage class YAML file, similar to the following demo-storageclass.yaml (below) and update the storage parameters as needed.

Volume groups can only be managed by **either** the associated VolumeGroup **or** the associated StorageClass (with the `volume_group` parameter). If a volume group is already associated with a VolumeGroup, then each volume of this StorageClass can be automatically deleted.{: restriction}

When electing to set the optional "virt_snap_func" parameter, it **must** also be set with an identical value in the relevant VolumeSnapshotClass yamls.{: requirement}

When setting the optional "virt_snap_func" parameter to "true", the optional "SpaceEfficiency" parameter **must not** be set.{: restriction}
  
    kind: StorageClass
    apiVersion: storage.k8s.io/v1
    metadata:
      name: demo-storageclass
    provisioner: block.csi.ibm.com
    parameters:
      pool: demo-pool
      io_group: demo-iogrp             # Optional.
      volume_group: demo-volumegroup   # Optional.
      SpaceEfficiency: thin            # Optional. Do not set this optional parameter if virt_snap_func is set to "true"
      volume_name_prefix: demo-prefix  # Optional.
      virt_snap_func: "false"          # Optional. Values "true"/"false". The default is "false". If set, this value MUST be identical to the value set in the VolumeSnapshotClass yamls

      csi.storage.k8s.io/fstype: xfs   # Optional. Values ext4/xfs. The default is ext4.
      csi.storage.k8s.io/secret-name: demo-secret
      csi.storage.k8s.io/secret-namespace: default
    allowVolumeExpansion: true
    
Apply the storage class.

    kubectl apply -f <filename>.yaml

The `storageclass.storage.k8s.io/<storageclass-name> created` message is emitted.

This procedure is applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.{: tip}

For information and parameter definitions that are not related to topology awareness, be sure to see the information provided in [Creating a StorageClass](creating_volumestorageclass.md), in addition to the current section.{: note}
