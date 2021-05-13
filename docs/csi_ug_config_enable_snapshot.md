# Creating a VolumeSnapshotClass

Create a VolumeSnapshotClass YAML file to enable creation and deletion of volume snapshots.

**Note:**

-   IBM® FlashCopy® function is referred to as the more generic volume snapshots and cloning within this documentation set. Not all supported products use the FlashCopy function terminology.
-   For volume snapshot support, the minimum orchestration platform version requirements are Red Hat® OpenShift® 4.4 and Kubernetes 1.17.

In order to enable creation and deletion of volume snapshots for your storage system, create a VolumeSnapshotClass YAML file, similar to the following demo-snapshotclass.yaml.

When configuring the file, be sure to use the same array secret and array secret namespace as defined in [Creating a Secret](csi_ug_config_create_secret.md).

-   The `snapshot_name_prefix` parameter is optional.

    **Note:** For IBM DS8000® Family, the maximum prefix length is five characters.<br/>The maximum prefix length for other systems is 20 characters.<br/>For storage systems using Spectrum Virtualize, the `CSI_` prefix is added as default if not specified by the user.


```screen
apiVersion: snapshot.storage.k8s.io/v1beta1
kind: VolumeSnapshotClass
metadata:
  name: demo-snapshotclass
driver: block.csi.ibm.com
deletionPolicy: Delete
parameters:
  csi.storage.k8s.io/snapshotter-secret-name: demo-secret
  csi.storage.k8s.io/snapshotter-secret-namespace: default
  snapshot_name_prefix: demoSnapshot   # Optional.
  pool: demo-pool                      # Mandatory only for DS8000 Family.
```

After the YAML file is created, apply it by using the `kubectl apply -f` command.

```
kubectl apply -f <filename>.yaml
```

