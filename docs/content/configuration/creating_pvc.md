# Creating a PersistentVolumeClaim (PVC)

Create a PersistentVolumeClaim (PVC) YAML file for a persistent volume (PV).

The IBM® block storage CSI driver supports using both file system and raw block volume modes.

**Important:**
  - If not defined, the default mode is `Filesystem`. Be sure to define the mode as `Block` if this configuration is preferred.
  - In all the examples - accessModes ReadWriteOnce is used, but ReadWriteMany can be used (instead or in addition) to allow multiple pod containers to access the mount - see details below
  - accessModes of existing PVCs should follow the procedure detailed below
  - The volume group labels are not pre-defined. Be sure to match the selector in the target volume group (`spec.source.selector`). For an example of creating a PVC using the VolumeGroup configuration, see [Creating a PVC within a volume group with the dynamic volume group feature](#creating-a-pvc-within-a-volume-group-with-the-dynamic-volume-group-feature).

**Note:** The examples below create the PVC with a storage size 1 Gb. This can be changed, per customer needs.

Use the sections below for creating YAML files for PVCs with file system and raw block volume modes. After each YAML file creation, use the `kubectl apply` command.

```
kubectl apply -f <filename>.yaml
```

The `persistentvolumeclaim/<pvc-name> created` message is emitted.

Use the following sections, according to your PVC needs:

- [Creating a PVC for volume with file system](#creating-a-pvc-for-volume-with-file-system)
- [Creating a PVC for raw block volume](#creating-a-pvc-for-raw-block-volume)
- [Creating a PVC within a volume group with the dynamic volume group feature](#creating-a-pvc-within-a-volume-group-with-the-dynamic-volume-group-feature)  
- [Creating a PVC from volume snapshot](#creating-a-pvc-from-volume-snapshot)
- [Creating a volume clone from an existing PVC](#creating-a-volume-clone-from-an-existing-pvc)
- [Creating a PVC that allows concurrent access from multiple pod containers](#creating-a-pvc-with-readwritemany)
- [Updating accessModes of existing PVC](#updating-access-modes)

## Creating a PVC for volume with file system

Create a PVC YAML file, similar to the following `demo-pvc-file-system.yaml` file, with the size of 1 Gb, with `volumeMode` defined as `Filesystem`.

**Note:** `volumeMode` is an optional field. `Filesystem` is the default if the value is not added.

    kind: PersistentVolumeClaim
    apiVersion: v1
    metadata:
      name: demo-pvc-file-system
    spec:
      volumeMode: Filesystem  # Optional. The default is Filesystem.
      accessModes:
      - ReadWriteOnce
      resources:
        requests:
          storage: 1Gi
      storageClassName: demo-storageclass

## Creating a PVC for raw block volume

Create a PVC YAML file, similar to the following `demo-pvc-raw-block.yaml` file, with the size of 1 Gb, with `volumeMode` defined as `Block`.

    kind: PersistentVolumeClaim
    apiVersion: v1
    metadata:
      name: demo-pvc-raw-block
    spec:
      volumeMode: Block
      accessModes:
      - ReadWriteOnce
      resources:
        requests:
          storage: 1Gi
      storageClassName: demo-storageclass

## Creating a PVC within a volume group with the dynamic volume group feature

Create a PVC YAML file similar to the following `demo-pvc-in-volume-group.yaml` file, changing the `volumeMode` as needed.

**Note:**  Be sure to match the selector in the target volume group (`spec.source.selector`). For more information, see [Creating a VolumeGroup](creating_volumegroup.md).

    kind: PersistentVolumeClaim
    apiVersion: v1
    metadata:
      name: demo-pvc-in-volume-group
      labels:
        demo-volumegroup-key: demo-volumegroup-value
    spec:
      volumeMode: Filesystem
      accessModes:
      - ReadWriteOnce
      resources:
        requests:
          storage: 1Gi
      storageClassName: demo-storageclass

## Creating a PVC from volume snapshot

To create a PVC from an existing volume snapshot, create a PVC YAML file, similar to the following `demo-pvc-from-snapshot.yaml` file, with the size of 1 Gb.

Update the `dataSource` parameters to reflect the existing volume snapshot information, where `kind` is `VolumeSnapshot`.

    kind: PersistentVolumeClaim
    apiVersion: v1
    metadata:
      name: demo-pvc-from-snapshot
    spec:
      volumeMode: Filesystem
      accessModes:
      - ReadWriteOnce
      resources:
        requests:
          storage: 1Gi
      storageClassName: demo-storageclass
      dataSource:
        name: demo-volumesnapshot
        kind: VolumeSnapshot
        apiGroup: snapshot.storage.k8s.io

## Creating a volume clone from an existing PVC

**Note:** This section refers to both the IBM FlashCopy® function and Snapshot function in Spectrum Virtualize storage systems.

To create a volume clone from an existing PVC object, create a PVC YAML file, similar to the following `demo-pvc-cloned-pvc.yaml` file, with the size of 1 Gb.

Update the `dataSource` parameters to reflect the existing PVC object information, where `kind` is `PersistentVolumeClaim`.

    kind: PersistentVolumeClaim
    apiVersion: v1
    metadata:
      name: demo-pvc-cloned-pvc
    spec:
      volumeMode: Filesystem
      accessModes:
      - ReadWriteOnce
      resources:
        requests:
          storage: 1Gi
      storageClassName: demo-storageclass
      dataSource:
        name: demo-pvc-file-system
        kind: PersistentVolumeClaim

## Creating a pvc with readwritemany

accessModes can be either ReadWriteOnce, ReadWriteMany, or both can be specified. The choice affects the constraint of multiple container access and mount permissions.
For information about accessModes - refer to the K8S documentation page https://kubernetes.io/docs/concepts/storage/persistent-volumes/

To use ReadWriteMany - replace ReadWriteOnce in the above examples:

    accessModes:
    - ReadWriteOnce

To specify both use:

     accessModes:
     - ReadWriteOnce
     - ReadWriteMany

If ReadWriteMany is specified (exclusively or with ReadWriteOnce) - K8S allows multiple pod containers to concurrently access the volume. If the application doesn't support multiple access - it is the user's responsibility to make sure that the volume is only access by a single pod container

If ReadWriteOnce is specified (exclusively or with ReadWriteMany) - volume ownership and permissions are modified to match the pod's security policy.

## Updating Access Modes

To update the accessModes of an existing PVC - follow these steps.
A template script can be downloaded from the git repository of the driver under
docs/content/configuration/rwo_to_rwx_demo.sh


Patch the PV to Retain policy (default is "delete")
Patch the PV to RWX
Delete the existing PVC associated with the PV
Patch the PV spec uid to blank
Create a new PVC with the new accessModes
Use the existing PV with this new PVC
