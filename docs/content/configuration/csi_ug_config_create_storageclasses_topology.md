# Creating a StorageClass with topology awareness

When using the CSI Topology feature, different parameters must be taken into account when creating a storage class yaml file with specific `by_system_id` requirements. Use this information to help define a StorageClass that is topology aware.

**Note:** For information and parameter definitions that are not related to topology awareness, be sure to see the information provided in [Creating a StorageClass](csi_ug_config_create_storageclasses.md), in addition to the current section.


The StorageClass file must be defined to contain topology information, based off of the labels that were already defined on the nodes in the cluster (see [Compatibility and requirements](content\installation\csi_ug_requirements.md)). This determines the storage pools that are then served as candidates for PersistentVolumeClaim (PVC) requests made, as well as the subset of nodes that can make use of the volumes provisioned by the CSI driver.

With topology awareness, the StorageClass must have the `volumeBindingMode` set to `WaitForFirstConsumer` (as defined in the yaml example below.) This defines that any PVCs that are requested with this specific StorageClass, will wait to be configured until the CSI driver can see the worker node topology.

The `by_system_id` parameter is optional and values, such as the `pool`, `SpaceEfficiency`, and `volume_name_prefix` may all be specified:
- Within by_system_id, per system
- Outside of the parameter, as a cross-system default
- Both inside and outside of the parameter, as specified above
  
  ```
   kind: StorageClass
   apiVersion: storage.k8s.io/v1
   metadata:
    name: demo-storageclass-config-secret
   provisioner: block.csi.ibm.com
   volumeBindingMode: WaitForFirstConsumer
   parameters:
    # non-csi.storage.k8s.io parameters may be specified in by_system_id per system and/or outside by_system_id as the cross-system default.

    by_system_id: '{"demo-system-id-1":{"pool":"demo-pool-1","SpaceEfficiency":"deduplicated","volume_name_prefix":"demo-prefix-1"},
                  "demo-system-id-2":{"pool":"demo-pool-2","volume_name_prefix":"demo-prefix-2"}}'  # Optional.
    pool: demo-pool
    SpaceEfficiency: thin            # Optional.
    volume_name_prefix: demo-prefix  # Optional.

    csi.storage.k8s.io/fstype: xfs   # Optional. Values ext4\xfs. The default is ext4.
    csi.storage.k8s.io/secret-name: demo-config-secret
    csi.storage.k8s.io/secret-namespace: default
  allowVolumeExpansion: true
  ```
Apply the storage class.

  ```
  kubectl apply -f demo-storageclass-config-secret.yaml
  ```
The `storageclass.storage.k8s.io/demo-storageclass-config-secret created` message is emitted.


