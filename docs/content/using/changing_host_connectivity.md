# Changing host connectivity

Host connectivity for dynamic host definition needs to be updated when node connectivity changes take place.

Before you begin, be sure to delete the old host definition.

Use the following procedure to redefine host connectivity:

1. Undeploy the CSI node pod from the relevant node that the host definition is a part of.
2. Verify that all host definition instances of the node are deleted.
     
          $> kubectl get hostdefinition | grep <hostname> | wc -l
     
     The output should be `0`.
3. From the host, change the host connectivity type.
4. Redeploy the CSI node pod on the relevant node.

     The host definer handles the creation of the new host definition on the storage side.
