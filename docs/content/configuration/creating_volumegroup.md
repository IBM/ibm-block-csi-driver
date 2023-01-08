# Creating a VolumeGroup

Create a VolumeGroup YAML file to specify a volume group key, for creating PersistentVolumeClaim (PVC) groups.

VolumeGroupClass needs to be present before a VolumeGroup can be created. For more information, see [Creating a VolumeGroupClass](creating_volumegroupclass.md).

Before creating a volume group, be sure to follow all of the volume group configurations, found in [Compatibility and requirements](../installation/install_compatibility_requirements.md).

1.  Create a Volume Group using the `demo-volumegroup.yaml`.

    **Note:** Use the `key` value is used in the relevant target PersistentVolumeClaim (PVC).

    ```
    apiVersion: csi.ibm.com/v1
    kind: VolumeGroup
    metadata:
      name: demo-volumegroup
    spec:
      volumeGroupClassName: volumeGroupClass1
      source:
        selector:
          matchExpressions:
            - key: volumegroup
              operator: In
              values:
              - demo-volumegroup
    ```

2.  After the YAML file is created, apply it by using the `kubectl apply -f` command.

    ```
    kubectl apply -f <filename>.yaml
    ```

    The `volumegroup.csi.ibm.com/<volumegroup-name> created` message is emitted.

3.  Verify that the volume was replicated.

    Run the `kubectl describe volumegroup` command.

    See the `status.state` section to see which of the following states the replication is in:

    -   **Primary** Indicates that the source volume is the primary volume.
    -   **Secondary** Indicates that the source volume is the secondary volume.
    -   **Unknown** Indicates that the driver does not recognize the replication state.

