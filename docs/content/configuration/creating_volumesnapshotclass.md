
{{site.data.keyword.attribute-definition-list}}

# Creating a VolumeSnapshotClass

Create a VolumeSnapshotClass YAML file to enable creation and deletion of volume snapshots.

This section refers to both the IBM FlashCopy® function and Snapshot function in IBM Storage Virtualize storage systems.{: note}

In order to enable creation and deletion of volume snapshots for your storage system, create a VolumeSnapshotClass YAML file, similar to the following `demo-volumesnapshotclass.yaml`.

When configuring the file, be sure to use the same array secret and array secret namespace as defined in [Creating a Secret](creating_secret.md).

-   The `snapshot_name_prefix` parameter is optional.

For IBM DS8000® family storage systems, the maximum prefix length is five characters. The maximum prefix length for other systems is 20 characters.{: requirement}

For IBM Storage Virtualize family storage systems, the `CSI` prefix is added as default if not specified by the user.{: tip}

- The `virt_snap_func` parameter is optional but necessary in IBM Storage Virtualize storage systems if using the Snapshot function. To enable the Snapshot function, set the value to _"true"_. The default value is _"false"_. If the value is `"false"` the snapshot will use the FlashCopy function.
    
When electing to set the optional "virt_snap_func" parameter, it **must** also be set with an identical value in the relevant StorageClass yaml.{: requirement}

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
  virt_snap_func: "false"            # Optional. Values "true"/"false". The default is "false". If set, this value MUST be identical to the value set in the StorageClass yaml

  csi.storage.k8s.io/snapshotter-secret-name: demo-secret
  csi.storage.k8s.io/snapshotter-secret-namespace: default
```

After the YAML file is created, apply it by using the `kubectl apply -f` command.

```
kubectl apply -f <filename>.yaml
```
 The `volumesnapshotclass.snapshot.storage.k8s.io/<volumesnapshotclass-name> created` message is emitted.
