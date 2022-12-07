# What's new in 1.11.0

IBM® block storage CSI driver 1.11.0 introduces the enhancements that are detailed in the following section.

**General availability date:** 15 January 2023

## Enhanced dynamic host definition

The IBM® block storage CSI driver 1.11.0 host definition feature now supports the following:

- **CSI Topology feature**

    For more information, see [Configuring for CSI Topology](../configuration/confiugring_toplogy.md).

- **Dynamically configuring host ports**

     Changes in host port hierarchy are now identified automatically updated. For more inforation, see [Configuring the host definer](../configuration/configuring_hostdefiner.md).

- **I/O groups**

    By default the host definer now creates all definitions across all possible I/O groups. Additionally, an optional label available in order to specify which I/O group should be used on a specific node. For more information, see [Using dynamic host definition](../configuration/configuring_hostdefiner.md).

- **Overriding node host connectivity**

    This version introduces a new label tag, allowing connectivity type definition of a specific node, regardless of connectivity hierarchy. For more information, see [Adding optional labels for dynamic host definition](../using/using_hostdefinition_labels.md).
    
In addition, only valid ports are now defined when working with Fibre Channel.

## Additional supported orchestration platforms for deployment

This version adds support for orchestration platforms Kubernetes 1.25 and Red Hat® OpenShift® 4.12, suitable for deployment of the CSI (Container Storage Interface) driver.