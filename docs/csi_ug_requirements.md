# Compatibility and requirements

For the complete and up-to-date information about the compatibility and requirements for using the IBM® block storage CSI driver, refer to its latest release notes. The [release notes](csi_block_storage_kc_rn.md) detail supported operating system and container platform versions, as well as microcode versions of the supported storage systems.

Before beginning the installation of the CSI \(Container Storage Interface\) driver, be sure to verify that you comply with the following prerequisites.

For IBM Cloud® Satellite users, see [cloud.ibm.com/docs/satellite](https://cloud.ibm.com/docs/satellite) for full system requirements.

**Important:** When using Satellite, complete the following checks, configurations, and the installation process before assigning the hosts to your locations.<br />In addition, **do not** create a Kubernetes cluster. This is done through Satellite.

-   The CSI driver requires the following ports to be opened on the worker nodes OS firewall:
    -   **For all iSCSI users**

        Port 3260

    -   **FlashSystem A9000 and A9000R**

        Port 7778

    -   **IBM Spectrum® Virtualize Family includes IBM® SAN Volume Controller and IBM FlashSystem® family members built with IBM Spectrum® Virtualize \(including FlashSystem 5xxx, 7200, 9100, 9200, 9200R\)**

        Port 22

    -   **DS8000® Family systems**

        Port 8452

-   Be sure that multipathing is installed and running.

Perform these steps for each worker node in Kubernetes cluster to prepare your environment for installing the CSI \(Container Storage Interface\) driver.

1. **For RHEL OS users:** Ensure iSCSI connectivity. If using RHCOS or if the packages are already installed, skip this step and continue to step 2.

2. Configure Linux® multipath devices on the host.

   **Important:** Be sure to configure each worker with storage connectivity according to your storage system instructions. For more information, find your storage system documentation in [IBM Documentation](http://www.ibm.com/docs/) (ibm.com/docs/).

   **Additional configuration steps for OpenShift® Container Platform users \(RHEL and RHCOS\).** Other users can continue to step 3.

   Download and save the following yaml file:

   ```
   curl https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/master/deploy/99-ibm-attach.yaml > 99-ibm-attach.yaml
   ```

   This file can be used for both Fibre Channel and iSCSI configurations. To support iSCSI, uncomment the last two lines in the file.

   **Important:** The 99-ibm-attach.yaml configuration file overrides any files that already exist on your system. Only use this file if the files mentioned are not already created.<br />If one or more have been created, edit this yaml file, as necessary.

   Apply the yaml file.

   `oc apply -f 99-ibm-attach.yaml`
    
3. If needed, enable support for volume snapshots \(FlashCopy® function\) on your Kubernetes cluster.

   For more information and instructions, see the Kubernetes blog post, [Kubernetes 1.17 Feature: Kubernetes Volume Snapshot Moves to Beta](https://kubernetes.io/blog/2019/12/09/kubernetes-1-17-feature-cis-volume-snapshot-beta/) (kubernetes.io/blog/2019/12/09/kubernetes-1-17-feature-cis-volume-snapshot-beta/).

   Install both the Snapshot CRDs and the Common Snapshot Controller once per cluster.

   The instructions and relevant yaml files to enable volume snapshots can be found at: [https://github.com/kubernetes-csi/external-snapshotter\#usage](https://github.com/kubernetes-csi/external-snapshotter#usage)

4. Configure storage system connectivity.

    1.  Define the host of each Kubernetes node on the relevant storage systems with the valid WWPN \(for Fibre Channel\) or IQN \(for iSCSI\) of the node.

    2.  For Fibre Channel, configure the relevant zoning from the storage to the host.



