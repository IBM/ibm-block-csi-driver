# Upgrading

Use this information to upgrade the IBM® block storage CSI driver and host definer.

**Important:** To enable the optional host definer feature when upgrading from IBM® block storage CSI driver 1.9.0 or earlier, the host definer must be manually installed. If the host definer was installed with GitHub, the host definer must be updated manually. If the host definer was installed from the Red Hat OpenShift web console or OperatorHub.io, the host definer automatically updates along with the driver version. 
  - For manual installation of the host definer with GitHub and OperatorHub.io, see [Installing the host definer](install_hostdefiner.md).
  - For manual upgrade with the Red Hat OpenShift webconsole, see [Manual upgrade with OpenShift](upgrade_manual_openshift.md).

The Red Hat OpenShift web console and OperatorHub.io both automatically upgrade the CSI (Container Storage Interface) driver when a new version is released.
   - With OpenShift web console, the **Approval Strategy** must be set to **Automatic**.

      To check if your operator is running at the latest release level, from the OpenShift web console, browse to **Operators** > **Installed Operators**. Check the status of the IBM block storage CSI driver operator. Ensure that the **Upgrade Status** is _Up to date_.
    
      **Note:** For more information about automatic upgrades, see https://olm.operatorframework.io/docs/concepts/crds/subscription/.

To manually upgrade the CSI driver with the Red Hat OpenShift web console, see [Manual upgrade with OpenShift](upgrade_manual_openshift.md).

To manually upgrade the CSI (Container Storage Interface) driver from a previous version with GitHub, perform step 1 of the [installation procedure](install_driver_github.md) for the latest version.
