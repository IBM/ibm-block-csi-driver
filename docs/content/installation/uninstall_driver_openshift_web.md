
{{site.data.keyword.attribute-definition-list}}

# Uninstalling the driver with the OpenShift web console

Use this information to uninstall the IBM® CSI (Container Storage Interface) operator and driver through the Red Hat® OpenShift® Container Platform web console.

Perform the following steps in order to uninstall the CSI driver and operator through Red Hat OpenShift Container Platform web console.

When using host definition and `dynamicNodeLabeling` is set to `true`, if these steps are not completed in the correct order, `hostdefiner.block.csi.ibm.com/manage-node=true` labels can be left on the nodes.{: attention}

1. From the web console, select **IBM block storage CSI driver** within the **IBM block storage CSI driver operator**.

4. Click on the **more** menu for the **ibm-block-csi** driver and select **Delete IBMBlockCSI**.

    Wait for the controller and node pods to terminate.

    This deletes the CSI driver.

5. If applicable, click on the **more** menu for the **host-definer** driver and select **Delete HostDefiner**.

    Wait for the host definer pod to terminate.

    This deletes the host definer.

6. From the **Installed Operators** page, click on the **more** menu for the **IBM block storage CSI driver operator** and select **Uninstall Operator**.


