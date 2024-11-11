# Creating a VolumeReplication with policy-based replication

Create a VolumeReplication YAML file to replicate a specific PersistentVolumeClaim (PVC).

VolumeReplicationClass needs to be present before a VolumeReplication can be created. For more information, see [Creating a VolumeReplicationClass](creating_volumereplicationclass.md).

**Note:** For information and parameter definitions that are not related to topology awareness, be sure to see the information provided in [Creating a VolumeReplication](creating_volumereplication.md), in addition to the current section.

**Note:** Use the `VolumeGroup` value for `spec.dataSource.kind`.

1.  Replicate a specific PersistentVolumeClaim (PVC) using the `demo-volumereplication.yaml`.

    For more information about PVC configuration, see [Creating a PersistentVolumeClaim (PVC)](creating_pvc.md).

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
        kind: VolumeGroup
        name: demo-volumegroup  # Ensure that this is in the same namespace as VolumeGroup.
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

    **Note:** For information about changing the replication state, see the [Usage](https://github.com/csiblock/volume-replication-operator/tree/v0.1.0#usage) section of the Volume Replication Operator for csi-addons.
