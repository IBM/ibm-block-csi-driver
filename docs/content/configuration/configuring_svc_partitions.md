# Configuring IBM Storage Virtualize® partitions / High Availability Partitions (PBHA)

Use this information for specific configuring information when using Partitions / high availaibility partitions (PBHA) with the IBM® block storage CSI driver.

Partitions is an IBM Storage Virtualize® feature that allows virtualization and high availability support.

Minimum IBM Storage Virtualize® version supported by CSI is 9.1.0.1

The partition, default volume group and high availability policy is defined directly on the storage, there's no option to configure from CSI.
PBHA configurations require a single management IP (supported in IBM Storage Virtualize® since 9.1.0.1) - single secret needs to be used with that management IP.
In PBHA - CSI doesn't support iSCSI (between cluster and storage).

Since host definer (if used) always defines all the ports of each worker (if allowed by node labelling / configuration) - assignment to partitions is also cluster/node based.

To change a secret to use a partition, change used partition or stop using a partition - it's possible to add/edit/remove the partition_name parameter in the secret. Make sure that no PVC is in use before making the change.
The host definer currently doesn't support secret update to stop using a partition - it requires removal and recreation of the secret.

High Hvailability Partitions:
- Note if volume is mapped when the peer system is down - multipath devices won't be created for the down system.
- It is strongly advised to install the utility that fascilitates auto rescan of devices in case of failover.
https://github.com/IBM/oss-rescan-storage-linux-udev
- Snapshots aren't supported yet in IBM Storage Virtualize® (9.1.0.1).


See the following sections for more information:
- [Limitations](../release_notes/limitations.md)
- [Creating a Partitions secret](creating_secret.md)
