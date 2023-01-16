# What's new in 1.10.0

IBM® block storage CSI driver 1.10.0 introduces the enhancements that are detailed in the following section.

**General availability date:** 26 July 2022

## Alpha support for the new Snapshot function that was introduced in IBM Spectrum Virtualize 8.5.1 release

This version adds Alpha support for the new Snapshot function that was introduced in IBM Spectrum Virtualize 8.5.1 release. The main use case of snapshot is corruption protection. It protects the user data from deliberate or accidental data corruption from the host's systems. For more information about the Snapshot function, see **Product overview** > **Technical overview** > **Volume groups** > **Snapshot function** within your Spectrum Virtualize product documentation on [IBM Documentation](https://www.ibm.com/docs).

**Important:** Be sure to read all of the limitations before using Snapshot function with the CSI driver.

**Note:** The IBM® FlashCopy and Snapshot function are both referred to as the more generic volume snapshots and cloning within this documentation set. Not all supported products use the FlashCopy and Snapshot function terminology. Spectrum Virtualize storage systems introduced the new Snapshot function as of Spectrum Virtualize 8.5.1 release. Notes clarifying which function is being referred to within this document are made, as necessary.

## New dynamic host definition

IBM® block storage CSI driver 1.10.0 enables users to not need to statically define hosts on the storage in advance, eliminating the need for manual static host definitions. The host definer handles changes in the orchestrator cluster that relate to the host definition and applies them to the relevant storage systems.

## Now enables volume group configuration

The CSI driver now enables volume group configuration when creating a new volume for Spectrum Virtualize family systems.

For more information about volume groups, see **Product overview** > **Technical overview** > **Volume groups** within your product documentation on [IBM Documentation](https://www.ibm.com/docs).

## New metrics support

IBM® block storage CSI driver 1.10.0 introduces new kubelet mounted volume metrics support for volumes created with the CSI driver.

The following metrics are currently supported:
- kubelet_volume_stats_available_bytes
- kubelet_volume_stats_capacity_bytes
- kubelet_volume_stats_inodes
- kubelet_volume_stats_inodes_free
- kubelet_volume_stats_inodes_used
- kubelet_volume_stats_used_bytes

For more information about the supported metrics, see `VolumeUsage` within the [Container Storage Interface (CSI) spec documentation for `NodeGetVolumeStats`](https://github.com/container-storage-interface/spec/blob/v1.5.0/spec.md#nodegetvolumestats).

For more information about using metrics in Kubernetes, see [Metrics in Kubernetes](https://kubernetes.io/docs/concepts/cluster-administration/system-metrics/#metrics-in-kubernetes) in the Kubernetes documentation.

## Additional supported orchestration platforms for deployment

This version adds support for orchestration platforms Kubernetes 1.24 and Red Hat® OpenShift® 4.11, suitable for deployment of the CSI (Container Storage Interface) driver.