# CSI driver configuration

Use this information to configure the IBM® block storage CSI driver after installation.

Once the driver is installed and running (see [Installing the operator and driver](../installation/csi_ug_install_operator.md)), in order to use the driver and run stateful applications using IBM block storage systems, the relevant yaml files must be created.

Multiple yaml files per type can be created (with different configurations), according to your storage needs.

-   [Creating a Secret](csi_ug_config_create_secret.md)
-   [Creating a StorageClass](csi_ug_config_create_storageclasses.md)
-   [Creating a PersistentVolumeClaim (PVC)](csi_ug_config_create_pvc.md)
-   [Creating a StatefulSet](csi_ug_config_create_statefulset.md)
-   [Creating a VolumeSnapshotClass](csi_ug_config_create_vol_snapshotclass.md)
-   [Creating a VolumeSnapshot](csi_ug_config_create_snapshots.md)
-   [Expanding a PersistentVolumeClaim (PVC)](csi_ug_config_expand_pvc.md)
-   [Advanced configuration](csi_ug_config_advanced.md)



