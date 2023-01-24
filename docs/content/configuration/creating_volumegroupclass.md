# Creating a VolumeGroupClass

Create a VolumeGroupClass YAML file to enable volume groups.

Volume groups allow users to create PersistentVolumeClaim (PVC) groups. PVCs can be dynamically managed through the volume groups created. This allows actions across all PVCs within the same volume group at once.

Volume groups are used with policy-based replication, allowing all PVCs within a single volume group to be replicated at once. For more information about volume groups and policy-based replication, see the following sections within your Spectrum Virtualize product documentation [IBM Documentation](https://www.ibm.com/docs).

- **Product overview** > **Technical overview** > **Volume groups**
- **What's new** > **Getting started with policy-based replication**

In order to enable volume groups for your storage system, create a VolumeGroupClass YAML file, similar to the following `demo-volumegroupclass.yaml`.

```
apiVersion: csi.ibm.com/v1
kind: VolumeGroupClass
metadata:
  name: demo-volumegroupclass
driver: block.csi.ibm.com
parameters:
  volume_group_name_prefix: demo-prefix

  volumegroup.storage.ibm.io/secret-name: demo-secret
  volumegroup.storage.ibm.io/secret-namespace: default
```

After the YAML file is created, apply it by using the `kubectl apply -f` command.

```
kubectl apply -f <filename>.yaml
```
The `volumegroupclass.csi.ibm.com/<volumegroupclass-name> created` message is emitted.