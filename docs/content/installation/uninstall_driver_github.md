# Uninstalling the driver with GitHub

Use this information to uninstall the IBM® CSI (Container Storage Interface) operator and driver with GitHub.

Perform the following steps in order to uninstall the CSI driver and operator.
1. Delete the IBMBlockCSI custom resource.

    ```
    kubectl delete -f csi.ibm.com_v1_ibmblockcsi_cr.yaml
    ```

2. If applicable, delete the HostDefiner custom resource.
    ```
    kubectl delete -f csi_v1_hostdefiner_cr.yaml
    ```

3. Delete the operator.

    ```
    kubectl delete -f ibm-block-csi-operator.yaml
    ```




