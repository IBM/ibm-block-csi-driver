# Creating a PersistentVolumeClaim (PVC) with volume groups

Create a PersistentVolumeClaim (PVC) YAML file for a persistent volume (PV).

**Note:** For information and parameter definitions that are not related to topology awareness, be sure to see the information provided in [Creating a PersistentVolumeClaim (PVC)](creating_pvc.md), in addition to the current section.


**Note:** The examples below create the PVC with a storage size 1 Gb. This can be changed, per customer needs.

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

After each YAML file creation, use the `kubectl apply` command.

```
kubectl apply -f <filename>.yaml
```

The `persistentvolumeclaim/<pvc-name> created` message is emitted.
