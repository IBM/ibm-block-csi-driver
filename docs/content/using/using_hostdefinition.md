# Using dynamic host definition

Dynamic host connectivity eliminates the necessity for manual host definitions. The host definer handles changes in the orchestrator cluster that relate to the host definition and applies them to the relevant storage systems. As a result, this also facilitates the scaling out process of a cluster.

A use case example of using dynamic host definition is when creating a new storage class with a new storage. With the dynamic host definition feature, new host definitions are created on the storage for the relevant nodes. For each host definition on the storage, a new host definition resource is created. With these resources, the status of the host definition on the storage system can easily be retrieved.

The host definer identifies the nodes available for host definition on each storage system and controls each of the host definitions. To see the phase status of all managed host definitions by the host definer, use:

     $> kubectl get hostdefinitions

|Phase|Description|
|---------|--------|
|Ready|Host definition is created and is working as expected.|
|PendingCreation|Host definition did not complete during the last attempt. The host definer will try again.|
|PendingDeletion|Host deletion did not complete during the last attempt. The host definer will try again.|
|Error|Host definition or deletion did not complete and will not try again.|

## Recovering from an Error state

If any of the host definitions have an Error status, follow this procedure to have the host definer reattempt to define the hosts.

1. Undeploy the CSI node pod from the relevant node that the `HostDefinition` is a part of.
2. Verify that all `HostDefinition` instances of the node are deleted.
     
          $> kubectl get hostdefinition | grep <nodename> | wc -l
     
     The output should be `0`.
3. Redeploy the CSI node pod on the relevant node.

     The host definer handles the creation of the new host definition on the storage side.
        
4. Verify that the `hostdefinition` is in the _Ready_ phase.

    ```
    $> kubectl get hostdefinition
    NAME                     AGE    PHASE   NODE
    <host_definition_name1>  102m   Ready   <node_name1>
    <host_definition_name2>  102m   Ready   <node_name2>
    ```

## Blocking a specific node definition from being deleted

To block a specific host definition from being deleted by the host definer, you can add the following label to the node: `hostdefiner.block.csi.ibm.com/avoid-deletion=true`.

This label works on a per node basis, where the `allowDelete` parameter definition in the `csi_v1_hostdefiner_cr.yaml` is for all cluster nodes.
