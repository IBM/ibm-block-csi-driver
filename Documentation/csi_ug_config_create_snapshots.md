# Creating a VolumeSnapshot

Create a VolumeSnapshot yaml file for a specific PersistentVolumeClaim (PVC\).

VolumeSnapshotClass needs to be present before a VolumeSnapshot can be created. For more information, see [Creating a VolumeSnapshotClass](csi_ug_config_enable_snapshot.md).

**Note:**

-   IBM® FlashCopy® function is referred to as the more generic volume snapshots and cloning within this documentation set. Not all supported products use the FlashCopy function terminology.
-   For volume snapshot support, the minimum orchestration platform version requirements are Red Hat® OpenShift® 4.4 and Kubernetes 1.17.

When creating volume snapshots, be sure to follow all of the snapshot configurations, found in [Compatibility and requirements](csi_ug_requirements.md) before snapshot creation.

1.  Create a snapshot for a specific PersistentVolumeClaim \(PVC\) using the demo-snapshot.yaml.

    For more information about PVC configuration, see [Creating a PersistentVolumeClaim \(PVC\)](csi_ug_config_create_pvc.md).

    ```screen
    apiVersion: snapshot.storage.k8s.io/v1beta1
    kind: VolumeSnapshot
    metadata:
      name: demo-snapshot
    spec:
      volumeSnapshotClassName: demo-snapshotclass
      source:
        persistentVolumeClaimName: demo-pvc-file-system
    ```

2.  After the YAML file is created, apply it by using the `kubectl apply -f` command.

    ```
    kubectl apply -f <filename>.yaml
    ```

3.  Verify that the VolumeSnapshot was created.

    Run the kubectl describe volumesnapshot command.

    See the **Status** section of the output for the following:

    -   **Bound Volume Snapshot Content Name:** Indicates the volume is bound to the specified VolumeSnapshotContent.
    -   **Creation Time:** Indicates when the snapshot was created.
    -   **Ready to Use:** Indicates the volume snapshot is ready to use.
    -   **Restore Size:** Indicates the minimum volume size required when restoring (provisioning) a volume from this snapshot.


