# Creating a VolumeSnapshot

Create a VolumeSnapshot YAML file for a specific PersistentVolumeClaim (PVC).

VolumeSnapshotClass needs to be present before a VolumeSnapshot can be created. For more information, see [Creating a VolumeSnapshotClass](creating_volumesnapshotclass.md).

**Note:** This section refers to both the IBM FlashCopy® function and Snapshot function in Spectrum Virtualize storage systems.

When creating volume snapshots, be sure to follow all of the snapshot configurations, found in [Compatibility and requirements](../installation/install_compatibility_requirements.md) before snapshot creation.

1.  Create a snapshot for a specific PersistentVolumeClaim (PVC) using the `demo-volumesnapshot.yaml`.

    For more information about PVC configuration, see [Creating a PersistentVolumeClaim (PVC)](creating_pvc.md).

    ```
    apiVersion: snapshot.storage.k8s.io/v1beta1
    kind: VolumeSnapshot
    metadata:
      name: demo-volumesnapshot
    spec:
      volumeSnapshotClassName: demo-volumesnapshotclass
      source:
        persistentVolumeClaimName: demo-pvc-file-system
    ```

2.  After the YAML file is created, apply it by using the `kubectl apply -f` command.

    ```
    kubectl apply -f <filename>.yaml
    ```

    The `volumesnapshot.snapshot.storage.k8s.io/<volumesnapshot-name> created` message is emitted.

3.  Verify that the volume snapshot was created.

    Run the `kubectl describe volumesnapshot <volumesnapshot-name>` command.

    See the **Status** section of the output for the following:

    -   **Bound Volume Snapshot Content Name:** Indicates the volume is bound to the specified VolumeSnapshotContent.
    -   **Creation Time:** Indicates when the snapshot was created.
    -   **Ready to Use:** Indicates the volume snapshot is ready to use.
    -   **Restore Size:** Indicates the minimum volume size required when restoring (provisioning) a volume from this snapshot.
