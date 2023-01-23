# Removing a PVC from a volume group with a replication policy

When both Primary and Secondary volume groups are represented on a cluster, their associated PVCs must be removed in this specific order.

**Important:** Be sure to follow these steps in the correct order to prevent a PVC from locking.

For each PVC Primary and Secondary pair to be removed from its volume group:
   1. Remove the Primary PVC volume group labels.
   2. Remove the Secondary PVC volume group labels.<br>
    **Note:** After the Primary PVC volume group labels have been removed, the Secondary PVC associated volume is automatically deleted from the storage system.
