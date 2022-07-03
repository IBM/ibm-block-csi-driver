# Creating a VolumeSnapshotClass with topology awareness

When using the CSI Topology feature, different parameters must be taken into account when creating a VolumeSnapshotClass YAML file with specific `by_management_id` requirements. Use this information to help define a VolumeSnapshotClass that is topology aware and enables the creation and deletion of volume snapshots.

**Note:** 
  - For information and parameter definitions that are not related to topology awareness, be sure to see the information that is provided in [Creating a VolumeSnapshotClass](creating_volumesnapshotclass.md), in addition to the current section.
  
  - This section refers to both the IBM FlashCopy® function and Snapshot function in Spectrum Virtualize storage systems.

In order to enable creation and deletion of volume snapshots for your storage system, create a VolumeSnapshotClass YAML file, similar to the following `demo-volumesnapshotclass-config-secret.yaml`.

  The `by_management_id` parameter is optional and values such as the `pool`, `SpaceEfficiency`, and `volume_name_prefix` can all be specified.

The various `by_management_id` parameters are chosen within the following hierarchical order:
1. From within the `by_management_id` parameter, per system (if specified).
2. Outside of the parameter, as a cross-system default (if not specified within the `by_management_id` parameter for the relevant `management-id`).
    
```
apiVersion: snapshot.storage.k8s.io/v1beta1
kind: VolumeSnapshotClass
metadata:
  name: demo-volumesnapshotclass-config-secret
driver: block.csi.ibm.com
deletionPolicy: Delete
parameters:
  # non-csi.storage.k8s.io parameters may be specified in by_management_id per system and/or outside by_management_id as the cross-system default.

  by_management_id: '{"demo-management-id-1":{"pool":"demo-pool-1","SpaceEfficiency":"dedup_compressed","snapshot_name_prefix":"demo-prefix-1"},
                      "demo-management-id-2":{"pool":"demo-pool-2","snapshot_name_prefix":"demo-prefix-2"}}'  # Optional.
  pool: demo-pool                    # Optional. Use to create the snapshot on a different pool than the source.
  SpaceEfficiency: thin              # Optional. Use to create the snapshot with a different space efficiency than the source.
  snapshot_name_prefix: demo-prefix  # Optional.

  csi.storage.k8s.io/snapshotter-secret-name: demo-config-secret
  csi.storage.k8s.io/snapshotter-secret-namespace: default
```

After the YAML file is created, apply it by using the `kubectl apply -f` command.

```
kubectl apply -f <filename>.yaml
```

 The `volumesnapshotclass.snapshot.storage.k8s.io/<volumesnapshotclass-name> created` message is emitted.