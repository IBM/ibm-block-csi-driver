# What's new in 1.10.0

IBM® block storage CSI driver 1.10.0 introduces the enhancements that are detailed in the following section.

**General availability date:** 15 July 2022

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

For more information about using metrics in Kubenertes, see [Metrics in Kubernetes](https://kubernetes.io/docs/concepts/cluster-administration/system-metrics/#metrics-in-kubernetes) in the Kubernetes documentation.

## Additional supported orchestration platforms for deployment

This version adds support for orchestration platforms Kubernetes 1.24 and Red Hat® OpenShift 4.11, suitable for deployment of the CSI (Container Storage Interface) driver.