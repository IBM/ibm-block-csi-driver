# Using the CSI driver with policy-based replication

Policy-based replication was introduced in IBM Spectrum Virtualize 8.5.2 release. Policy-based replication provides simplified configuration and management of asynchronous replication between two systems.

Policy-based replication uses volume groups to automatically deploy and manage replication. This feature significantly simplifies configuring, managing, and monitoring replication between two systems. In order to support this feature, the CSI driver creates fictitious volume groups for the volume being replicated, as replication is handled on a per volume basis within CSI. It is then the volume group that gets replicated. Once the volume groups are replicated all volume groups can be seen within the Spectrum Virtualize user interface. All replicated volumes are identified by the original volume group name with the `_vg` suffix.

When deleting volumes that are replicated, both the replicated volume and volume group are automatically deleted, as well as the original fictitious volume group that was created in order to use the policy-based replication. Deleting replicated volumes / volume groups does not delete the original volume itself.

The CSI driver identifies that policy-based replication is being used based on the use of the `replication_policy` parameter within the VolumeReplicationClass YAML file.

**Important:** `replication_policy` cannot be used together with the `system_id` parameter.

Before replicating a volume with policy-based replication, verify that the proper replication policies are in place on your storage system.

- For more information, see [Compatibility and requirements](../installation/install_compatibility_requirements.md)
- For more configuration information, see [Creating a VolumeReplicationClass](../configuration/creating_volumereplicationclass.md) and [Creating a VolumeReplication](../configuration/creating_volumereplication.md)
- For information on importing existing volume groups, see [Importing an existing volume group](../configuration/importing_existing_volume_group.md)

**Note:** For full information about this Spectrum Virtualize feature, see **What's new** > **Getting started with policy-based replication** within your Spectrum Virtualize product documentation on [IBM Documentation](https://www.ibm.com/docs).

## Promoting a volume group
To promote a replicated volume group within the CSI driver, the VolumeReplication state must be promoted.

Promote the VolumeReplication state, by changing the `spec.replicationState` from `Secondary` to `Primary`. For more information, see [Creating a VolumeReplication](../configuration/creating_volumereplication.md).

### Promoting a replicated volume group
Use the following procedure to promote a replicated volume group:

1. Import the existing volume group. See [Importing an existing volume group](../configuration/importing_existing_volume_group.md).
<br>**Attention:** Be sure to import any existing volumes before importing the volume group.
2. Create and apply a new VolumeReplication YAML file for the volume group, with the  `spec.replicationState` parameter being `Primary`. See [Creating a VolumeReplication](../configuration/creating_volumereplication.md).

## Removing a PVC from a volume group with a replication policy

When both Primary and Secondary volume groups are represented on a cluster, their associated PVCs must be removed in this specific order.

**Important:** Be sure to follow these steps in the correct order to prevent a PVC from locking.

For each PVC Primary and Secondary pair to be removed from its volume group:
   1. Remove the Primary PVC volume group labels.
   2. Remove the Secondary PVC volume group labels.<br>
    **Note:** After the Primary PVC volume group labels have been removed, the Secondary PVC associated volume is automatically deleted from the storage system.

## Deleting a VolumeGroup with a replication policy

When both Primary and Secondary volume groups are represented on a cluster, delete them in this specific order.

For each VolumeGroup Primary and Secondary pair to be deleted:
   1. Delete the Primary VolumeGroup.
   2. Delete the Secondary VolumeGroup.<br>
    **Note:** After the Primary VolumeGroup has been deleted, the Secondary volume group is automatically deleted from the storage system.




