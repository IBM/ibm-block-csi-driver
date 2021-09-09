# Creating a VolumeReplicationClass

Create a VolumeReplicationClass YAML file to enable volume replication.

**Note:** Replication is referred to as the more generic volume replication within this documentation set. Spectrum Virtualize products refer to this feature as the remote copy function.

In order to enable volume replication for your storage system, create a VolumeReplicationClass YAML file, similar to the following `demo-volumereplicationclass.yaml`.

When configuring the file, be sure to use the same array secret and array secret namespace as defined in [Creating a Secret](csi_ug_config_create_secret.md).

```
apiVersion: replication.storage.openshift.io/v1alpha1
kind: VolumeReplicationClass
metadata:
  name: demo-volumereplicationclass
spec:
  provisioner: block.csi.ibm.com
  parameters:
    system_id: "0000000000DEM01D"
    copy_type: "async"  # Optional. Values sync\async. The default is sync.
    replication.storage.openshift.io/replication-secret-name: demo-secret
    replication.storage.openshift.io/replication-secret-namespace: default
```

After the YAML file is created, apply it by using the `kubectl apply -f` command.

```
kubectl apply -f <filename>.yaml
```