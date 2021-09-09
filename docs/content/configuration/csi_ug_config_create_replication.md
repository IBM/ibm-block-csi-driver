# Creating a VolumeReplication

Create a VolumeReplication YAML file to replicate a specific PersistentVolumeClaim (PVC).

VolumeReplicationClass needs to be present before a VolumeSnapshot can be created. For more information, see [Creating a VolumeReplicationClass](csi_ug_config_create_vol_replicationclass.md).

**Note:** Replication is also known as mirroring in many storage systems.

When replicating a volume, be sure to follow all of the replication configurations, found in [Compatibility and requirements](../installation/csi_ug_requirements.md) before snapshot creation.

1.  Replicate a specific  PersistentVolumeClaim (PVC) using the `demo-volumereplication.yaml`.

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
        name: demo-pvc-file-system # should be in same namespace as VolumeReplication
    ```

2.  After the YAML file is created, apply it by using the `kubectl apply -f` command.

    ```
    kubectl apply -f <filename>.yaml
    ```

3.  Verify that the VolumeReplication file was created.

    Run the `kubectl describe volumereplication` command.

    See the **Status** section of the output for the following:

    -   **Bound Volume Snapshot Content Name:** Indicates the volume is bound to the specified VolumeSnapshotContent.
    -   **Creation Time:** Indicates when the snapshot was created.
    -   **Ready to Use:** Indicates the volume snapshot is ready to use.
    -   **Restore Size:** Indicates the minimum volume size required when restoring (provisioning) a volume from this snapshot.


