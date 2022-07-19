{:note: .note}

# Uninstalling the driver with GitHub

Use this information to uninstall the IBM® CSI (Container Storage Interface) operator and driver with GitHub.

Perform the following steps to uninstall the CSI driver and operator.

When using host definition and `dynamicNodeLabeling` is set to `true`, if these steps is not completed in the correct order, `hostdefiner.block.csi.ibm.com/manage-node=true` labels can be left on the nodes.
{:note: .note}


1. Delete the IBMBlockCSI custom resource.

    ```
    kubectl delete -f csi.ibm.com_v1_ibmblockcsi_cr.yaml
    ```

2. If applicable, delete the HostDefiner custom resource.

    1. Verify that all host definition instances of the node are deleted.
     
            kubectl get hostdefinition | grep <node-name>
     
        The output should be `0`.

     2. Delete the custom resource.
    
        ```
        kubectl delete -f csi_v1_hostdefiner_cr.yaml
        ```

3. Delete the operator.

    ```
    kubectl delete -f ibm-block-csi-operator.yaml
    ```




