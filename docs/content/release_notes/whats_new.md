# What's new in 1.10.0

IBM® block storage CSI driver 1.10.0 introduces the enhancements that are detailed in the following section.

**General availability date:** 15 July 2022

## New dynamic host definition

IBM® block storage CSI driver 1.10.0 enables users to not need to statically define hosts on the storage in advance, eliminating the need for manual static host definitions. The host definer handles changes in the orchestrator cluster that relate to the host definition and applies them to the relevant storage systems.

## Alpha support for the new snapshot function that was introduced in IBM Spectrum Virtualize 8.5.1 release

This version adds Alpha support for the new snapshot function that was introduced in IBM Spectrum Virtualize 8.5.1 release. The main use case of snapshot is corruption protection. It protects the user data from deliberate or accidental data corruption from the host's systems. For more information about the snapshot function, see **Product overview** > **Technical overview** > **Volume groups** > **Snapshot function** within your Spectrum Virtualize product documentation on [IBM Documentation](https://www.ibm.com/docs).

**Note:** The IBM® FlashCopy and Snapshot function are both referred to as the more generic volume snapshots and cloning within this documentation set. Not all supported products use the FlashCopy and Snapshot function terminology. Spectrum Virtualize storage systems introduced the new Snapshot function as of Spectrum Virtualize 8.5.1 release. Notes clarifying which function is being referred to within this document are made, as necessary.

## Additional supported orchestration platforms for deployment

This version adds support for orchestration platforms Kubernetes 1.24 and Red Hat® OpenShift 4.11, suitable for deployment of the CSI (Container Storage Interface) driver.