# Configuring for CSI Topology

Use this information for specific configuring information when using CSI Topology with the IBM® block storage CSI driver.

Using the CSI Topology feature, volume access can be limited to a subset of nodes, based on regions and availability zones. Nodes can be located in various regions within an availability zone, or across the different availability zones. Using the CSI Topology feature can ease volume provisioning for workloads within a multi-zone architecture.

**Important:** Be sure that all of the topology requirements are met before starting. For more information, see [Compatibility and requirements](../installation/install_compatibility_requirements.md).

- [Creating a Secret with topology awareness](creating_secret_topology_aware.md)
- [Creating a StorageClass with topology awareness](creating_storageclass_topology_aware.md)
- [Creating a VolumeSnapshotClass with topology awareness](creating_volumesnapshotclass_topology_aware.md)