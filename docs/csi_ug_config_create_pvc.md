# Creating a PersistentVolumeClaim \(PVC\)

Create a PersistentVolumeClaim \(PVC\) yaml file for a persistent volume \(PV\).

The IBM® block storage CSI driver supports using both file system and raw block volume types.

**Important:** If not defined, the default type is `Filesystem`. Be sure to define the type as `Block` if this configuration is preferred.

**Note:** The examples below create the PVC with a storage size 1 Gb. This can be changed, per customer needs.

Use the sections below for creating yaml files for PVCs with file system and raw block volume types. After each yaml file creation, use the `kubectl apply` command.

```
kubectl apply -f <filename>.yaml
```

The `persistentvolumeclaim/<filename> created` message is emitted.

Use the following sections, according to your PVC needs:

-   [Creating PVC for volume with file system](#Creating-PVC-for-volume-with-Filesystem)
-   [Creating PVC for raw block volume](#Creating-PVC-for-raw-block-volume)
-   [Creating PVC from volume snapshot](#Creating-PVC-from-volume-snapshot)
-   [Creating a volume clone from an existing PVC](#Creating-a-volume-clone-from-an-existing-PVC)

## Creating PVC for volume with Filesystem

Create a PVC yaml file, similar to the following demo-pvc-file-system.yaml file, with the size of 1 Gb.

**Note:** `volumeMode` is an optional field. `Filesystem` is the default if the value is not added.

<pre>
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: demo-pvc-file-system
spec:
  volumeMode: <b>Filesystem</b>  # Optional. The default is Filesystem.
  accessModes:
  - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
  storageClassName: demo-storageclass
</pre>

## Creating PVC for raw block volume

Create a PVC yaml file, similar to the following demo-pvc-raw-block.yaml file, with the size of 1 Gb.

<pre>
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: demo-pvc-raw-block
spec:
  volumeMode: <b>Block</b>
  accessModes:
  - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
  storageClassName: demo-storageclass
</pre>

## Creating PVC from volume snapshot

To create a PVC from an existing volume snapshot, create a PVC yaml file, similar to the following demo-pvc-from-snapshot.yaml file, with the size of 1 Gb.

<pre>
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
  <b>dataSource:
    name: demo-snapshot
    kind: VolumeSnapshot
    apiGroup: snapshot.storage.k8s.io</b>
</pre>

## Creating a volume clone from an existing PVC

**Note:** IBM FlashCopy® function is referred to as the more generic volume snapshots and cloning within this documentation set. Not all supported products use the FlashCopy function terminology.

To create a volume clone from an existing PVC object, create a PVC yaml file, similar to the following demo-pvc-cloned-pvc.yaml file, with the size of 1 Gb.

<pre>
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
  <b>dataSource:
    name: demo-pvc-file-system
    kind: PersistentVolumeClaim</b>
</pre>

