# Using dynamic host definition

Dynamic host connectivity removes the necessity for manual host definitions. This is also facilitates the scaling out process of a node. 

The `HostDefiner` identifies the hosts available for host definition on each storage system and controls each of the host definitions. To see the statuses of the managed host definitions by the host definer, use the `hostdefinition` command.

Use the `hostdefinitions` command to see the phase status of all definitions on the storage side.

     $> kubectl get hostdefinitions

|Phase|Description|
|---------|--------|
|Ready|Host definition is created and is working as expected.|
|PendingForCreation|Host definition did not complete during the last attempt. The `HostDefiner` will try again.|
|PendingForDeletion|Host deletion did not complete during the last attempt. The `HostDefiner` will try again.|
|Error|Host definition or deletion did not complete and will not try again.|

## Recovering from an Error state

If any of the host definitions have an Error status, follow this procedure to have the `HostDefiner` reattempt to define the hosts.

1. Undeploy the CSI node pod from the relevant node that the `HostDefinition` is a part of.
2. Verify that all `HostDefinition` instances of the node are deleted.
     
          $> kubectl get hostdefinition | grep <hostname> | wc -l
     
     The output should be `0`.
3. Redeploy the CSI node pod on the relevant node.

     The HostDefiner handles all of the new host definitions.
        
4. Verify that the `hostdefinition` is in the _Ready_ phase.

    ```
    $> kubectl get hostdefinition
    NAME                     AGE    PHASE   STORAGE               NODE
    <host_definition_name1>  102m   Ready   <management_address>  <node_name1>
    <host_definition_name2>  102m   Ready   <management_address>  <node_name2>
    ```

## Updating the `HostDefiner.yaml`

Some of the parameters within the `HostDefiner.yaml` are configurable. Use this information to help decide whether the parameters for your storage system need to be updated.
    
|Field|Description|
|---------|--------|
|prefix|Adds a prefix to the hosts defined by the CSI driver.|
|connectivity|Selects the connectivity type for the host ports.<br>Possible input values are:<br>- `nvme` for use with NVME over Fibre Channel connectivity<br>- `fc` for use with Fibre Channel over SCSI connectivity<br>- `iscsi` for use with iSCSI connectivity<br><br>By default, this field is blank and the driver selects the first of available connectivity types available on the storage system, according to the following hierarchy: NVMe, FC, iSCSI.|
|allowDelete|Defines whether the `HostDefiner` is allowed to delete host definitions.<br>Input values are `true` or `false`.<br>The default value is `true`.|
|dynamicNodeLabeling|Defines whether the `HostDefiner` chooses which nodes to manage dynamically by their CSI node resource or if the user must create the `hostdefiner.block.csi.ibm.com/managed-by=true` label on each relevant node.<br>Input values are `true` or `false`.<br>The default value is `false`, where the user must create each label.|

## Blocking a specific node from being deleted

In order to block a specific node from being deleted by the CSI driver, you can add the following label to the node: `hostdefiner.block.csi.ibm.com/avoid-deletion=true`.

This works on a per node basis, where the `allowDelete` parameter definition in the `HostDefiner.yaml` is for all nodes on the system.

## Changing host connectivity

To be able to identify new host connectivity, when the host connectivity type on the storage system needs to be changed, use the following procedure:


1. Undeploy the CSI node pod from the relevant node that the `HostDefinition` is a part of.
2. Verify that all `HostDefinition` instances of the node are deleted.
     
          $> kubectl get hostdefinition | grep <hostname> | wc -l
     
     The output should be `0`.
3. From the host, change the host connectivity type.
4. Redeploy the CSI node pod on the relevant node.

     The HostDefiner handles all of the new host definitions.
