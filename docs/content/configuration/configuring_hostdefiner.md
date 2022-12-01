# Configuring the host definer

Some of the parameters within the HostDefiner custom resource are configurable. Use this information to help decide whether the parameters for your storage system need to be updated.

For more information about using the host definer, see [Using dynamic host definition](../using/using_hostdefinition.md).
    
|Field|Description|
|---------|--------|
|`prefix`|Adds a prefix to the hosts defined by the host definer.<br>**Note:** The prefix length is bound by the limitation of the storage system. When defined, the length is a combination of both the prefix and node (server) hostname.|
|`connectivityType`|Selects the connectivity type for the host ports.<br>Possible input values are:<br>- `nvmeofc` for use with NVMe over Fibre Channel connectivity<br>- `fc` for use with Fibre Channel over SCSI connectivity<br>- `iscsi` for use with iSCSI connectivity<br>By default, this field is blank and the host definer selects the first of available connectivity types on the node, according to the following hierarchy: NVMe, FC, iSCSI.|
|`allowDelete`|Defines whether the host definer is allowed to delete host definitions on the storage system.<br>Input values are `true` or `false`.<br>The default value is `true`.|
|`dynamicNodeLabeling`|Defines whether the nodes that run the CSI node pod are dynamically labeled or if the user must create the `hostdefiner.block.csi.ibm.com/manage-node=true` label on each relevant node. This label tells the host definer which nodes to manage their host definition on the storage side.<br>Input values are `true` or `false`.<br>The default value is `false`, where the user must manually create this label on every node to be managed by the host definer for dynamic host definition on the storage.|

For an example HostDefiner yaml file, see [csi_v1_hostdefiner_cr.yaml](https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/v1.11.0/config/samples/csi_v1_hostdefiner_cr.yaml).