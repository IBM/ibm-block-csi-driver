# What's new in 1.11.0

IBM® block storage CSI driver 1.11.0 introduces the enhancements that are detailed in the following section.

**General availability date:** 15 January 2023

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

## Additional supported orchestration platforms for deployment

This version adds support for orchestration platforms Kubernetes 1.25 and Red Hat® OpenShift® 4.12, suitable for deployment of the CSI (Container Storage Interface) driver.