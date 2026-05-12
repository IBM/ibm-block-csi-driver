# Compatibility and requirements

For the complete and up-to-date information about the compatibility and requirements for using the IBM® block storage CSI driver, refer to its latest release notes. The release notes detail supported operating system and container platform versions, and microcode versions of the supported storage systems.

Be sure to verify that you comply with all of the following prerequisites before beginning the installation of the CSI (Container Storage Interface) driver.

For IBM Cloud® Satellite users, see [cloud.ibm.com/docs/satellite](https://cloud.ibm.com/docs/satellite) for full system requirements.

When using Satellite, complete the following checks, configurations, and the installation process before assigning the hosts to your locations. </br>In addition, **do not** create a Kubernetes cluster. Creating the Kubernetes cluster is done through Satellite.{: .important}

The CSI driver requires the following ports to be opened on the worker nodes OS firewall:
 -   **For all iSCSI users**

        Port 3260

 
 -   **IBM Storage® Virtualize family**

        Port 22

 -   **IBM DS8000® family systems**

      Port 8452

Complete these steps to prepare your environment for installing the CSI (Container Storage Interface) driver.

1. Configure Linux® multipath devices, per worker node.

Be sure to configure each worker with storage connectivity according to your storage system instructions. For more information, find your storage system documentation in [IBM Documentation](http://www.ibm.com/docs/).{: .important}

2. Configure your storage system host attachment, per worker node.

    **Note:** IBM® block storage CSI driver 1.11.0 introduced dynamic host definition. For more information and installation instructions, see [Installing the host definer](install_hostdefiner.md). If this feature is not installed, the nodes are not dynamically defined on the storage system and they must be defined manually. <br />
    **Note:** Dynamic host definition is only supported with IBM Storage Virtualize® family products.
    
    Be sure to configure your storage system host attachment according to your storage system instructions.

    The CSI driver supports the following connectivity for each worker node: Fibre Channel (WWPN) and iSCSI (IQN).

    **Note:** 
    - As of this document's publication date, NVMe/FC is not supported for this release.
    - For Fibre Channel connectivity be sure that storage system is using one of the fully supported HBAs compatible with your host connection, as listed in the [IBM System Storage Interoperation Center® (SSIC)](https://www-03.ibm.com/systems/support/storage/ssic/interoperability.wss).
    - The CSI driver supports IBM DS8000® family storage systems only with Fibre Channel connectivity.
       
    For more information, find your storage system documentation in [IBM Documentation](http://www.ibm.com/docs/).

3. **For RHEL OS users:** Ensure that the following packages are installed per worker node.

    If using RHCOS or if the packages are already installed, this step can be skipped.

    - sg3_utils
    - iscsi-initiator-utils
    - device-mapper-multipath
    - xfsprogs (if XFS file system is required)

4. (Optional) To use CSI Topology, at least one node in the cluster must have the label-prefix of `topology.block.csi.ibm.com` to introduce topology awareness.
      
      This label-prefix must be found on the nodes in the cluster **before** installing the IBM® block storage CSI driver. If the nodes do not have the proper label-prefix before installation, CSI Topology cannot be used with the CSI driver.{: .important}

      For more information, see [Configuring for CSI Topology](../configuration/configuring_topology.md).

5. (Optional) If planning on using a high availability (HA) feature (either HyperSwap or stretched topology) on your storage system, see the appropriate sections within your IBM Storage Virtualize® product documentation on [IBM Documentation](https://www.ibm.com/docs/en/):
    - HyperSwap topology planning and configuration
        - **Planning** > **Planning for high availability** > **Planning for a HyperSwap topology system**
        - **Configuring** > **Configuration details** > **HyperSwap system configuration details**
    - Stretched topology planning and configuration ([SAN Volume Controller](https://www.ibm.com/docs/en/sanvolumecontroller) only):
        - **Planning** > **Planning for high availability** > **Planning for a stretched topology system**
        - **Configuring** > **Configuration details** > **Stretched system configuration details**

6. (Optional) If planning on using policy-based replication with your IBM Storage Virtualize® storage system, verify that the correct replication policy is in place. This can be done either through the IBM Storage Virtualize® user interface (go to **Policies** > **Replication policies**) or through the CLI (`lsreplicationpolicy`). If a replication policy is not in place create one before replicating a volume through the CSI driver.
