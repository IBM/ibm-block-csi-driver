# Uninstalling the driver with the OpenShift web console

Use this information to uninstall the IBM® CSI (Container Storage Interface) operator and driver through the Red Hat® OpenShift® Container Platform web console.

Perform the following steps in order to uninstall the CSI driver and operator through Red Hat OpenShift Container Platform web console.
1.  From the web console go to **Operators** > **Installed Operators**. Select the Project namespace, where installed, from `Projects: <namespace>`.

2.  Select **IBM block storage CSI driver operator**.

3.  Select **IBM block storage CSI driver**.

    **Operators** > **Installed Operators** > **Operator Details**.

4. If applicable, click on the **more** menu for the **host-definer** driver and select **Delete HostDefiner**.

    Wait for the host definer pod to terminate.

    This deletes the host definer. 

5. Click on the **more** menu for the **ibm-block-csi** driver and select **Delete IBMBlockCSI**.

    Wait for the controller and node pods to terminate.

    This deletes the CSI driver. Continue to the next step to delete the Operator for IBM block storage CSI driver.

6. From the **Installed Operators** page, click on the **more** menu for the **IBM block storage CSI driver operator** and select **Uninstall Operator**.


