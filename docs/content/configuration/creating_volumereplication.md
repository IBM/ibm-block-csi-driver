# Creating a VolumeReplication

Create a VolumeReplication YAML file to replicate a specific PersistentVolumeClaim (PVC).

VolumeReplicationClass needs to be present before a VolumeReplication can be created. For more information, see [Creating a VolumeReplicationClass](creating_volumereplicationclass.md).

**Note:** Remote copy function is referred to as the more generic volume replication within this documentation set. Not all supported products use the remote-copy function terminology.

When replicating a volume, be sure to follow all of the replication configurations, found in [Compatibility and requirements](../installation/install_compatibility_requirements.md) before volume replication.

1.  Replicate a specific PersistentVolumeClaim (PVC) using the `demo-volumereplication.yaml`.

    For more information about PVC configuration, see [Creating a PersistentVolumeClaim (PVC)](creating_pvc.md).

    **Note:** Use the `spec.csi.volumeHandle` of the relevant target PersistentVolume (PV) for the `replicationHandle` value.

    ```
    apiVersion: replication.storage.openshift.io/v1alpha1
    kind: VolumeReplication
    metadata:
      name: demo-volumereplication
      namespace: default
    spec:
      volumeReplicationClass: demo-volumereplicationclass
      replicationState: primary
      replicationHandle: demo-volumehandle
      dataSource:
        kind: PersistentVolumeClaim
        name: demo-pvc-file-system  # Ensure that this is in the same namespace as VolumeReplication.
    ```

2.  After the YAML file is created, apply it by using the `kubectl apply -f` command.

    ```
    kubectl apply -f <filename>.yaml
    ```

    The `volumereplication.replication.storage.openshift.io/<volumereplication-name> created` message is emitted.

3.  Verify that the volume was replicated.

    Run the `kubectl describe volumereplication` command.

    See the `status.state` section to see which of the following states the replication is in:

    -   **Primary** Indicates that the source volume is the primary volume.
    -   **Secondary** Indicates that the source volume is the secondary volume.
    -   **Unknown** Indicates that the driver does not recognize the replication state.

    **Note:** For information about changing the replication state, see the [Usage](https://github.com/csi-addons/volume-replication-operator/tree/v0.2.0#usage) section of the Volume Replication Operator for csi-addons.