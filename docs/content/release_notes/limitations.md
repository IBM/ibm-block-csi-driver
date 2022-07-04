# Limitations

As opposed to known issues, limitations are functionality restrictions that are part of the predefined system design and capabilities in a particular version.

## IBM® DS8000® usage limitations

Connectivity limits on the storage side might be reached with DS8000 family products due to too many open connections. This occurs due to connection closing lag times from the storage side.

## Volume snapshot limitations

The following limitations apply when using volume snapshots with the IBM block storage CSI driver:

-   When deleting a PersistentVolumeClaim (PVC), the persistent volume (PV) remains until all snapshots of the specific PV are deleted.
    
    **Note:** This limitation is not relevant when using Snapshot function. For more information, see [Snapshot function limitations](#snapshot-function-limitations).
-   When using the CSI (Container Storage Interface) driver with IBM Spectrum® Virtualize family products, a snapshot can only be used to provision a new volume of equal size.

**Note:** For volume snapshot limitations pertaining specifically to HyperSwap usage, see [HyperSwap volume limitations](#hyperswap-volume-limitations).

## Volume clone limitations

The following limitations apply when using volume clones with the IBM block storage CSI driver:

-   When cloning a PersistentVolumeClaim (PVC), the clone cannot contain a smaller size than the source PVC.

    **Note:** The size can be expanded after the cloning process.

-   A PVC and its clone need to both have the same volume mode (**Filesystem** or **Block**).

**Note:** For high availability volume clone limitations, see [High availability (HA) limitations](#high-availability-ha-limitations).

## Volume expansion limitations

The following limitations apply when expanding volumes with the IBM block storage CSI driver:

-   When using the CSI driver with IBM Spectrum Virtualize family and IBM DS8000 family products, during size expansion of a PersistentVolumeClaim (PVC), the size remains until all snapshots of the specific PVC are deleted.

     **Note:** This limitation is not relevant when using Snapshot function. For more information, see [Snapshot function limitations](#snapshot-function-limitations).
-   When expanding a PVC while not in use by a pod, the volume size immediately increases on the storage side. However, PVC size only increases after a pod uses the PVC.
-   When expanding a file system PVC for a volume that was previously formatted but is now no longer being used by a pod, any copy or replication operations performed on the PVC (such as snapshots or cloning) results in a copy with the newer, larger, size on the storage. However, its file system has the original, smaller, size.

## Volume replication limitations

When a role switch is conducted, this is not reflected within the other orchestration platform replication objects.

**Important:** When using volume replication on volumes that were created with a driver version lower than 1.7.0:

 1. Change the reclaim policy of the relevant PersistentVolumes to `Retain`.
 2. Delete the relevant PersistentVolumes.
 3. Import the volumes, by using the latest import procedure (version 1.7.0 or later) (see **CSI driver configuration** > **Advanced configuration** > **Importing an existing volume** in the user information).
      
    For more information, see the [Change the Reclaim Policy of a PersistentVolume](https://kubernetes.io/docs/tasks/administer-cluster/change-pv-reclaim-policy/) information in the Kubernetes documentation.

## High availability (HA) limitations
**Note:**
- HyperSwap topology is only supported for use with IBM Spectrum Virtualize family storage systems.
- Stretched topology is only supported by SAN Volume Controller storage systems.

### HyperSwap volume limitations
The following IBM block storage CSI driver features are not supported on volumes where HyperSwap is used:

- A HyperSwap volume cannot be created from a snapshot.

    **Note:** A snapshot can be created from a HyperSwap volume.
 - Volume cloning.

### Stretched volume limitations
 When conducting volume cloning, both volumes must use stretched topology.

## NVMe®/FC usage limitations

 Red Hat® Enterprise Linux CoreOS (RHCOS) does not support NVMe®/FC.
 
 For other limitations with your storage system, see the following section within your Spectrum Virtualize product documentation on [IBM Documentation](https://www.ibm.com/docs/en/): **Configuring** > **Host attachment** > **NVMe over Fibre Channel host attachments** > **FC-NVMe limitations and SAN configuration guidelines**.
 
 ## Volume attach limitations

 In cases where a volume cleanup consistently fails, eventually the orchestrator incorrectly resorts to detaching the volume. As a result, subsequent volume attachment disruptions may occur on the worker node.
 
 This limitation is tracked in the following places:

- Red Hat Bug [2022328](https://bugzilla.redhat.com/show_bug.cgi?id=2022328)
- Kubernetes Issue [106710](https://github.com/kubernetes/kubernetes/issues/106710)

## I/O group limitations

I/O group configuration is only supported for use with IBM Spectrum Virtualize family storage systems.

## Volume group limitations

Volume group configuration is only supported for use with IBM Spectrum Virtualize family storage systems.

## Snapshot function limitations

**Important:** Snapshot function support is only Alpha support.

- Snapshot function is only supported for use with IBM Spectrum Virtualize family storage system versions 8.5.1 or higher. For more information, see **Product overview** > **Technical overview** > **Volume groups** > **Snapshot function** within your Spectrum Virtualize product documentation on [IBM Documentation](https://www.ibm.com/docs).
- Both source and target PVCs (in a source PVC to snapshot to target PVC scenario) must have the same space efficiency set within their storage classes. If the space efficiency is set differently, the target PVC creation fails.
- A PVC target must have the same volume size as the source volume.
- A snapshot that uses the Snapshot function cannot be created with space efficiency set. If the VolumeSnapshotClass has the `SpaceEfficiency` parameter set along with the snapshot flag (`virt_snap_func`) enabled, the snapshot creation fails.
- In very rare cases, there can be leftover or undeleted volumes.
- A snapshot that uses the Snapshot function must be created within the same pool or child pool as the original PVC.
- Snapshots do not have unique identifiers (UIDs). As a result, if a snapshot with Snapshot function is deleted another snapshot can get the same ID number.
- Any object that is linked in any way (for example, a clone or a snapshot) must have the same definition of snapshot support. For example, a clone cannot be created with `virt_snap_func` disabled (indicating FlashCopy mapping is enabled) from a PVC with an existing Snapshot function connection.

    **Note:** FlashCopy mapping (`fcmap`) and Snapshot function cannot be used together on the same volume. However, they can be used on different volumes within the same storage system.