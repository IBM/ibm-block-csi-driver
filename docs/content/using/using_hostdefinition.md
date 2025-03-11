# Using dynamic host definition

Dynamic host connectivity eliminates the necessity for manual host definitions. The host definer handles changes in the orchestrator cluster that relate to the host definition and applies them to the relevant storage systems. As a result, this also facilitates the scaling out process of a cluster.

A use case example of using dynamic host definition is when creating a new storage class with a new storage. With the dynamic host definition feature, new host definitions are created on the storage for the relevant nodes. For each host definition on the storage, a new host definition resource is created. With these resources, the status of the host definition on the storage system can easily be retrieved.

Dynamic host definitions supports the following:

- **CSI Topology**<br>For more information, see [Configuring for CSI Topology](../configuration/configuring_topology.md).
- **I/O Groups**<br>By default the host definer creates all definitions on all possible I/O groups (0, 1, 2, 3) and there is no need to define the I/O groups.<br>If you want a node to use a specific I/O group, use the I/O group label to specify the usage. For more information, see [Adding optional labels for dynamic host definition](using_hostdefinition_labels.md).

The host definer identifies the nodes available for host definition on each storage system and controls each of the host definitions. To see the phase status of all managed HostDefinitions by the host definer, use:

    kubectl get hostdefinitions

|Phase|Description|
|---------|--------|
|Ready|Host definition is created and is working as expected.|
|PendingCreation|Host definition did not complete during the last attempt. The host definer will try again.|
|PendingDeletion|Host deletion did not complete during the last attempt. The host definer will try again.|
|Error|Host definition or deletion did not complete and will not try again.|

Adding labels to nodes allows for greater control over the system nodes, when using dynamic host definition.

Node labels can be used to help customize node usage with host definition. For more information, see [Adding optional labels for dynamic host definition](using_hostdefinition_labels.md).

## Recovering from an Error state

If any of the host definitions have an Error status, follow this procedure to have the host definer reattempt to define the hosts.

1. Undeploy the CSI node pod from the relevant node that the HostDefinition is a part of.
2. Verify that all HostDefinition instances of the node are deleted.

```
kubectl get hostdefinitions -o=jsonpath='{range .items[?(@.spec.hostDefinition.nodeName=="<node-name>")]}{.metadata.name}{"\n"}{end}'
```

   The output displays all HostDefinitions that do not need to be deleted for the `<node-name>`.

3. Redeploy the CSI node pod on the relevant node.

   The host definer handles the creation of the new host definition on the storage side.
        
4. Verify that the `hostdefinition` is in the _Ready_ phase.

```
$> kubectl get hostdefinition
NAME                     AGE    PHASE   NODE          MANAGEMENT_ADDRESS   
<host_definition_name1>  102m   Ready   <node_name1>  <management_address>
<host_definition_name2>  102m   Ready   <node_name2>  <management_address>
```
