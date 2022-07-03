# Using dynamic host definition

Dynamic host connectivity eliminates the necessity for manual host definitions. The host definer handles changes in the Kubernetes clusters that relate to the host definition feature and applies them to the relevant clusters. This also facilitates the scaling out process of a cluster. 

A use case example of this is when creating a new storage class with a new storage. With the dynamic host definition feature, the new storage is applied to the relevant clusters and for each host on the storage, a new host definition resource is created. With these resources the status of the host on the storage system can easily be retrieved.

The host definer identifies the hosts available for host definition on each storage system and controls each of the host definitions. To see the phase status of all managed host definitions by the host definer, use:

     $> kubectl get hostdefinitions

|Phase|Description|
|---------|--------|
|Ready|Host definition is created and is working as expected.|
|PendingForCreation|Host definition did not complete during the last attempt. The host definer will try again.|
|PendingForDeletion|Host deletion did not complete during the last attempt. The host definer will try again.|
|Error|Host definition or deletion did not complete and will not try again.|

## Recovering from an Error state

If any of the host definitions have an Error status, follow this procedure to have the host definer reattempt to define the hosts.

1. Undeploy the CSI node pod from the relevant node that the `HostDefinition` is a part of.
2. Verify that all `HostDefinition` instances of the node are deleted.
     
          $> kubectl get hostdefinition | grep <hostname> | wc -l
     
     The output should be `0`.
3. Redeploy the CSI node pod on the relevant node.

     The host definer handles the creation of the new host definition on the storage side.
        
4. Verify that the `hostdefinition` is in the _Ready_ phase.

    ```
    $> kubectl get hostdefinition
    NAME                     AGE    PHASE   STORAGE               NODE
    <host_definition_name1>  102m   Ready   <management_address>  <node_name1>
    <host_definition_name2>  102m   Ready   <management_address>  <node_name2>
    ```

## Blocking a specific node definition from being deleted

In order to block a specific host definition from being deleted by the host definer, you can add the following label to the node: `hostdefiner.block.csi.ibm.com/avoid-deletion=true`.

This works on a per node basis, where the `allowDelete` parameter definition in the `csi_v1_hostdefiner.yaml` is for all cluster nodes.
