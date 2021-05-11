# Sample configurations for running a stateful container

You can use the CSI \(Container Storage Interface\) driver for running stateful containers with a storage volume provisioned from IBM® block storage systems.

These examples illustrate a basic configuration required for running a stateful container with volumes provisioned on an IBM Spectrum® Virtualize Family storage system.

While these examples specify the use of IBM Spectrum Virtualize products, the same configuration is used on all supported storage system types.

**Note:** The secret names given can be user specified. When giving secret names when managing different system storage types, be sure to give system type indicators to each name.

The following are examples of different types of secret names that can be given per storage type.

|Storage system name|Secret name|
|-------------------|-----------|
|IBM FlashSystem® A9000<br />IBM FlashSystem A9000R|a9000-array1|
|IBM Spectrum Virtualize Family including IBM SAN Volume Controller and<br />IBM FlashSystem family members built with IBM Spectrum Virtualize<br />(including FlashSystem 5xxx, 7200, 9100, 9200, 9200R\)|storwize-array1|
|IBM DS8000® Family products|DS8000-array1|

**Note:** This procedure is applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.

Use this information to run a stateful container on StatefulSet volumes using either file systems or raw block volumes.

1.  Create an array secret, as described in [Creating a Secret](csi_ug_config_create_secret.md).

2.  Create a storage class, as described in [Creating a StorageClass](csi_ug_config_create_storageclasses.md).

    **Remember:** The `SpaceEfficiency` values for Spectrum Virtualize Family are: thick, thin, compressed, or deduplicated. These values are not case specific.<br />For DS8000 Family systems, the default value is standard, but can be set to thin, if required. These values are not case specific. For more information, see [Creating a StorageClass](csi_ug_config_create_storageclasses.md).<br />This parameter is not applicable for IBM FlashSystem A9000 and A9000R systems. These systems always include deduplication and compression.

3.  Create a PVC with the size of 1 Gb, as described in [Creating a PersistentVolumeClaim \(PVC\)](csi_ug_config_create_pvc.md).

4.  Display the existing PVC and the created persistent volume \(PV\).

5.  Create a StatefulSet, as described in [Creating a StatefulSet](csi_ug_config_create_statefulset.md).


