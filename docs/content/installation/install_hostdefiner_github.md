# Installing the host definer with GitHub

The host definer for IBM® block storage CSI driver can be installed directly with GitHub.

Use the following steps to install the HostDefiner custom resource, with [GitHub](https://github.com/IBM/ibm-block-csi-operator).

1. Download the custom resource manifest from [GitHub](https://github.com/IBM/ibm-block-csi-operator).

        curl https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/v1.10.0/config/samples/csi_v1_hostdefiner_cr.yaml > csi_v1_hostdefiner_cr.yaml

2. Install the `csi_v1_hostdefiner_cr.yaml`.

        kubectl apply -f csi_v1_hostdefiner_cr.yaml

After the host definer is installed, it searches for nodes that have not yet been defined on storage, and defines them according to the host definer attributes configured by the user (see [Configuring the host definer](../configuration/configuring_hostdefiner)).