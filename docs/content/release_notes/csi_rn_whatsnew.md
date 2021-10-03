# What's new in 1.7.0

IBM® block storage CSI driver 1.7.0 introduces the enhancements that are detailed in the following section.

**General availability date:** 30 September 2021

## Now supports CSI Topology

IBM® block storage CSI driver 1.7.0 is now topology aware. Using this feature, volume access can be limited to a subset of nodes, based on regions and availability zones. Nodes can be located in various regions within an availability zone, or across the different availability zones. Using CSI Topology feature can ease volume provisioning for workloads within a multi-zone architecture.

For more information, see [CSI Topology Feature](https://kubernetes-csi.github.io/docs/topology.html).

## New volume replication support for IBM Spectrum Virtualize Family storage systems

When using IBM Spectrum Virtualize Family storage systems, the CSI driver now supports volume replication (remote copy).

## Additional support for Kubernetes 1.22 orchestration platforms for deployment

This version adds support for orchestration platform Kubernetes 1.22, suitable for deployment of the CSI (Container Storage Interface) driver.



