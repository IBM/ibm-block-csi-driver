# Limitations

As opposed to known issues, limitations are functionality restrictions that are part of the predefined system design and capabilities in a particular version.

## IBM® DS8000® usage limitations

Connectivity limits on the storage side might be reached with DS8000 Family products due to too many open connections. This occurs due to connection closing lag times from the storage side.

## Volume snapshot limitations

The following limitations apply when using volume snapshots with the IBM block storage CSI driver:

-   When deleting a PersistentVolumeClaim (PVC), the persistent volume (PV) remains until all snapshots of the specific PV are deleted.
-   When using the CSI (Container Storage Interface) driver with IBM Spectrum® Virtualize Family products, a snapshot can only be used to provision a new volume of equal size.

## Volume clone limitations

The following limitations apply when using volume clones with the IBM block storage CSI driver:

-   When cloning a PersistentVolumeClaim (PVC), the clone cannot contain a smaller size than the source PVC.

    **Note:** The size can be expanded after the cloning process.

-   A PVC and its clone need to both have the same volume mode (**Filesystem** or **Block**).

## Volume expansion limitations

The following limitations apply when expanding volumes with the IBM block storage CSI driver:

-   When using the CSI driver with IBM Spectrum Virtualize Family and IBM DS8000 Family products, during size expansion of a PersistentVolumeClaim (PVC), the size remains until all snapshots of the specific PVC are deleted.
-   When expanding a PVC while not in use by a pod, the volume size immediately increases on the storage side. However, PVC size only increases after a pod uses the PVC.
-   When expanding a filesystem PVC for a volume that was previously formatted but is now no longer being used by a pod, any copy or replication operations performed on the PVC (such as snapshots or cloning) results in a copy with the newer, larger, size on the storage. However, its filesystem has the original, smaller, size.

## Volume replication limitations

When a role switch is conducted, this is not reflected within the other orchestration platform replication objects.

**Important:** When using volume replication on volumes that were created with a driver version lower than 1.7.0:

 1. Change the reclaim policy of the relevant PersistentVolumes to `Retain`.
 2. Delete the relevant PersistentVolumes.
 3. Import the volumes, by using the latest import procedure (version 1.7.0 or later) (see [Importing an existing volume](../configuration/csi_ug_config_advanced_importvol.md) in the User Guide).
      
    For more information, see the [Change the Reclaim Policy of a PersistentVolume](https://kubernetes.io/docs/tasks/administer-cluster/change-pv-reclaim-policy/) information in the Kubernetes documentation.