
{{site.data.keyword.attribute-definition-list}}

# What's new in 1.13.0

IBM® block storage CSI driver 1.13.0 adds support for:

- IBM Storage Virtualize® partitions, including Policy Based High Availability (PBHA) on partitions, over host FC only.
- IBM Storage Virtualize® - option to define secret-specific port set

**General availability date:** December 2025

If you are using hostdefiner and are upgrading from versions before 1.13.0 note the following.{: attention}

Before upgrading IBM® block storage CSI driver from 1.12.x to 1.13.x hostdefiner must be removed

#### Uninstalling using UI
1. From the web console, select Installed Operators->IBM block storage CSI driver operator.
2. Navigate to IBM block storage Host Definer
3. Click on the more menu for the host-definer driver and select Delete HostDefiner.

#### Uninstalling using github
1. Download the custom resource manifest from Github that matches the current installed version (replace the x with actual version).
```
	 curl https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/v1.12.x/config/samples/csi_v1_hostdefiner_cr.yaml > csi_v1_hostdefiner_cr.yaml
```
2. Run the following command
```
 	kubectl delete -f csi_v1_hostdefiner_cr.yaml
```

Once host definer pod is removed, continue with the CSI upgrade to 1.13.x.
Verify configmap for the host definer is in place if needed [Configuring the host definer](../configuration/configuring_hostdefiner.md)

After upgrade is finished, and configmap is applied (if needed), reinstall host definer using one of the documented methods [Installing the host definer](../installation/install_hostdefiner.md)

## Miscellaneous resolved issues

For information about the resolved issues in version 1.13.0, see [1.13.0](changelog_1.13.0.md).

If you have any configuration changes that you want saved over upgrade, notice the following.{: attention}

For configuration persistancy over upgrade, a config map procedure was added:
Prior to upgrading to IBM® block storage CSI driver 1.13.0 (or above) from releases before 1.13.0, please create a configmap with your existing changes. Please refer to [Configuring the host definer](../configuration/configuring_hostdefiner.md)
