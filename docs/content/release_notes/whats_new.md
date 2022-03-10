# What's new in 1.9.0

IBM® block storage CSI driver 1.9.0 introduces the enhancements that are detailed in the following section.

**General availability date:** 18 March 2022

## Additional high availability (HA) feature

Stretched volumes and stretched snapshots (FlashCopy) are now supported on SAN Volume Controller storage systems. Stretched storage topology enables disaster recovery and high availability between nodes in I/O groups at different locations. For more information, see **Product overview** > **Technical overview** > **Systems** > **Stretched systems** within the [SAN Volume Controller documentation](https://www.ibm.com/docs/en/sanvolumecontroller).

## New Call Home support

Call Home is now supported on Spectrum Virtualize family storage systems. For more information about Call Home on your storage system, see **Product overview** > **Technical overview** > **Call Home** within your product documentation on [IBM Documentation](https://www.ibm.com/docs).

## New Red Hat® Enterprise Linux® (RHEL) 8.x support

IBM® block storage CSI driver 1.9.0 now supports RHEL 8.x systems for x86 architectures.

## Additional supported orchestration platforms for deployment

This version adds support for orchestration platforms Kubernetes 1.23 and Red Hat® OpenShift 4.10, suitable for deployment of the CSI (Container Storage Interface) driver.