# Configuring SVC partitions / High Availability Partitions (PBHA)

Use this information for specific configuring information when using Partitions / high availaibility partitions (PBHA) with the IBM® block storage CSI driver.

Partitions is an SVC feature that allows virtualization and high availability support.

The partition, default volume group and high availability policy is defined directly n the storage, there's no option to configure from CSI.
PBHA configurations require a single management iP supported in SVC since 9.1.0) - single secret needs to be used with that management IP.

Since host definer (if used) always defines all the ports of each worker (if allowed by node labelling / configuration) - assignment to partitions is also cluster/node based.

To change a secret to use a partition, change used partition or stop using a partition - it's possible to add/edit/remove the partition_name parameter in the secret. Make sure that no PVC is in use before making the change.
The host definer currently doesn't support secret update to stop using a partition - it requires removal and recreation of the secret.

Moving volumes between volume groups is currently not supported for partitions (not implemneted in SVC)

See the following sections for more information:
- [Limitations](../release_notes/limitations.md)
- [Creating a Partitions secret](creating_secret.md)
