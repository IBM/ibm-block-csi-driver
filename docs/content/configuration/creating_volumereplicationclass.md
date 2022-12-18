# Creating a VolumeReplicationClass

Create a VolumeReplicationClass YAML file to enable volume replication.

**Note:** Remote copy function is referred to as the more generic volume replication within this documentation set. Not all supported products use the remote-copy function terminology.

In order to enable volume replication for your storage system, create a VolumeReplicationClass YAML file, similar to the following `demo-volumereplicationclass.yaml`.

When configuring the file, be sure to use the same array secret and array secret namespace as defined in [Creating a Secret](creating_secret.md).

If using policy-based replication, use the `replication_policy` parameter, with the `replication_policy_name` value, instead of `system_id`. For information on obtaining your volume `replication_policy_name`, see [Finding the `replication_policy_name`](finding_replication_policy_name.md).

If policy-based replication is not in use, use the `system_id` of the storage system containing the `replicationHandle` volumes. For information on obtaining your storage system `system_id`, see [Finding a `system_id`](finding_systemid.md).

**Important:** Be sure to only use one of the following parameters: `replication_policy` **or** `system_id`. Using both parameters within the VolumeReplicationClass, results in the following error message: `got an invalid parameter: system_id`.

Use one of the following examples, depending on the replication type that is being used.

**Example 1: VolumeReplicationClass _not_ using Spectrum Virtualize policy-based replication**

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
**Example 2: VolumeReplicationClass using Spectrum Virtualize policy-based replication**

```
apiVersion: replication.storage.openshift.io/v1alpha1
kind: VolumeReplicationClass
metadata:
  name: demo-volumereplicationclass
spec:
  provisioner: block.csi.ibm.com
  parameters:
    replication_policy: demo_replication-policy-name
    replication.storage.openshift.io/replication-secret-name: demo-secret
    replication.storage.openshift.io/replication-secret-namespace: default
```

After the YAML file is created, apply it by using the `kubectl apply -f` command.

```
kubectl apply -f <filename>.yaml
```
The `volumereplicationclass.replication.storage.openshift.io/<volumereplicationclass-name> created` message is emitted.