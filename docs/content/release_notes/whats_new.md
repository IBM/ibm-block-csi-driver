# What's new in 1.9.0

IBM® block storage CSI driver 1.9.0 introduces the enhancements that are detailed in the following section.

**General availability date:** 18 March 2022

## Additional high availability (HA) feature

Stretched volumes and stretched snapshots (FlashCopy) are now supported on SAN Volume Controller storage systems. Stretched storage topology enables disaster recovery and high availability between nodes in I/O groups at different locations. For more information, see **Product overview** > **Technical overview** > **Systems** > **Stretched systems** within the [SAN Volume Controller documentation](https://www.ibm.com/docs/en/sanvolumecontroller).

## New metrics support

IBM® block storage CSI driver 1.9.0 introduces new kubelet mounted volume metrics support for volumes created with the CSI driver.

The following metrics are currently supported:
- kubelet_volume_stats_available_bytes
- kubelet_volume_stats_capacity_bytes
- kubelet_volume_stats_inodes
- kubelet_volume_stats_inodes_free
- kubelet_volume_stats_inodes_used
- kubelet_volume_stats_used_bytes

For more information about the supported metrics, see `VolumeUsage` within the [Container Storage Interface (CSI) spec documentation for `NodeGetVolumeStats`](https://github.com/container-storage-interface/spec/blob/v1.5.0/spec.md#nodegetvolumestats).

For more information about using metrics in Kubenertes, see [Metrics in Kubernetes](https://kubernetes.io/docs/concepts/cluster-administration/system-metrics/#metrics-in-kubernetes) in the Kubernetes documentation.

## Additional orchestration support for OpenShift 4.7 and 4.9 for deployment

This version reintroduces Red Hat® OpenShift 4.7 and adds new support for orchestration platform Red Hat OpenShift 4.9, suitable for deployment of the CSI (Container Storage Interface) driver.