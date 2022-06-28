# Installing the `HostDefiner` custom resource

Install the `HostDefiner` custom resource to enable dynamic host definitions from within the CSI driver.

For more information on using dynamic host connectivity definition, see [Using dynamic host definition](../using/using_hostdefinition.md).

Use the following steps to install the `HostDefiner` custom resource, with [GitHub](https://github.com/IBM/ibm-block-csi-operator).

**Note:** Only hosts added after the `HostDefiner` installation are dynamically defined within the CSI driver.

Download the `HostDefiner` customer resource manifest from [GitHub](https://github.com/IBM/ibm-block-csi-operator).

    curl https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/v1.10.0/config/samples/csi_v1_hostdefiner.yaml > csi_v1_hostdefiner.yaml

After the `HostDefiner` is installed, it searches for nodes that have not yet been defined on storage, and defines them.