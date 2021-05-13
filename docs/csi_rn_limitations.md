# Limitations

As opposed to known issues, limitations are functionality restrictions that are part of the predefined system design and capabilities in a particular version.

## IBM® DS8000® usage limitations

When using the CSI \(Container Storage Interface\) driver with DS8000 Family products, connectivity limit on the storage side may be reached because of too many open connections. This occurs due to connection closing lag times from the storage side.

## Volume snapshot limitations

The following limitations apply when using volume snapshots with the IBM block storage CSI driver:

-   When deleting a PersistentVolumeClaim \(PVC\), the persistent volume \(PV\) remains until all snapshots of the specific PV are deleted.
-   When using the CSI \(Container Storage Interface\) driver with IBM Spectrum® Virtualize Family products, a snapshot can only be used to provision a new volume of equal size.

## Volume clone limitations

The following limitations apply when using volume clones with the IBM block storage CSI driver:

-   When cloning a PersistentVolumeClaim \(PVC\), the clone cannot contain a smaller size than the source PVC.

    **Note:** The size can be expanded after the cloning process.

-   A PVC and its clone need to both have the same volume mode \(**Filesystem** or **Block**\).

## Volume expansion limitations

The following limitations apply when expanding volumes with the IBM block storage CSI driver:

-   When using the CSI driver with IBM Spectrum Virtualize Family and IBM DS8000 Family products, during size expansion of a PersistentVolumeClaim \(PVC\), the size remains until all snapshots of the specific PVC are deleted.
-   When expanding a PVC while not in use by a pod, the volume size immediately increases on the storage side. PVC size only increases, however, after a pod begins to use the PVC.
-   When expanding a filesystem PVC for a volume that was previously formatted but is now no longer being used by a pod, any copy or replication operations performed on the PVC \(such as snapshots or cloning, and so on\) results in a copy with the newer, larger, size on the storage. However, its filesystem has the original, smaller, size.