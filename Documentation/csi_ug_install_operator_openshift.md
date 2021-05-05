# Installing the driver using the OpenShift web console

When using the Red Hat® OpenShift® Container Platform, the operator for IBM® block storage CSI driver can be installed directly from OpenShift web console, through the OperatorHub. Installing the CSI \(Container Storage Interface\) driver is part of the operator installation process.

The Red Hat OpenShift Container Platform uses the following SecurityContextConstraints for the following serviceAccounts:

**Note:** This data is for informational purposes only.

|serviceAccount|SecurityContextConstraint|
|--------------|-------------------------|
|ibm-block-csi-operator|restricted|
|ibm-block-csi-controller-sa|anyuid|
|ibm-block-csi-node-sa|privileged|

1.  From Red Hat OpenShift Container Platform **Home** \> **Projects**, click **Create Project**. In the **Create Project** dialog box, enter a Project name \(also referred to as namespace\). Click **Create** to save.

2.  From **Operators** \> **OperatorHub**. Select the namespace from **Projects:** **<namespace\>**, defined in step [1](#create_namespace).

3.  Search for IBM block storage CSI driver.

4.  Select the **Operator for IBM block storage CSI driver** and click **Install**.

    The Operator Installation form appears.

5.  Set the **Installation Mode** to the project namespace selected previously, in step [2](#project_define), under **A specific namespace on the cluster**.

6.  Set the **Approval Strategy** to either **Automatic** or **Manual** as per your preference.

    **Note:** The general recommendation is to select **Automatic** option.

7.  Click **Install**.

8.  From **Operators** \> **Installed Operators**, check the status of the Operator for IBM block storage CSI driver.

    Wait until the **Status** is Up to date and then Succeeded.

    **Note:** While waiting for the **Status** to change from Up to date to Succeeded, you can check the pod progress and readiness status from **Workloads** \> **Pods**.

9.  After the operator installation progress is complete, click the installed Operator for IBM block storage CSI driver.

10. Click **Create Instance** to create the IBM block storage CSI driver \(IBMBlockCSI\).

    A yaml file opens in the web console. This file can be left as-is, or edited as needed.

11. Update the yaml file to include your user-defined namespace.

12. Click **Create**.

    Wait until the **Status** is Running.


**Parent topic:**[Installing the operator and driver](csi_ug_install_operator.md)

