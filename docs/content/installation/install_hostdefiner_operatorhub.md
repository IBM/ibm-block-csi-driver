
{{site.data.keyword.attribute-definition-list}}

# Installing the host definer with OperatorHub.io

Before installing or upgrading, if one wants non-default configuration settings, a configmap must be created See [Configuring the host definer](../configuration/configuring_hostdefiner.md). The old way of setting parameters in yaml is obsolete.
{: .important}


When using OperatorHub.io, the host definer can be installed directly from the OperatorHub.io website.

From [IBM block storage CSI driver operator](https://operatorhub.io/operator/ibm-block-csi-operator-community) on OperatorHub.io, apply the HostDefiner custom resource definition yaml provided. For more information, see [Configuring the host definer](../configuration/configuring_hostdefiner.md).

To ensure that the operator installs the driver, be sure to apply the YAML file that is located as part of the ibm-block-csi-operator-community page mentioned above.{: note}
