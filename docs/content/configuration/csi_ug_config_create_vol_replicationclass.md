# Creating a VolumeReplicationClass

Create a VolumeReplicationClass YAML file to enable volume replication.

**Note:** Replication is also known as mirroring in many storage systems.

In order to enable volume replication for your storage system, create a VolumeReplicationClass YAML file, similar to the following demo-replicationclass.yaml.

When configuring the file, be sure to use the same array secret and array secret namespace as defined in [Creating a Secret](csi_ug_config_create_secret.md).

-   The `snapshot_name_prefix` parameter is optional.

```
apiVersion: replication.storage.openshift.io/v1alpha1
kind: VolumeReplicationClass
metadata:
  name: volumereplicationclass-sample
spec:
  provisioner: example.provisioner.io
  parameters:
    replication.storage.openshift.io/replication-secret-name: secret-name
    replication.storage.openshift.io/replication-secret-namespace: secret-namespace
```

After the YAML file is created, apply it by using the `kubectl apply -f` command.

```
kubectl apply -f <filename>.yaml
```