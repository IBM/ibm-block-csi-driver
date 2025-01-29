
{{site.data.keyword.attribute-definition-list}}

# Installing the driver with the OpenShift web console

When using the Red Hat® OpenShift® Container Platform, the operator for IBM® block storage CSI driver can be installed directly from OpenShift Container Platform web console, through the OperatorHub. Installing the CSI (Container Storage Interface) driver is part of the operator installation process.

Installation via the OpenShift Container Platform web console is only available for the x86 platform. For the IBM Z and IBM Power Systems platforms, please refer to [Installing the driver with GitHub](install_driver_github.md) or [Installing the driver with OperatorHub.io](install_driver_operatorhub.md) {: attention}

The Red Hat OpenShift Container Platform uses the following `SecurityContextConstraints` for the following `serviceAccounts`:

This data is for informational purposes only. {: note}

|serviceAccount|SecurityContextConstraint|
|--------------|-------------------------|
|ibm-block-csi-operator|restricted|
|ibm-block-csi-controller-sa|anyuid|
|ibm-block-csi-node-sa|privileged|

1. From the web console, create a new project (also referred to as namespace).

2. Find and install **IBM block storage CSI driver operator** within the new project.<br /><br />The **Operator Installation** form appears.

5. Set the **Installation Mode** to the project namespace selected previously.

6. Set the **Approval Strategy** to either **Automatic** or **Manual** as per your preference.

The general recommendation is to select **Automatic** option.{: tip}

7. Click **Install**.

8. Check the status of the IBM block storage CSI driver operator.

    Wait until the **Status** is _Up to date_ and then _Succeeded_.

While waiting for the **Status** to change from _Up to date_ to _Succeeded_, you can check the pod progress and readiness status from **Workloads** > **Pods**.{: tip}

9. After the operator installation progress is complete, click the installed IBM block storage CSI driver operator.

10. (Optional) Create the host definer (`HostDefiner`).

    A YAML file opens in the web console. This file can be left as-is, or edited as needed. For more information, see [Configuring the host definer](../configuration/configuring_hostdefiner.md).

11. Create the IBM block storage CSI driver (`IBMBlockCSI`).

    A YAML file opens in the web console. This file can be left as-is, or edited as needed.

12. Update the YAML file to include your user-defined namespace.

13. After everything is created, wait until the **Status** is _Running_.

