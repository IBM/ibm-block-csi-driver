# What's new in 1.11.0

IBM® block storage CSI driver 1.11.0 introduces the enhancements that are detailed in the following section.

**General availability date:** 15 January 2023


## New support for policy-based replication

This version adds support for policy-based replication that was introduced in IBM Spectrum Virtualize 8.5.2 release. Policy-based replication provides a simplified configuration and management of asynchronous replication between two system. To see if your specific product is supported and for more information, see **What's new** > **Getting started with policy-based replication** within your Spectrum Virtualize product documentation on [IBM Documentation](https://www.ibm.com/docs).

## Added dynamic host definition enhancements

The IBM® block storage CSI driver 1.11.0 host definition feature now supports the following:

- **CSI Topology feature**

    Dynamic host definition now works together with CSI Topology feature. For more information about CSI Topology, see [Configuring for CSI Topology](../configuration/confiugring_toplogy.md).

- **Dynamically configuring host ports**

     Host ports are now automatically updated and changes in host port hierarchy are now identified and automatically updated. For more information, see [Configuring the host definer](../configuration/configuring_hostdefiner.md).

- **I/O group function**

    By default the host definer now creates all definitions across all possible I/O groups. Additionally, an optional label available in order to specify which I/O group(s) should be used on a specific node. For more information, see [Adding optional labels for dynamic host definition](../using/using_hostdefinition_labels.md).

    For more about the I/O group function, see **Product overview** > **Technical overview** > **I/O group** within your Spectrum Virtualize product documentation on [IBM Documentation](https://www.ibm.com/docs).

- **Overriding node host connectivity**

    This version introduces a new label tag, allowing connectivity type definition of a specific node, regardless of connectivity hierarchy. For more information, see [Adding optional labels for dynamic host definition](../using/using_hostdefinition_labels.md).
    
In addition, only valid ports are now defined. For example, if a host has a total of four Fibre Channel ports and only two of them are zoned to the storage system, only the two zoned ports are created on the host.

## Additional supported orchestration platforms for deployment

This version adds support for orchestration platforms Kubernetes 1.25 and Red Hat® OpenShift® 4.12, suitable for deployment of the CSI (Container Storage Interface) driver.