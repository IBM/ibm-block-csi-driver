# Creating a VolumeReplicationClass

Create a VolumeReplicationClass YAML file to enable volume replication.

**Note:** Remote copy function is referred to as the more generic volume replication within this documentation set. Not all supported products use the remote-copy function terminology.

In order to enable volume replication for your storage system, create a VolumeReplicationClass YAML file, similar to the following `demo-volumereplicationclass.yaml`.

When configuring the file, be sure to use the same array secret and array secret namespace as defined in [Creating a Secret](creating_secret.md).

Use the `system_id` of the storage system containing the `replicationHandle` volumes. For information on obtaining your storage system `system_id`, see [Finding a `system_id`](finding_systemid.md).

```
apiVersion: replication.storage.openshift.io/v1alpha1
kind: VolumeReplicationClass
metadata:
  name: demo-volumereplicationclass
spec:
  provisioner: block.csi.ibm.com
  parameters:
    system_id: demo-system-id
    copy_type: async  # Optional. Values sync/async. The default is sync.

    replication.storage.openshift.io/replication-secret-name: demo-secret
    replication.storage.openshift.io/replication-secret-namespace: default
```

After the YAML file is created, apply it by using the `kubectl apply -f` command.

```
kubectl apply -f <filename>.yaml
```
The `volumereplicationclass.replication.storage.openshift.io/<volumereplicationclass-name> created` message is emitted.