# Configuring for policy-based replication

Use this information for specific configuring information when using policy-based replication with the IBM® block storage CSI driver.

Policy-based replication uses volume groups to automatically deploy and manage replication. Be sure to use dynamic volume groups when configuring the CSI driver for policy-based replication.

See the following sections for more information:
- [Limitations](../release_notes/limitations.md)
- [Compatibility and requirements](../installation/install_compatibility_requirements.md)
- [Using the CSI driver with policy-based replication](../using/using_policy_based_replication.md).
- [Creating a StorageClass with volume groups](creating_storageclass_vg.md)
- [Creating a PersistentVolumeClaim (PVC) with volume groups](creating_pvc_vg.md)
- [Creating a VolumeReplication with policy-based replication](creating_volumereplication_pbr.md)
- [Creating a VolumeGroupClass](creating_volumegroupclass.md)
- [Creating a VolumeGroup](creating_volumegroup.md)
