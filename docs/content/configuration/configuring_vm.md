# Configuring an OpenShift VM

When creating an OpenShift VM - configure the storage profile to "Shared Access (RWX)". A common use case is live migration.

Set the FlashSystem StorageClass as the default SC, so that boot device of VMs are created as PVCs using this SC
In the main VM creation window click the "Customize VirtualMachine" button
In the "Customize template parameters" window click "Customize VirtualMachine parameters"
In the main parameters window - click the Disks tab
In the disk list - locate the PVC and click the three dots on the right of the line, in the menu select Edit
In the "Edit disk" window - uncheck "Apply optimized StorageProfile settings" and in "Access Mode" select "Shared access (RWX)". Then Click Save

