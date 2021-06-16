# Upgrading the CSI driver

Use this information to upgrade the IBM® block storage CSI driver.

- The OpenShift web console and OperatorHub.io both automatically upgrade the the CSI (Container Storage Interface) driver when a new version is released.
    - With OpenShift web console, the **Approval Strategy** must be set to **Automatic**.

        To check if your operator is running at the latest release level, from the OpenShift web console, browse to **Operators** > **Installed Operators**. Check the status of the Operator for IBM block storage CSI driver. Ensure that the **Upgrade Status** is _Up to date_.
    
  **Note:** For more information about automatic upgrades, see https://olm.operatorframework.io/docs/concepts/crds/subscription/.

- To manually upgrade the CSI driver with the OpenShift web console, see [Manual upgrade with OpenShift](csi_ug_upgrade_ocp_manual.md).

- To manually upgrade the CSI (Container Storage Interface) driver from a previous version with GitHub, perform step 1 of the [installation procedure](csi_ug_install_operator_github.md) for the latest version.



