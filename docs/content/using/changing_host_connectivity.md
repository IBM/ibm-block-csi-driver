# Changing host connectivity

When node connectivity changes take place, use the following procedure to redefine host connectivity:

1. Undeploy the CSI node pod from the relevant node that the `HostDefinition` is a part of.
2. Verify that all `HostDefinition` instances of the node are deleted.
     
          $> kubectl get hostdefinition | grep <hostname> | wc -l
     
     The output should be `0`.
3. From the host, change the host connectivity type.
4. Redeploy the CSI node pod on the relevant node.

     The host definer handles all of the new host definitions.
