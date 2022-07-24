# Creating a VolumeSnapshotClass

Create a VolumeSnapshotClass YAML file to enable creation and deletion of volume snapshots.

**Note:** IBM® FlashCopy® function is referred to as the more generic volume snapshots and cloning within this documentation set. Not all supported products use the FlashCopy function terminology.

In order to enable creation and deletion of volume snapshots for your storage system, create a VolumeSnapshotClass YAML file, similar to the following `demo-volumesnapshotclass.yaml`.

When configuring the file, be sure to use the same array secret and array secret namespace as defined in [Creating a Secret](creating_secret.md).

-   The `snapshot_name_prefix` parameter is optional.

    **Note:** For IBM DS8000® family, the maximum prefix length is five characters.<br/>The maximum prefix length for other systems is 20 characters.<br/>For storage systems that use Spectrum Virtualize, the `CSI` prefix is added as default if not specified by the user.
    
- To create a stretched snapshot on SAN Volume Controller storage systems, put a colon (:) between the two pools within the `pool` value. For example:
  
  ```
  pool: demo-pool1:demo-pool2 
  ```
   **Important:** The two pools must be from different sites.

   For more information about stretched snapshot limitations and requirements, see [Limitations](../release_notes/limitations.md) and [Compatibility and requirements](../installation/install_compatibility_requirements.md).

-   The `pool` parameter is not available on IBM FlashSystem A9000 and A9000R storage systems. For these storage systems, the snapshot must be created on the same pool as the source.

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

  csi.storage.k8s.io/snapshotter-secret-name: demo-secret
  csi.storage.k8s.io/snapshotter-secret-namespace: default
```

After the YAML file is created, apply it by using the `kubectl apply -f` command.

```
kubectl apply -f <filename>.yaml
```
 The `volumesnapshotclass.snapshot.storage.k8s.io/<volumesnapshotclass-name> created` message is emitted.