{{site.data.keyword.attribute-definition-list}}

# What's new in 1.14.0

IBM® block storage CSI driver 1.14.0 adds support for:


**General availability date:** TBD 2026

### virt_snap_func parameter configuration change

The `virt_snap_func` parameter is now **only** read from the StorageClass and is **no longer** supported in VolumeSnapshotClass configurations. When creating a snapshot, the driver automatically retrieves the `virt_snap_func` value from the source volume's StorageClass. Any value specified in VolumeSnapshotClass will be ignored.

For more information, see [Creating a StorageClass](../configuration/creating_volumestorageclass.md).

## Miscellaneous resolved issues

For information about the resolved issues in version 1.14.0, see [1.14.0](changelog_1.14.0.md).

If you have any configuration changes that you want saved over upgrade, notice the following.{: attention}

For configuration persistancy over upgrade, a config map procedure was added:
Prior to upgrading to IBM® block storage CSI driver 1.13.0 (or above) from releases before 1.13.0, please create a configmap with your existing changes. Please refer to [Configuring the host definer](../configuration/configuring_hostdefiner.md)
