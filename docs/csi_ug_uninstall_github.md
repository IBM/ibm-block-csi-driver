# Uninstalling the driver using GitHub

Use this information to uninstall the IBM® CSI (Container Storage Interface) operator and driver using GitHub.

Perform the following steps in order to uninstall the CSI driver and operator from a command line terminal.
1.  Delete the IBMBlockCSI custom resource.

    ```
    kubectl -n <namespace> delete -f csi.ibm.com_v1_ibmblockcsi_cr.yaml
    ```

2.  Delete the operator.

    ```
    kubectl -n <namespace> delete -f ibm-block-csi-operator.yaml
    ```


