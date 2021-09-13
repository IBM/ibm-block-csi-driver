# Creating a VolumeReplication

Create a VolumeReplication YAML file to replicate a specific PersistentVolumeClaim (PVC).

VolumeReplicationClass needs to be present before a VolumeReplication can be created. For more information, see [Creating a VolumeReplicationClass](csi_ug_config_create_vol_replicationclass.md).

**Note:** Replication is referred to as the more generic volume replication within this documentation set. Spectrum Virtualize products refer to this feature as the remote copy function.

When replicating a volume, be sure to follow all of the replication configurations, found in [Compatibility and requirements](../installation/csi_ug_requirements.md) before volume replication.

1.  Replicate a specific PersistentVolumeClaim (PVC) using the `demo-volumereplication.yaml`.

    For more information about PVC configuration, see [Creating a PersistentVolumeClaim (PVC)](csi_ug_config_create_pvc.md).

    ```
    apiVersion: replication.storage.openshift.io/v1alpha1
    kind: VolumeReplication
    metadata:
      name: demo-volumereplication
      namespace: default
    spec:
      volumeReplicationClass: demo-volumereplicationclass
      replicationState: primary
      dataSource:
        kind: PersistentVolumeClaim
        name: demo-pvc-file-system  # Ensure that this is in the same namespace as VolumeReplication.
    ```

2.  After the YAML file is created, apply it by using the `kubectl apply -f` command.

    ```
    kubectl apply -f <filename>.yaml
    ```

3.  Verify that the volume was replicated.

    Run the `kubectl describe volumereplication` command.

    See the `status.state` section to see which of the following states the replication is in:

    -   **Primary** Indicates that the source volume is the primary volume.
    -   **Secondary** Indicates that the source volume is the secondary volume.
    -   **Unknown** Indicates that the driver does not recognize the replication state.

    **Note:** For information about changing the replication state, see the [Usage](https://github.com/csi-addons/volume-replication-operator/tree/v0.1.0#usage) section of the Volume Replication Operator for csi-addons.