# Adding optional labels for dynamic host definition

Adding labels to nodes allows for greater control over the system nodes, when using dynamic host definition.

## Blocking a specific node definition from being deleted

To block a specific host definition from being deleted by the host definer, you can add the following label to the node: `hostdefiner.block.csi.ibm.com/avoid-deletion=true`.

This label works on a per node basis, where the `allowDelete` parameter definition in the `csi_v1_hostdefiner_cr.yaml` is for all cluster nodes.

## Defining a specific host node

In addition to defining `connectivityType` in the HostDefiner, the node's connectivity type can be defined by overriding the `connectivityType` definition within the HostDefiner by using the `connectivity-type` label.

This tag defines the connectivity type of the node regardless of connectivity hierarchy.

For example, if `connectivityType` is defined as using `fc` but you want to use NVMe on a specific node, you can define `nvmeofc` for this specific node, using this label.

`block.csi.ibm.com/connectivity-type=<connectivityType>

**Note:**<br> 
- The values for the connectivityType are the same as those for defining the HostDefiner: `nvmeofc`, `fc`, `iscsi`. If an invalid label is used, this label is ignored.
-  For more information about defining the connectivity type within the HostDefiner, see [Configuring the host definer](../configuration/configuring_hostdefiner.md)).

## Specifying I/O group usage

To specify which I/O group a node should use, add one of the following labels to the node:

- `hostdefiner.block.csi.ibm.com/io-group-0=true`
- `hostdefiner.block.csi.ibm.com/io-group-1=true`
- `hostdefiner.block.csi.ibm.com/io-group-2=true`
- `hostdefiner.block.csi.ibm.com/io-group-3=true`
