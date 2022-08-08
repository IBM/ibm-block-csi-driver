# Uninstalling the driver with OperatorHub.io

Use this information to uninstall the IBM® CSI (Container Storage Interface) operator and driver with OperatorHub.io.

To uninstall the CSI driver with OperatorHub.io, use the `kubectl delete -f` command to delete the YAML files, one at a time, in the reverse order of the installation steps that are documented in  https://operatorhub.io/operator/ibm-block-csi-operator-community.

**Note:** When using host definition and `dynamicNodeLabeling` is set to `true`, complete the steps in the correct order or `hostdefiner.block.csi.ibm.com/manage-node=true` labels can be left on the nodes.

**Note:** To see the installation steps, click **Install** on the OperatorHub.io webpage.