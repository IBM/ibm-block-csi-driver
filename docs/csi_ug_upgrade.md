# Upgrading the CSI driver

Use this information to upgrade the IBM® block storage CSI driver.

When the **Approval Strategy** is set to **Automatic** within the OpenShift web console the CSI \(Container Storage Interface\) driver upgrades automatically when a new version is released. \(See [Installing the driver using the OpenShift web console](csi_ug_install_operator_openshift.md).\)

To check if your operator is running at the latest release level, from the OpenShift web console, browse to **Operators** \> **Installed Operators**. Check the status of the Operator for IBM block storage CSI driver. Ensure that the **Upgrade Status** is _Up to date_.

To manually upgrade by using the OpenShift web console, see [Upgrading the driver using the OpenShift web console](csi_ug_upgrade_openshift.md).

To manually upgrade the CSI \(Container Storage Interface\) driver from a previous version by using CLI commands, perform step 1 and step 4 of the [installation procedure](csi_ug_install_operator_github.md) for the latest version.

-   **[Upgrading the driver using the OpenShift web console](csi_ug_upgrade_openshift.md)**  
When using the Red Hat OpenShift Container Platform, the operator for IBM block storage CSI driver can be upgraded directly from OpenShift web console, through the OperatorHub.


