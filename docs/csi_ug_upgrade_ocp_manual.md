# Manual upgrade with OpenShift

When using the Red Hat® OpenShift® Container Platform, the CSI \(Container Storage Interface\) driver can be manually updated through the OpenShift web console.

1.  From Red Hat OpenShift Container Platform **Operators** \> **Installed Operators** see the status of the **ibm-block-csi-operator**.

    If the **Status** is _UpgradePending_, click on the operator.

2.  From the **Subscription Overview** view, click on **1 requires approval**.

    The **Review Manual Install Plan** notice appears.

3.  Click on **Preview Install Plan**.

4.  Review the manual install plan and click **Approve**.

5.  From the **Subscription** tab, check the upgrade status and the installed version.

6.  From **Operators** \> **Installed Operators** \> **Operator for IBM block storage CSI driver**, click **Create Instance**.

7.  Check the **Subscriptions** \> **Subscription Overview** tab see the Operator status.

    Wait for the **Upgrade Status** to be **Upgrading** and **1 requires approval** appears.

8.  Click **1 requires approval**.

    The **Review Manual Install Plan** notice appears.

9.  Click on **Preview Install Plan**.

10. Review the manual install plan and click **Approve**.

11. From the **Subscription** tab, check the upgrade status and the installed version.

12. Check the **Overview** tab and that the **Controller Image Tab** and **Node Image Tag** are showing the most up-to-date version of the driver and the **Status** is _Running_.


