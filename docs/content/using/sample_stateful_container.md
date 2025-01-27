
{{site.data.keyword.attribute-definition-list}}

# Sample configurations for running a stateful container

You can use the CSI (Container Storage Interface) driver for running stateful containers with a storage volume provisioned from IBM® block storage systems.

These instructions illustrate the general flow for a basic configuration required for running a stateful container with volumes provisioned on a storage system.

The secret names given are user specified. To implement order and help any debugging that may be required, provide system type indicators to each secret name when managing different system storage types.{: tip}

Use this information to run a stateful container on StatefulSet volumes using either file systems or raw block volumes.

1. Create an array secret, as described in [Creating a Secret](../configuration/creating_secret.md).

2. Create a storage class, as described in [Creating a StorageClass](../configuration/creating_volumestorageclass.md).

Use the `SpaceEfficiency` parameters available for your storage system, as specified in the [`SpaceEfficiency` parameter definitions per storage system type](../configuration/creating_volumestorageclass.md#spaceefficiency-parameter-definitions-per-storage-system-type) table.{: tip}

3. Create a PVC with the size of 1 Gb, as described in [Creating a PersistentVolumeClaim (PVC)](../configuration/creating_pvc.md).

4. (Optional) Display the existing PVC and the created persistent volume (PV).

5. Create a StatefulSet, as described in [Creating a StatefulSet](../configuration/creating_statefulset.md).
