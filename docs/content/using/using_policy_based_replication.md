
{{site.data.keyword.attribute-definition-list}}

# Using the CSI driver with policy-based replication

Policy-based replication was introduced in IBM Storage Virtualize 8.5.2 release. Policy-based replication provides simplified configuration and management of asynchronous replication between two systems.

Policy-based replication uses volume groups to automatically deploy and manage replication. This feature significantly simplifies configuring, managing, and monitoring replication between two systems. In order to support this feature, the CSI driver creates fictitious volume groups for the volume being replicated, as replication is handled on a per volume basis within CSI. It is then the volume group that gets replicated. Once the volume groups are replicated all volume groups can be seen within the IBM Storage Virtualize user interface. All replicated volumes are identified by the original volume group name with the `_vg` suffix.

When deleting volumes that are replicated, both the replicated volume and volume group are automatically deleted, as well as the original fictitious volume group that was created in order to use the policy-based replication. Deleting replicated volumes / volume groups does not delete the original volume itself.

The CSI driver identifies that policy-based replication is being used based on the use of the `replication_policy` parameter within the VolumeReplicationClass YAML file.

`replication_policy` cannot be used together with the `system_id` parameter.{: restriction}

Before replicating a volume with policy-based replication, verify that the proper replication policies are in place on your storage system.

- For more information, see [Compatibility and requirements](../installation/install_compatibility_requirements.md)
- For more configuration information, see [Creating a VolumeReplicationClass](../configuration/creating_volumereplicationclass.md) and [Creating a VolumeReplication](../configuration/creating_volumereplication.md)
- For information on importing existing volume groups, see [Importing an existing volume group](../configuration/importing_existing_volume_group.md)

For full information about this IBM Storage Virtualize feature, see **What's new** > **Getting started with policy-based replication** within your IBM Storage Virtualize product documentation on [IBM Documentation](https://www.ibm.com/docs).{: tip}

