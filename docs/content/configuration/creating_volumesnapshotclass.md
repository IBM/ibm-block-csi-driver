
{{site.data.keyword.attribute-definition-list}}

# Creating a VolumeSnapshotClass

Create a VolumeSnapshotClass YAML file to enable creation and deletion of volume snapshots.

This section refers to both the IBM FlashCopy® function and Snapshot function in IBM Storage Virtualize® storage systems.{: note}

In order to enable creation and deletion of volume snapshots for your storage system, create a VolumeSnapshotClass YAML file, similar to the following `demo-volumesnapshotclass.yaml`.

When configuring the file, be sure to use the same array secret and array secret namespace as defined in [Creating a Secret](creating_secret.md).

-   The `snapshot_name_prefix` parameter is optional.

For IBM DS8000® family storage systems, the maximum prefix length is five characters. The maximum prefix length for other systems is 20 characters.{: requirement}

For IBM Storage Virtualize® family storage systems, the `CSI` prefix is added as default if not specified by the user.{: tip}

- The `virt_snap_func` parameter should **NOT** be configured in the VolumeSnapshotClass. This parameter is **only** read from the source volume's StorageClass. Any value specified in the VolumeSnapshotClass will be ignored by the driver.

For IBM Storage Virtualize® storage systems, configure `virt_snap_func` in the StorageClass to control whether snapshots use the Snapshot function (`"true"`) or FlashCopy function (`"false"`, default). See [Creating a StorageClass](creating_storageclass.md) for details.{: tip}

NOTE: In IBM Storage Virtualize® partition environments the flag is ignored - new method is used for taking snapshots

- To create a stretched snapshot on SAN Volume Controller storage systems, put a colon (:) between the two pools within the `pool` value. For example:
  
  `pool: demo-pool1:demo-pool2`
  
The two pools must be from different sites.{: important}

For more information about stretched snapshot limitations and requirements, see [Limitations](../release_notes/limitations.md) and [Compatibility and requirements](../installation/install_compatibility_requirements.md).{: tip}


```
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshotClass
metadata:
  name: demo-volumesnapshotclass
driver: block.csi.ibm.com
deletionPolicy: Delete
parameters:
  pool: demo-pool                    # Optional. Use to create the snapshot on a different pool than the source.
  SpaceEfficiency: thin              # Optional. Use to create the snapshot with a different space efficiency than the source.
  snapshot_name_prefix: demo-prefix  # Optional.
  # NOTE: virt_snap_func should NOT be configured here. It is only read from the StorageClass.

  csi.storage.k8s.io/snapshotter-secret-name: demo-secret
  csi.storage.k8s.io/snapshotter-secret-namespace: default
```

After the YAML file is created, apply it by using the `kubectl apply -f` command.

```
kubectl apply -f <filename>.yaml
```
 The `volumesnapshotclass.snapshot.storage.k8s.io/<volumesnapshotclass-name> created` message is emitted.
