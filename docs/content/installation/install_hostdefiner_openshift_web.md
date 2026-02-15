
{{site.data.keyword.attribute-definition-list}}

# Installing the host definer with the OpenShift web console

When using the Red Hat® OpenShift® Container Platform, the HostDefiner custom resource can be installed directly from OpenShift Container Platform web console, through the OperatorHub. 

Before installing or upgrading, if one wants non-default configuration settings, a configmap must be created See [Configuring the host definer](../configuration/configuring_hostdefiner.md). The old way of setting parameters in yaml is obsolete.
{: .important}

1. From the web console, navigate to the **IBM block storage CSI driver operator** within your project namespace.

2. From the IBM block storage CSI Host Definer driver tab, click Create `HostDefiner`.

3. After everything is created, wait until the **Status** is _Running_.
