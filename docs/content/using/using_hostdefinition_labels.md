
{{site.data.keyword.attribute-definition-list}}

# Configuration paramaters now in config map

Starting from release 1.13.0, the global configuration parameters are maintained in a config map, rather than the host definer yaml as earlier releases.
Please refer to the host definer configuration page for more information.

# Adding optional labels for dynamic host definition

Adding labels to nodes allows for greater control over the system nodes, when using dynamic host definition.

## Blocking a specific node definition from being deleted

To block a specific host definition from being deleted by the host definer, you can add the following label to the node: `hostdefiner.block.csi.ibm.com/avoid-deletion=true`.

This label works on a per node basis, where the `allowDelete` parameter definition in the config map is for all cluster nodes.

## Defining a specific host node

In addition to defining `connectivityType` in the config map, the node's connectivity type can be defined to override the `connectivityType` definition within the config map by using the `connectivity-type` label.

This tag defines the connectivity type of the node regardless of connectivity hierarchy.

For example, if `connectivityType` is defined as using `fc` in the config map, but you want to use NVMe on a specific node, you can define `nvmeofc` for this specific node, using this label.

`block.csi.ibm.com/connectivity-type=<connectivityType>`

The values for the connectivityType label are the same as those for defining the config map: `nvmeofc`, `fc`, `iscsi`. If an invalid label is used, this label is ignored.{: note}

For more information about defining the connectivity type within the HostDefiner, see [Configuring the host definer](../configuration/configuring_hostdefiner.md).{: tip}

As of this document's publication date, NVMe/FC is not supported for this release.{: restriction}

## Specifying I/O group usage

To specify which I/O group(s) a node should use, add any of the following labels to the node:

- `hostdefiner.block.csi.ibm.com/io-group-0=true`
- `hostdefiner.block.csi.ibm.com/io-group-1=true`
- `hostdefiner.block.csi.ibm.com/io-group-2=true`
- `hostdefiner.block.csi.ibm.com/io-group-3=true`

If no `io_group` is defined, the volume is created within the storage system's default I/O group(s). For more about the I/O group function, see **Product overview** > **Technical overview** > **I/O group** within your IBM Storage Virtualize® product documentation on [IBM Documentation](https://www.ibm.com/docs).{: tip}
