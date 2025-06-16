
{{site.data.keyword.attribute-definition-list}}

# Configuring the host definer

Some of the parameters within the HostDefiner custom resource are configurable. Use this information to help decide whether the parameters for your storage system need to be updated.

Any configuration changes are reverted upon each IBM® Block Storage CSI driver upgrade. If configuration changes are required, it is recommended to uset InstallPlans to manual upgrade only.{: attention}

Consider [configuring dynamic host definition labels](../using/using_hostdefinition_labels.md) when possible to preserve HostDefiner customizations during IBM® Block Storage CSI driver upgrades.{: tip}

For more information about using the host definer, see [Using dynamic host definition](../using/using_hostdefinition.md).

The prefix length is bound by the limitation of the storage system. When defined, the length is a combination of both the prefix and node (server) hostname.{: restriction}

When left blank, the connectivity type will update along with any changes within the host ports, according to the set hierarchy (see `connectivityType` description below). If the value is set and there are host port changes, the connectivity needs to be manually updated. For more information, see [Changing node connectivity](../using/changing_node_connectivity.md).{: attention}

As of this document's publication date, NVMe/FC is not supported for this release.{: restriction}

|Field|Description|
|---------|--------|
|`prefix`|Adds a prefix to the hosts defined by the host definer.|
|`connectivityType`|Selects the connectivity type for the host ports.<br>Possible input values are:<br>- `nvmeofc` for use with NVMe over Fibre Channel connectivity<br>- `fc` for use with Fibre Channel over SCSI connectivity<br>- `iscsi` for use with iSCSI connectivity<br>By default, this field is blank and the host definer selects the first of available connectivity types on the node, according to the following hierarchy: NVMe, FC, iSCSI.|
|`allowDelete`|Defines whether the host definer is allowed to delete host definitions on the storage system.<br>Input values are `true` or `false`.<br>The default value is `true`.|
|`dynamicNodeLabeling`|Defines whether the nodes that run the CSI node pod are dynamically labeled or if the user must create the `hostdefiner.block.csi.ibm.com/manage-node=true` label on each relevant node. This label tells the host definer which nodes to manage their host definition on the storage side.<br>Input values are `true` or `false`.<br>The default value is `false`, where the user must manually create this label on every node to be managed by the host definer for dynamic host definition on the storage.|
|`portSet`|FlashSystem specific field - Specifies the portset for new port definitions (ports already defined on the FlashSystem are not modified).|

For an example HostDefiner yaml file, see [csi_v1_hostdefiner_cr.yaml](https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/v1.12.5/config/samples/csi_v1_hostdefiner_cr.yaml).
