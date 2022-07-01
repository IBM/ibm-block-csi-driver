# What's new in 1.10.0

IBM® block storage CSI driver 1.10.0 introduces the enhancements that are detailed in the following section.

**General availability date:** 15 July 2022

## New dynamic host definition

IBM® block storage CSI driver 1.10.0 enables users to not need to statically define dynamic objects on the storage in advance, eliminating the need for manual static host definitions. The host definer custom resource handles changes in the Kubernetes clusters that relate to the host definition feature and applies them to the relevant clusters.

## Additional supported orchestration platforms for deployment

This version adds support for orchestration platforms Kubernetes 1.24 and Red Hat® OpenShift 4.11, suitable for deployment of the CSI (Container Storage Interface) driver.