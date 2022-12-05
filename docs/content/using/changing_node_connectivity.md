# Changing node connectivity

Node connectivity for dynamic host definition needs to be updated manually when node connectivity changes take place of the `connectivityType` in the HostDefiner is defined.

Starting from CSI driver 1.11.0, if the `connectivityType` in the HostDefiner is left with its default empty value, the host ports dynamically update. For more information, see [Configuring the host definer](../configuration/configuring_hostdefiner.md).

Before you begin, if the `allowDelete` parameter is set to `false`, ensure that the old host definition is deleted.

Use the following procedure to redefine host connectivity:

1. Undeploy the CSI node pod from the relevant node that the host definition is a part of.
2. Verify that all HostDefinition instances of the node are deleted.
     
          kubectl get hostdefinitions -o=jsonpath='{range .items[?(@.spec.hostDefinition.nodeName=="<node-name>")]}{.metadata.name}{"\n"}{end}'
     
     The output displays all HostDefinitions that do not need to be deleted for the `<node-name>`.

3. From the node, perform the connectivity changes.
4. Redeploy the CSI node pod on the relevant node.

     The host definer handles the creation of the new host definition on the storage side.

