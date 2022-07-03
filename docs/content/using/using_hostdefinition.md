# Using dynamic host definition

Dynamic host connectivity eliminates the necessity for manual host definitions. The host definer custom resource handles changes in the Kubernetes clusters that relate to the host definition feature and applies them to the relevant clusters. This also facilitates the scaling out process of a cluster. 

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

     The host definer handles the creation of the new host definition.
        
4. Verify that the `hostdefinition` is in the _Ready_ phase.

    ```
    $> kubectl get hostdefinition
    NAME                     AGE    PHASE   STORAGE               NODE
    <host_definition_name1>  102m   Ready   <management_address>  <node_name1>
    <host_definition_name2>  102m   Ready   <management_address>  <node_name2>
    ```

## Updating the host definer` custom resource

Some of the parameters within the host definer custom resource are configurable. Use this information to help decide whether the parameters for your storage system need to be updated.
    
|Field|Description|
|---------|--------|
|prefix|Adds a prefix to the hosts defined by the host definer.|
|connectivity|Selects the connectivity type for the host ports.<br>Possible input values are:<br>- `nvme` for use with NVME over Fibre Channel connectivity<br>- `fc` for use with Fibre Channel over SCSI connectivity<br>- `iscsi` for use with iSCSI connectivity<br><br>By default, this field is blank and the host definer selects the first of available connectivity types on the storage system, according to the following hierarchy: NVMe, FC, iSCSI.|
|allowDelete|Defines whether the host definer is allowed to delete host definitions.<br>Input values are `true` or `false`.<br>The default value is `true`.|
|dynamicNodeLabeling|Defines whether the nodes that run our CSI node pod are dynamically labelled or if the user must create the `hostdefiner.block.csi.ibm.com/managed-by=true` label on each relevant node. In addition, this label allows the host definer to find all nodes with this label and manages their storage definition.<br>Input values are `true` or `false`.<br>The default value is `false`, where the user must create each label.|

## Blocking a specific node definition from being deleted

In order to block a specific host definition from being deleted by the host definer, you can add the following label to the node: `hostdefiner.block.csi.ibm.com/avoid-deletion=true`.

This works on a per node basis, where the `allowDelete` parameter definition in the `csi_v1_hostdefiner.yaml` is for all cluster nodes.
