
{{site.data.keyword.attribute-definition-list}}

# Upgrading

Use this information to upgrade the IBM® block storage CSI driver and host definer.

## IBM® block storage CSI driver upgrade

To check if your operator is running at the latest release level, from the OpenShift web console, browse to **Operators** > **Installed Operators**. Check the status of the IBM block storage CSI driver operator. Ensure that the **Upgrade Status** is _Up to date_. For more information about automatic upgrades, see https://olm.operatorframework.io/docs/concepts/crds/subscription/.{: tip}

### Automatic upgrades

If the IBM block storage CSI driver operator install plan has the **Approval Strategy** set to **Automatic**, the Kubernetes and RedHat Openshift operator lifetime management will automatically upgrade the IBM block storage CSI driver when a new version is released.

### Manual upgrades

- To manually upgrade the IBM block storage CSI driver with the Red Hat OpenShift web console, see [Manual upgrade with OpenShift](upgrade_manual_openshift.md).
- To manually upgrade the IBM block storage CSI driver from a previous version with GitHub, perform step 1 of the [installation procedure](install_driver_github.md) with the latest version.

## IBM® block storage CSI host definer upgrade

When using host definer and upgrading from release prior to 1.13.0 to release 1.13.0 or up, please see the following.{: attention}

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

If the host definer was installed with GitHub, the host definer must be updated manually.{: important}

To enable the optional host definer feature when upgrading from IBM® block storage CSI driver 1.9.0 or earlier, the host definer must be manually installed.{: attention}

- If the host definer was installed from the Red Hat OpenShift web console or OperatorHub.io, the host definer automatically updates along with the driver version.
- For manual upgrade of the host definer with GitHub and OperatorHub.io, simply install the latest version, as described in [Installing the host definer](install_hostdefiner.md).
- For manual upgrade with the Red Hat OpenShift webconsole, see [Manual upgrade with OpenShift](upgrade_manual_openshift.md).
