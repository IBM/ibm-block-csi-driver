# Uninstalling the driver using the OpenShift web console

Use this information to uninstall the IBM® CSI \(Container Storage Interface\) operator and driver through the Red Hat® OpenShift® Container Platform web console.

**Note:** These instructions are for Red Hat OpenShift Container Platform users only. If you are not using the Red Hat OpenShift Container Platform, follow the instructions detailed in [Uninstalling the driver using CLIs](csi_ug_uninstall_cli.md).

1.  Perform the following steps in order to uninstall the CSI driver and operator through Red Hat OpenShift Container Platform web console.
2.  From the web console go to **Operators** \> **Installed Operators**. Select the Project namespace, where installed, from **Projects:** **<namespace\>**.

3.  Select **Operator for IBM block storage CSI driver**.

4.  Select **IBM block storage CSI driver**.

    **Operators** \> **Installed Operators** \> **Operator Details**.

5.  Click on the **more** menu for the **ibm-block-csi** driver and select **Delete IBMBlock CSI**.

    Wait for the controller and node pods to terminate.

    This deletes the CSI driver. Continue to step [5](#operator) to delete the operator for IBM block storage CSI driver.

6.  From the **Installed Operators** page, click on the **more** menu for the **Operator for IBM block storage CSI driver** and select **Uninstall Operator**.



