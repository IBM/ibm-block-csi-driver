# Creating a VolumeGroup

Create a VolumeGroup YAML file to specify a volume group key, for creating PersistentVolumeClaim (PVC) groups.

VolumeGroupClass needs to be present before a VolumeGroup can be created. For more information, see [Creating a VolumeGroupClass](creating_volumegroupclass.md).

Before creating a volume group, be sure to follow all of the volume group configurations, found in [Compatibility and requirements](../installation/install_compatibility_requirements.md).

1.  Create a Volume Group by using the `demo-volumegroup.yaml`.

    **Note:**  Be sure to match the selector in the target volume group (`spec.source.selector`). Use the same volume group `key` and value in both the VolumeGroup and PVC. For more information, see [Creating a PersistentVolumeClaim (PVC)](creating_pvc.md).

    ```
    apiVersion: csi.ibm.com/v1
    kind: VolumeGroup
    metadata:
      name: demo-volumegroup
    spec:
      volumeGroupClassName: demo-volumegroupclass
      source:
        selector: 
          matchLabels:
            demo-volumegroup-key: demo-volumegroup-value
    ```

2.  After the YAML file is created, apply it by using the `kubectl apply -f` command.

    ```
    kubectl apply -f <filename>.yaml
    ```

    The `volumegroup.csi.ibm.com/<volumegroup-name> created` message is emitted.

3.  Verify that the volume was replicated.

    Run the `kubectl describe volumegroup <vg_name>` command.

    **Note:** This command to verify which PVCs are in the volume group.

    See the `status.state` section to see the state of the volume group and volumes within it.

    -   **Primary** Indicates that the source volume is the primary volume.
    -   **Secondary** Indicates that the source volume is the secondary volume.
    -   **Unknown** Indicates that the driver does not recognize the replication state.

