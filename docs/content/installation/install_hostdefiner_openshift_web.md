# Installing the host definer with the OpenShift web console

When using the Red Hat® OpenShift® Container Platform, the HostDefiner custom resource can be installed directly from OpenShift Container Platform web console, through the OperatorHub. 

1. From the web console, navigate to the **IBM block storage CSI driver operator** within your project namespace.

2. From the IBM block storage CSI Host Definer driver tab, click Create `HostDefiner`.

    A YAML file opens in the web console. This file can be left as-is, or edited as needed. For more information, see [Configuring the host definer](../configuration/configuring_hostdefiner.md).

3. After everything is created, wait until the **Status** is _Running_.