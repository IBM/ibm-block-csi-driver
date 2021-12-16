# What's new in 1.8.0

IBM® block storage CSI driver 1.8.0 introduces the enhancements that are detailed in the following section.

**General availability date:** 17 December 2021

## New HyperSwap support for IBM Spectrum Virtualize family storage systems

IBM® block storage CSI driver 1.8.0 now supports HyperSwap implementation, when using IBM Spectrum Virtualize family storage systems.

## New NVMe® over Fibre Channel protocol for IBM Spectrum Virtualize family storage systems

This version adds NVMe®/FC support for supported IBM Spectrum Virtualize family storage systems using Red Hat® Enterprise Linux® (RHEL) operating systems. 

## Increased StorageClass `SpaceEfficiency` parameter capabilities

Version 1.8.0 increases the `SpaceEfficiency` deduplication parameter options for IBM Spectrum Virtualize family storage systems. For more information, see [Creating a StorageClass](../configuration/creating_volumestorageclass.md).

## Added custom resource configurability for the CSI driver health port

This version allows you to configure the health port (9808) for the CSI driver through the custom resource. Configure using the `healthPort` parameter.

## Additional orchestration support for OpenShift 4.7 and 4.9 for deployment

This version reintroduces Red Hat® OpenShift 4.7 and adds new support for orchestration platform Red Hat OpenShift 4.9, suitable for deployment of the CSI (Container Storage Interface) driver.