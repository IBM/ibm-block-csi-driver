{{site.data.keyword.attribute-definition-list}}

# What's new in 1.14.0

IBM® block storage CSI driver 1.14.0 adds support for:

- IBM Storage Virtualize® partitions PBHA for both FC and iSCSI hosts
- Support of NVMe over FC hosts
- Extended support to RedHat OpenShift® 4.21
- Extended support to Kubernetes 1.35
- More info in callhome

**General availability date:** April 2026

## Miscellaneous resolved issues

For information about the resolved issues in version 1.14.0, see [1.14.0](changelog_1.14.0.md).

If you have any configuration changes that you want saved over upgrade, notice the following.{: attention}

For configuration persistancy over upgrade, a config map procedure was added:
Prior to upgrading to IBM® block storage CSI driver 1.13.0 (or above) from releases before 1.13.0, please create a configmap with your existing changes. Please refer to [Configuring the host definer](../configuration/configuring_hostdefiner.md)
