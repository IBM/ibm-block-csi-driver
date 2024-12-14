# Creating a StorageClass

Create a storage class YAML file in order to define the storage parameters, such as pool name, secret reference, `SpaceEfficiency`, and `fstype`.

**Note:** If you are using the CSI Topology feature, in addition to the information and parameter definitions provided here, be sure to follow the steps in [Creating a StorageClass with topology awareness](creating_storageclass_topology_aware.md).

Use the following procedure to create and apply the storage classes.

**Note:** This procedure is applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.

Create a storage class YAML file, similar to the following `demo-storageclass.yaml` and update the storage parameters as needed.

When configuring the file, be sure to use the same array secret and array secret namespace as defined in [Creating a Secret](creating_secret.md).

Use the `SpaceEfficiency` parameters for each storage system, as defined in the following table. These values are not case-sensitive.

**Important:** When using external provisioning policies for linked pools, do not use the `SpaceEfficiency` parameter, the capacity savings within the linked pools are defined by the provisioning policy. If the `SpaceEfficiency` parameter is used together with provisioning policies, the volume cannot be created. For more information see **What's new** > **Getting started with policy-based replication** > **Configuring policy-based replication** > **Creating provisioning policy and assigning to pools**.

#### `SpaceEfficiency` parameter definitions per storage system type

|Storage system type|SpaceEfficiency parameter options|
|-------------------|---------------------------------|
|IBM Storage® Virtualize family|- `thick` (default value)<br />- `thin`<br />- `compressed`<br />- `dedup_thin` (creates volumes that are deduplicated with thin-provisioning)<br />- `dedup_compressed` (creates deduplicated and compressed volumes)<br /><br /> **Note:** <br />- The `deduplicated` value is deprecated. Use `dedup_compressed`, if possible. When used, `deduplicated` provides the same results as `dedup_compressed`.<br />- If not specified, the default value is `thick`.|
|IBM® DS8000® family| - `none` (default value) <br />- `thin`<br /><br /> **Note:** If not specified, the default value is `none`.|

- The IBM DS8000 family `pool` value is the pool ID and not the pool name as is used in other storage systems.
- Be sure that the `pool` value is the name of an existing pool on the storage system.
- To create a volume with high availability (HA) (HyperSwap or stretched topology) on IBM Storage Virtualize storage systems, put a colon (:) between the two pools within the `pool` value. For example:
  
  `pool: demo-pool1:demo-pool2`
  
   **Important:** The two pools must be from different sites.<br>
   **Important:** vdisk protection must be disabled globally or for a specific child pools to be used
   
  For more information about high availability limitations, see [Limitations](../release_notes/limitations.md).
  
  For more information about high availability requirements, see [Compatibility and requirements](../installation/install_compatibility_requirements.md).

- The `allowVolumeExpansion` parameter is optional but is necessary for using volume expansion. The default value is _false_.

  **Note:** Be sure to set the value to _true_ to allow volume expansion.

- The `csi.storage.k8s.io/fstype` parameter is optional. The values that are allowed are _ext4_ or _xfs_. The default value is _ext4_.
- The `volume_name_prefix` parameter is optional.
- The `io_group` and `volume_group` parameters are only available on IBM Storage Virtualize storage systems.
<br><br>**Attention:** Volume groups can only be managed by **either** the associated VolumeGroup **or** the associated StorageClass (with the `volume_group` parameter). If a volume group is already associated with a VolumeGroup, then each volume of this StorageClass can be automatically deleted.
<br><br>**Note:** If no `io_group` is defined, the volume is created within the storage system's default I/O group(s).
- The `virt_snap_func` parameter is optional but necessary in IBM Storage Virtualize storage systems if using the Snapshot function. To enable the Snapshot function, set the value to _"true"_. The default value is _"false"_. If the value is _"false"_ the snapshot will use the FlashCopy function.

    **Note:**
    For IBM DS8000 family storage systems, the maximum prefix length is five characters. The maximum prefix length for other systems is 20 characters. <br /><br />For IBM Storage Virtualize family storage systems, the `CSI` prefix is added as default if not specified by the user.

    
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

      kubectl apply -f <filename>.yaml

The `storageclass.storage.k8s.io/<storageclass-name> created` message is emitted.
