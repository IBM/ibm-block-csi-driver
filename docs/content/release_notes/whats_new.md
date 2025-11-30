# What's new in 1.13.1

IBM® block storage CSI driver 1.13.1 adds support for:

- IBM Storage Virtualize® partitions, including Policy Based High Availability (PBHA) on partitions, over host FC only.
- IBM Storage Virtualize® - option to define secret-specific port set

**General availability date:** December 2025

## Miscellaneous resolved issues

For information about the resolved issues in version 1.13.1, see [1.13.1](changelog_1.13.1.md).

If you have any configuration changes that you want saved over upgrade, notice the following.{: attention}

For configuration persistancy over upgrade, a config map procedure was added:
Prior to upgrading to IBM® block storage CSI driver 1.13.0 (or above) from releases before 1.13.0, please create a configmap with your existing changes, for example:
`kubectl create configmap ibm-csi-hostdefiner-config --from-literal=portSet=portset64`
If you have more than one configuration change, you can add more `--from-literal=key=value` at the end of the above line. This configuration will be saved over upgrade.

More info regarding this configmap in [Configuring the host definer](content/configuration/configuring_hostdefiner.md)
