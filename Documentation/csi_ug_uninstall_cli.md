# Uninstalling the driver using CLIs

Use this information to uninstall the IBM® CSI (Container Storage Interface) operator and driver from a command line terminal.

**Note:** These instructions are for command-line terminal users only. If using the Red Hat® OpenShift® Container Platform, follow the instructions detailed in [Uninstalling the driver using the OpenShift web console](csi_ug_uninstall_openshift.md).

Perform the following steps in order to uninstall the CSI driver and operator from a command line terminal.
1.  Delete the IBMBlockCSI custom resource.

    ```
    kubectl -n <namespace> delete -f csi.ibm.com_v1_ibmblockcsi_cr.yaml
    ```

2.  Delete the operator.

    ```
    kubectl -n <namespace> delete -f ibm-block-csi-operator.yaml
    ```


