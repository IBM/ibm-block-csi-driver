# Manual upgrade with OpenShift

When using the Red Hat® OpenShift® Container Platform, the CSI (Container Storage Interface) driver can be manually updated through the OpenShift web console.

1.  From Red Hat OpenShift Container Platform web console, see the status of the **ibm-block-csi-operator**.

2. Check if any subscription upgrade approvals are pending.

3. Install the IBM block storage CSI driver operator and driver.

4. Verify that both the **Controller Image Tab** and **Node Image Tag** are showing the most up-to-date version of the driver and the **Status** is _Running_.

13. (Optional for initial host definer installation) If desired, create the host definer (`HostDefiner`).

    A YAML file opens in the web console. This file can be left as-is, or edited as needed. For more information, see [Configuring the host definer](../configuration/configuring_hostdefiner.md).


