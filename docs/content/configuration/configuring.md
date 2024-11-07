# Configuring

Use this information to configure the IBM® block storage CSI driver after installation.

Once the driver is installed and running (see [Installing the operator and driver](../installation/install_operator_driver.md)), in order to use the driver and run stateful applications using IBM block storage systems, the relevant YAML files must be created.

Multiple YAML files per type can be created (with different configurations), according to your storage needs.

- [Make sure multipath is enabled](enable_multipath.md)
- [Creating a Secret](creating_secret.md)
- [Creating a StorageClass](creating_volumestorageclass.md)
- [Creating a PersistentVolumeClaim (PVC)](creating_pvc.md)
- [Creating a StatefulSet](creating_statefulset.md)
- [Creating a VolumeSnapshotClass](creating_volumesnapshotclass.md)
- [Creating a VolumeSnapshot](creating_volumesnapshot.md)
- [Creating a VolumeReplicationClass](creating_volumereplicationclass.md)
- [Creating a VolumeReplication](creating_volumereplication.md)
- [Expanding a PersistentVolumeClaim (PVC)](expanding_pvc.md)
- [Configuring for CSI Topology](configuring_topology.md)
- [Configuring the host definer](configuring_hostdefiner.md)
- [Advanced configuration](advanced_configuration.md)
- [Configuring an OpenShift VM](configuring_vm.md)
