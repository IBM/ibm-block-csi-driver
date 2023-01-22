# What's new in 1.11.0

IBM® block storage CSI driver 1.11.0 introduces the enhancements that are detailed in the following section.

**General availability date:** 23 January 2023

## New dynamic volume group support

The new volume group support allows dynamic management of the content of the groups. This feature is used by policy-based replication, introduced IBM Spectrum Virtualize 8.5.2 release.
As opposed to the volume group feature present within the CSI driver which stays static on the StorageClass, the new volume group feature is dynamic. 

With this new volume group support, the PersistentVolumeClaim (PVC) label can be updated at any time to change the PVC from belonging from one volume group to another, once the volume groups are defined on the storage.

The advantage of using volume groups is that actions, like replication, can be done simultaneously across all volumes in a volume group.

For more information about volume groups and policy-based replication, see the following sections within your Spectrum Virtualize product documentation [IBM Documentation](https://www.ibm.com/docs).

- **Product overview** > **Technical overview** > **Volume groups**
- **What's new** > **Getting started with policy-based replication**

## New support for policy-based replication

This version adds support for policy-based replication that was introduced in IBM Spectrum Virtualize 8.5.2 release. Policy-based replication provides a simplified configuration and management of asynchronous replication between two system. To see if your specific product is supported and for more information, see **What's new** > **Getting started with policy-based replication** within your Spectrum Virtualize product documentation on [IBM Documentation](https://www.ibm.com/docs).

**Important:** Policy-based replication must be used together with dynamic volume groups. For more information, see [Using the CSI driver with policy-based replication](../using/using_policy_based_replication.md).

## Added dynamic host definition enhancements

The IBM® block storage CSI driver 1.11.0 host definition feature now supports the following:

- **CSI Topology feature**

    Dynamic host definition now works together with CSI Topology feature. For more information about CSI Topology, see [Configuring for CSI Topology](../configuration/confiugring_toplogy.md).

- **Dynamically configuring host ports**

     Host ports are now automatically updated and changes in host port hierarchy are now identified and automatically updated. For more information, see [Configuring the host definer](../configuration/configuring_hostdefiner.md).

- **I/O group function**

    By default the host definer now creates all definitions across all possible I/O groups. Additionally, an optional label available in order to specify which I/O group(s) should be used on a specific node. For more information, see [Adding optional labels for dynamic host definition](../using/using_hostdefinition_labels.md).

    For more about the I/O group function, see **Product overview** > **Technical overview** > **I/O group** within your Spectrum Virtualize product documentation on [IBM Documentation](https://www.ibm.com/docs).

- **Overriding node host connectivity**

    This version introduces a new label tag, allowing connectivity type definition of a specific node, regardless of connectivity hierarchy. For more information, see [Adding optional labels for dynamic host definition](../using/using_hostdefinition_labels.md).
    
In addition, only valid ports are now defined. For example, if a host has a total of four Fibre Channel ports and only two of them are zoned to the storage system, only the two zoned ports are created on the host.

## Additional supported orchestration platforms for deployment

This version adds support for orchestration platforms Kubernetes 1.25 and Red Hat® OpenShift® 4.12, suitable for deployment of the CSI (Container Storage Interface) driver.

**Note:** IBM® block storage CSI driver 1.11.0 does not support FlashSystem A9000 or A9000R storage systems.

## Miscellaneous resolved issues

For information about the resolved issue in version 1.11.0, see [1.11.0 (January 2023)](changelog_1.11.0.md).