# Uninstalling the driver with the OpenShift web console

Use this information to uninstall the IBM® CSI (Container Storage Interface) operator and driver through the Red Hat® OpenShift® Container Platform web console.

Perform the following steps in order to uninstall the CSI driver and operator through Red Hat OpenShift Container Platform web console.
1.  From the web console go to **Operators** > **Installed Operators**. Select the Project namespace, where installed, from `Projects: <namespace>`.

2.  Select **Operator for IBM block storage CSI driver**.

3.  Select **IBM block storage CSI driver**.

    **Operators** > **Installed Operators** > **Operator Details**.

4.  Click on the **more** menu for the **ibm-block-csi** driver and select **Delete IBMBlock CSI**.

    Wait for the controller and node pods to terminate.

    This deletes the CSI driver. Continue to step [5](#operator) to delete the operator for IBM block storage CSI driver.

<a name="operator"></a>5.  From the **Installed Operators** page, click on the **more** menu for the **Operator for IBM block storage CSI driver** and select **Uninstall Operator**.


