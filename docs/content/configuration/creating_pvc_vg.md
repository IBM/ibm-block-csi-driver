
{{site.data.keyword.attribute-definition-list}}

# Creating a PersistentVolumeClaim (PVC) with volume groups

Create a PersistentVolumeClaim (PVC) YAML file for a persistent volume (PV).

For information and parameter definitions that are not related to topology awareness, be sure to see the information provided in [Creating a PersistentVolumeClaim (PVC)](creating_pvc.md), in addition to the current section.{: note}

Create a PVC YAML file similar to the following `demo-pvc-in-volume-group.yaml` file, changing the `volumeMode` as needed.

Be sure to match the selector in the target volume group (`spec.source.selector`). For more information, see [Creating a VolumeGroup](creating_volumegroup.md).{: important}

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

The example above creates a PVC with a storage size of 1 Gb. This can be changed as needed.{: note}

After each YAML file creation, use the `kubectl apply` command.

```
kubectl apply -f <filename>.yaml
```

The `persistentvolumeclaim/<pvc-name> created` message is emitted.
