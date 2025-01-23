# Configuring a Virtual Machine on RedHat OpenShift

**Important:** Prior to configuring Virtual Machines on RedHat Openshift® with the IBM® Block Storage CSI driver, vdisk protection must be disabled globally or for the specific child pools to be used on the connected IBM FlashSystem.

When creating a Virtual Machine on RedHat OpenShift - configure the storage profile to "Shared Access (RWX)". This is required to support Virtual Machine live migration.

1. Set the StorageClass configured to use the IBM Block Storage CSI driver as the default StorageClass, so that boot devices of VMs are created on the desired FlashSystem storage
2. In the main VM creation window click the "Customize VirtualMachine" button
3. In the "Customize template parameters" window click "Customize VirtualMachine parameters"
4. In the main parameters window click the "Disks" tab
5. In the disk list locate the boot device PVC (and any other desired disk) and click the three dots on the right of the line, selecting "Edit" in the menu
6. In the "Edit disk" window uncheck "Apply optimized StorageProfile settings" and in "Access Mode" select "Shared access (RWX)". Then Click Save

**Note:** In future releases of RedHat OpenShift, "Shared Access (RWX)" will be the default storage profile for Virtual Machines using the IBM Block Storage CSI driver.

**Note:** Virtual Machine support on RedHat OpenShift is limited to specific cluster configurations and requirements. Please review the latest RedHat OpenShift Virtualization installation guide.
