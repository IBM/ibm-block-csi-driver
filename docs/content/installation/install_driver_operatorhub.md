# Installing the driver with OperatorHub.io

When using OperatorHub.io, the operator for IBM® block storage CSI driver can be installed directly from the OperatorHub.io website. Installing the CSI (Container Storage Interface) driver is part of the operator installation process.

1. Install the CSI operator from OperatorHub.io, go to https://operatorhub.io/operator/ibm-block-csi-operator-community and follow the installation instructions, once clicking the **Install** button.

2. Apply the IBMBlockCSI custom resource definition yaml provided.

    **Note:** To ensure that the operator installs the driver, be sure to apply the YAML file that is located as part of the ibm-block-csi-operator-community page mentioned above.

3. (Optional) Apply the HostDefiner custom resource definition yaml provided. For more information, see [Configuring the host definer](../configuration/configuring_hostdefiner.md).