
{{site.data.keyword.attribute-definition-list}}

# Limitations

As opposed to known issues, limitations are functionality restrictions that are part of the predefined system design and capabilities in a particular version.

## Custom configuration limitations

Any configuration changes are reverted upon each IBM® Block Storage CSI driver upgrade. If configuration changes are required, it is recommended to set InstallPlans to manual upgrade only.

Consider [configuring dynamic host definition labels](../using/using_hostdefinition_labels.md) when possible to preserve HostDefiner customizations during IBM® Block Storage CSI driver upgrades.{: tip}

## IBM DS8000 usage limitations

When using the CSI driver with IBM DS8000® family storage systems:
- Connectivity limits on the storage side might be reached with IBM DS8000® family products due to too many open connections. This occurs due to connection closing lag times from the storage side.
- There is a limit of 11 FlashCopy relationships per volume (including all snapshots and clones).
- HostDefiner is not supported.

## IBM Storage Virtualize family usage limitations

When using the CSI driver with IBM Storage Virtualize® family storage systems:
- CSI will only allow up to 511 LUN connections to each storage system (even if multiple StorageClass objects are defined to different pools on the same storage system)

Check your operating system LUN limit.{: tip}

## High availability (HA) general limitations

Some storage system types do not support High availability (HA).{: attention}

- HyperSwap topology is only supported for use with IBM Storage Virtualize® family storage systems.
- Stretched topology is only supported by SAN Volume Controller storage systems.

## HyperSwap volume limitations
The following IBM block storage CSI driver features are not supported on volumes where HyperSwap is used:

- Volume cloning.
- A HyperSwap volume cannot be created from a snapshot.

A snapshot can be created from a HyperSwap volume. {: note}

## Stretched volume limitations
When conducting volume cloning, both volumes must use stretched topology.

## I/O group limitations

I/O group configuration is only supported for use with IBM Storage Virtualize® family storage systems.

## NVMe®/FC usage limitations

As of this document's publication date, NVMe/FC is not supported for this release.{: restriction}

For other limitations with your storage system, see the following section within your IBM Storage Virtualize® product documentation on [IBM Documentation](https://www.ibm.com/docs/en/): **Configuring** > **Host attachment** > **NVMe over Fibre Channel host attachments** > **FC-NVMe limitations and SAN configuration guidelines**.

## Policy-based replication limitations
Policy-based replication is only supported for use with IBM Storage Virtualize® family storage system versions 8.5.2 or higher. To see if your specific product is supported and for more information, see **What's new** > **Getting started with policy-based replication** within your IBM Storage Virtualize® product documentation on [IBM Documentation](https://www.ibm.com/docs).

For other policy-based replication limitations with your storage system, see the Configuration Limits and Restrictions for your product software version. From the [IBM Support](https://www.ibm.com/mysupport) website, search for `Configuration Limits and Restrictions` and your product name. For example, `Configuration Limits and Restrictions IBM Storage FlashSystem 9500`.

## Snapshot function limitations

- Snapshot function is only supported for use with IBM Storage Virtualize® family storage system versions 8.5.1 or higher. For more information, see **Product overview** > **Technical overview** > **Volume groups** > **Snapshot function** within your IBM Storage Virtualize product documentation on [IBM Documentation](https://www.ibm.com/docs).
- In very rare cases, due to a race condition, a different snapshot than intended may be mistakenly deleted during a snapshot deletion. This occurs as no snapshot unique ID (UID) is present on the storage side.
- Both source and target PVCs (in a source PVC to snapshot to target PVC scenario) must have the same space efficiency set within their storage classes. If the space efficiency is set differently, the target PVC creation fails.
- A PVC target must have the same volume size as the source volume.
- A snapshot that uses the Snapshot function cannot be created with space efficiency set. If the VolumeSnapshotClass has the `SpaceEfficiency` parameter set along with the snapshot flag (`virt_snap_func`) enabled, the snapshot creation fails.
- In very rare cases, there can be leftover or undeleted volumes. As a result of the Kubernetes/Openshift and CSI being stateless, in cases where the driver is not able to save a specific state, the CSI driver might administer the wrong process.

    For example, this can happen in a case where a volume is created from a snapshot but during the volume creation process a driver issue occurs. In such a case, the driver is not able to find the newly created volume and creates a new one. This results in both the initial volume that was created, but not found or linked by the snapshot, and the newly created volume.
- A snapshot that uses the Snapshot function must be created within the same pool or child pool as the original PVC.
- Any object that is linked in any way (for example, a clone or a snapshot) must have the same definition of snapshot support. For example, a clone cannot be created with `virt_snap_func` disabled (indicating FlashCopy mapping is enabled) from a PVC with an existing Snapshot function connection.

FlashCopy mapping (`fcmap`) and Snapshot function cannot be used together on the same volume. However, they can be used on different volumes within the same storage system. {: restriction} 

Check your IBM Storage Virtualize® product documentation on [IBM Documentation](https://www.ibm.com/docs) if your product supports the Snapshot function prior to enabling with the `virt_snap_func` parameter.{: important}

## Volume clone limitations

The following limitations apply when using volume clones with the IBM block storage CSI driver:

For high availability volume clone limitations, see [High availability (HA) limitations](#high-availability-ha-limitations). {: tip}

-   A PVC and its clone need to both have the same volume mode (**Filesystem** or **Block**).
-   When cloning a PersistentVolumeClaim (PVC), the clone cannot contain a smaller size than the source PVC.

The size of the volume can be expanded after the cloning process. {: tip}

## Volume expansion limitations

The following limitations apply when expanding volumes with the IBM block storage CSI driver:

-   When expanding a PVC while not in use by a pod, the volume size immediately increases on the storage side. However, the PVC size itself only increases after a pod uses the PVC.
-   When expanding a file system PVC for a volume that was previously formatted but is now no longer being used by a pod, any copy or replication operations performed on the PVC (such as snapshots or cloning) results in a copy with the newer, larger, size on the storage. However, its file system retains the original, smaller, size.
-   When using the CSI driver with IBM Storage Virtualize® family and IBM DS8000® family products, during size expansion of a PersistentVolumeClaim (PVC), the size remains until all snapshots of the specific PVC are deleted.

These limitations are not relevant when using the Snapshot the function. For more information, see [Snapshot function limitations](#snapshot-function-limitations). {: note}

## Volume group limitations

Volume group configuration is only supported for use with IBM Storage Virtualize® family storage systems.

The following limitations apply when using volume groups:

- PersistentVolumeClaims (PVCs) can only be defined inside volume groups in the following ways:
    - Defined within a StorageClass
    - Defined within a PVC, using the volume group key
- PVCs that already belong to a StorageClass with a defined volume group cannot be added to a VolumeGroup object.
- Volume groups must be empty in order to be deleted.

## Volume replication limitations

When a role switch is conducted, this is not reflected within the other orchestration platform replication objects.

When using volume replication on volumes that were created with a driver version lower than 1.7.0:
 1. Change the reclaim policy of the relevant PersistentVolumes to `Retain`.
 2. Delete the relevant PersistentVolumes.
 3. Import the volumes, by using the latest import procedure (version 1.7.0 or later) (see [Importing an existing volume](../configuration/importing_existing_volume.md)).
    For more information, see the [Change the Reclaim Policy of a PersistentVolume](https://kubernetes.io/docs/tasks/administer-cluster/change-pv-reclaim-policy/) information in the Kubernetes documentation.

## Volume snapshot limitations

The following limitations apply when using volume snapshots with the IBM block storage CSI driver:

- When using the CSI driver with IBM Storage® Virtualize family products, a snapshot can only be used to provision a new volume of equal size.
- When using the CSI driver with IBM DS8000® family storage systems, a snapshot is limited to creating 11 volumes.

These limitations are not relevant when using the Snapshot function. For more information, see [Snapshot function limitations](#snapshot-function-limitations). {: note}

