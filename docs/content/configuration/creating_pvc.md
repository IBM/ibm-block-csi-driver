# Creating a PersistentVolumeClaim (PVC)

Create a PersistentVolumeClaim (PVC) YAML file for a persistent volume (PV).

The IBM® block storage CSI driver supports using both file system and raw block volume modes.

**Important:** If not defined, the default mode is `Filesystem`. Be sure to define the mode as `Block` if this configuration is preferred.

**Note:** The examples below create the PVC with a storage size 1 Gb. This can be changed, per customer needs.

Use the sections below for creating YAML files for PVCs with file system and raw block volume modes. After each YAML file creation, use the `kubectl apply` command.

```
kubectl apply -f <filename>.yaml
```

The `persistentvolumeclaim/<pvc-name> created` message is emitted.

Use the following sections, according to your PVC needs:

- [Creating a PVC for volume with file system](#creating-a-pvc-for-volume-with-file-system)
- [Creating a PVC for raw block volume](#creating-a-pvc-for-raw-block-volume)
- [Creating a PVC from volume snapshot](#creating-a-pvc-from-volume-snapshot)
- [Creating a volume clone from an existing PVC](#creating-a-volume-clone-from-an-existing-pvc)

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