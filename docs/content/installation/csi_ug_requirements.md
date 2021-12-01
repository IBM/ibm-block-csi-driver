# Compatibility and requirements

For the complete and up-to-date information about the compatibility and requirements for using the IBM® block storage CSI driver, refer to its latest release notes. The release notes detail supported operating system and container platform versions, and microcode versions of the supported storage systems.

Before beginning the installation of the CSI (Container Storage Interface) driver, be sure to verify that you comply with the following prerequisites.

For IBM Cloud® Satellite users, see [cloud.ibm.com/docs/satellite](https://cloud.ibm.com/docs/satellite) for full system requirements.

**Important:** When using Satellite, complete the following checks, configurations, and the installation process before assigning the hosts to your locations. </br>In addition, **do not** create a Kubernetes cluster. Creating the Kubernetes cluster is done through Satellite.

-   The CSI driver requires the following ports to be opened on the worker nodes OS firewall:
    -   **For all iSCSI users**

        Port 3260

    -   **FlashSystem A9000 and A9000R**

        Port 7778

    -   **IBM Spectrum® Virtualize Family includes IBM® SAN Volume Controller and IBM FlashSystem® family members that are built with IBM Spectrum® Virtualize (including FlashSystem 5xxx, 7200, 9100, 9200, 9200R)**

        Port 22

    -   **DS8000® Family systems**

        Port 8452

-   Be sure that multipathing is installed and running.

Complete these steps for each worker node in Kubernetes cluster to prepare your environment for installing the CSI (Container Storage Interface) driver.

1. Configure Linux® multipath devices on the host.

   **Important:** Be sure to configure each worker with storage connectivity according to your storage system instructions. For more information, find your storage system documentation in [IBM Documentation](http://www.ibm.com/docs/).

   **Additional configuration steps for OpenShift® Container Platform users (RHEL and RHCOS).** Other users can continue to step 3.

   Download and save the following YAML file:

   ```
   curl https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/master/deploy/99-ibm-attach.yaml > 99-ibm-attach.yaml
   ```

   This file can be used for both Fibre Channel and iSCSI configurations. To support iSCSI, uncomment the last two lines in the file.

   **Important:**
   - The `99-ibm-attach.yaml` configuration file overrides any files that exist on your system. Only use this file if the files mentioned are not already created. <br />If one or more were created, edit this YAML file, as necessary.
   - The `99-ibm-attach.yaml` configuration file with the default configuration by the CSI driver. It is best practice to update the file according to your storage system and application networking needs.

   Apply the YAML file.

   `oc apply -f 99-ibm-attach.yaml`

2. Configure storage system connectivity.

    1.  Define the host of each Kubernetes node on the relevant storage systems with the valid WWPN (for Fibre Channel) or IQN (for iSCSI) of the node.

    2.  For Fibre Channel, configure the relevant zoning from the storage to the host.

    3. Ensure proper connectivity.

3. **For RHEL OS users:** Ensure that the following packages are installed.

    If using RHCOS or if the packages are already installed, this step may be skipped.

    - sg3_utils
    - iscsi-initiator-utils
    - device-mapper-multipath
    - xfsprogs (if XFS file system is required)

4. (Optional) If planning on using volume snapshots (FlashCopy® function), enable support on your Kubernetes cluster.

   For more information and instructions, see the Kubernetes blog post, [Kubernetes 1.20: Kubernetes Volume Snapshot Moves to GA](https://kubernetes.io/blog/2020/12/10/kubernetes-1.20-volume-snapshot-moves-to-ga/).

   Install both the Snapshot CRDs and the Common Snapshot Controller once per cluster.

   The instructions and relevant YAML files to enable volume snapshots can be found at: [https://github.com/kubernetes-csi/external-snapshotter#usage](https://github.com/kubernetes-csi/external-snapshotter#usage)

5. (Optional) If planning on using volume replication (remote copy function), enable support on your orchestration platform cluster and storage system.
    
    1. To enable support on your Kubernetes cluster, install the following replication CRDs once per cluster.

        ```
        curl -O https://raw.githubusercontent.com/csi-addons/volume-replication-operator/v0.2.0/config/crd/bases/replication.storage.openshift.io_volumereplicationclasses.yaml
        kubectl apply -f ./replication.storage.openshift.io_volumereplicationclasses.yaml
        
        curl -O https://raw.githubusercontent.com/csi-addons/volume-replication-operator/v0.2.0/config/crd/bases/replication.storage.openshift.io_volumereplications.yaml
        kubectl apply -f ./replication.storage.openshift.io_volumereplications.yaml
        ````
    
    2. To enable support on your storage system, see the following section within your Spectrum Virtualize product documentation on [IBM Documentation](https://www.ibm.com/docs/en/): **Administering** > **Managing Copy Services** > **Managing remote-copy partnerships**.

6. (Optional) To use CSI Topology, at least one node in the cluster must have the label-prefix of `topology.block.csi.ibm.com` to introduce topology awareness:
      
      **Important:** This label-prefix must be found on the nodes in the cluster **before** installing the IBM® block storage CSI driver. If the nodes do not have the proper label-prefix before installation, CSI Topology cannot be used with the CSI driver.

      For more information, see [Configuring for CSI Topology](../configuration/csi_ug_config_topology.md).