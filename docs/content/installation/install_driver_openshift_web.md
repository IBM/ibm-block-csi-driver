# Installing the driver with the OpenShift web console

When using the Red Hat® OpenShift® Container Platform, the operator for IBM® block storage CSI driver can be installed directly from OpenShift web console, through the OperatorHub. Installing the CSI (Container Storage Interface) driver is part of the operator installation process.

The Red Hat OpenShift Container Platform uses the following `SecurityContextConstraints` for the following `serviceAccounts`:

**Note:** This data is for informational purposes only.

|serviceAccount|SecurityContextConstraint|
|--------------|-------------------------|
|ibm-block-csi-operator|restricted|
|ibm-block-csi-controller-sa|anyuid|
|ibm-block-csi-node-sa|privileged|

1. From Red Hat OpenShift Container Platform **Home** > **Projects**, click **Create Project**. In the **Create Project** dialog box, enter a Project name (also referred to as namespace). Click **Create** to save.

2. From **Operators** > **OperatorHub**. Select the namespace from `Projects:<namespace>`, as defined in step 1.

3. Search for IBM block storage CSI driver.

4. Select the **IBM block storage CSI driver operator** and click **Install**. <br /><br />The **Operator Installation** form appears.

5. Set the **Installation Mode** to the project namespace selected previously, in step 2, under **A specific namespace on the cluster**.

6. Set the **Approval Strategy** to either **Automatic** or **Manual** as per your preference.

    **Note:** The general recommendation is to select **Automatic** option.

7. Click **Install**.

8. From **Operators** > **Installed Operators**, check the status of the IBM block storage CSI driver operator.

    Wait until the **Status** is _Up to date_ and then _Succeeded_.

    **Note:** While waiting for the **Status** to change from _Up to date_ to _Succeeded_, you can check the pod progress and readiness status from **Workloads** > **Pods**.

9. After the operator installation progress is complete, click the installed IBM block storage CSI driver operator.

10. Click **Create Instance** to create the IBM block storage CSI driver (`IBMBlockCSI`).

    A YAML file opens in the web console. This file can be left as-is, or edited as needed.

11. (Optional) Click **Create Instance** to create the host definer (`HostDefiner`).

    A YAML file opens in the web console. This file can be left as-is, or edited as needed. For more information, see [Configuring the host definer](../configuration/configuring_hostdefiner).


11. Update the YAML file to include your user-defined namespace.

12. Click **Create**.

    Wait until the **Status** is _Running_.