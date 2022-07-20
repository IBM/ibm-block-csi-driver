# Changing node connectivity

Node connectivity for dynamic host definition needs to be updated when node connectivity changes take place.

Before you begin, if the `allowDelete` parameter is set to `false`, ensure that the old host definition is deleted.

Use the following procedure to redefine host connectivity:

1. Undeploy the CSI node pod from the relevant node that the host definition is a part of.
2. Verify that all host definition instances of the node are deleted.
     
          kubectl get hostdefinition | grep <node-name>
     
     The output displays all host definitions that do not need to be deleted. If all get deleted, the output displays `No resources found`.
3. From the node, perform the connectivity changes.
4. Redeploy the CSI node pod on the relevant node.

     The host definer handles the creation of the new host definition on the storage side.
