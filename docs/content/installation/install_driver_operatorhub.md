# Installing the driver with OperatorHub.io

When using OperatorHub.io, the operator for IBM® block storage CSI driver can be installed directly from the OperatorHub.io website. Installing the CSI (Container Storage Interface) driver is part of the operator installation process.

To install the CSI driver from OperatorHub.io, go to https://operatorhub.io/operator/ibm-block-csi-operator-community and follow the installation instructions, once clicking the **Install** button.

The host definer custom resource can optionally be installed as part of this process. For more information, see [Installing the host definer custom resource](install_hostdefiner.md).

**Note:** To ensure that the operator installs the driver, be sure to apply the YAML file that is located as part of the ibm-block-csi-operator-community page mentioned above.
