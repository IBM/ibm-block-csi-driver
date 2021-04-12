# CSI driver configuration

Use this information to configure the IBM® block storage CSI driver after installation.

Once the driver is installed and running \(see [Installing the operator and driver](csi_ug_install_operator.md)\), in order to use the driver and run stateful applications using IBM block storage systems, the relevant yaml files must be created.

Multiple yaml files per type can be created \(with different configurations\), according to your storage needs.

-   [Creating a Secret](csi_ug_config_create_secret.md)
-   [Creating a StorageClass](csi_ug_config_create_storageclasses.md)
-   [Creating a PersistentVolumeClaim \(PVC\)](csi_ug_config_create_pvc.md)
-   [Creating a StatefulSet](csi_ug_config_create_statefulset.md)
-   [Creating a VolumeSnapshotClass](csi_ug_config_enable_snapshot.md)
-   [Creating a VolumeSnapshot](csi_ug_config_create_snapshots.md)
-   [Expanding a PersistentVolumeClaim \(PVC\)](csi_ug_config_expandvol.md)
-   [Advanced configuration](csi_ug_config_advanced.md)

-   **[Creating a Secret](csi_ug_config_create_secret.md)**  
Create an array secret YAML file in order to define the storage credentials \(username and password\) and address.
-   **[Creating a StorageClass](csi_ug_config_create_storageclasses.md)**  
Create a storage class yaml file in order to define the storage system pool name, secret reference, SpaceEfficiency, and fstype.
-   **[Creating a PersistentVolumeClaim \(PVC\)](csi_ug_config_create_pvc.md)**  
Create a PersistentVolumeClaim \(PVC\) yaml file for a persistent volume \(PV\).
-   **[Creating a StatefulSet](csi_ug_config_create_statefulset.md)**  
Create a StatefulSet yaml file to manage stateful applications.
-   **[Creating a VolumeSnapshotClass](csi_ug_config_enable_snapshot.md)**  
Create a VolumeSnapshotClass YAML file to enable creation and deletion of volume snapshots.
-   **[Creating a VolumeSnapshot](csi_ug_config_create_snapshots.md)**  
Create a VolumeSnapshot yaml file for a specific PersistentVolumeClaim \(PVC\).
-   **[Expanding a PersistentVolumeClaim \(PVC\)](csi_ug_config_expandvol.md)**  
Use this information to expand existing volumes.
-   **[Advanced configuration](csi_ug_config_advanced.md)**  
Use advanced configuration tasks to further customize the configuration of the IBM block storage CSI driver.

