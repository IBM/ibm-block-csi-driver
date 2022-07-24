# Uninstalling the driver with GitHub

Use this information to uninstall the IBM® CSI (Container Storage Interface) operator and driver with GitHub.

Perform the following steps to uninstall the CSI driver and operator.

**Note:** When using the host definer and `dynamicNodeLabeling` is set to `true`, be sure to complete the steps in the correct order or `hostdefiner.block.csi.ibm.com/manage-node=true` labels can be left on the nodes.

1. Delete the IBMBlockCSI custom resource.

    ```
    kubectl delete -f csi.ibm.com_v1_ibmblockcsi_cr.yaml
    ```

2. If applicable, delete the HostDefiner custom resource.

    1. Verify that all HostDefinition instances, per configuration allowances, are deleted.
         
            kubectl get hostdefinition
     
        The output displays all HostDefinition instances that do not need to be deleted. If all get deleted, the output displays `No resources found`.

     2. Delete the custom resource.
    
        ```
        kubectl delete -f csi_v1_hostdefiner_cr.yaml
        ```

3. Delete the operator.

    ```
    kubectl delete -f ibm-block-csi-operator.yaml
    ```




