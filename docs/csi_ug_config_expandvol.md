# Expanding a PersistentVolumeClaim \(PVC\)

Use this information to expand existing volumes.

**Important:** Before expanding an existing volume, be sure that the relevant StorageClass yaml `allowVolumeExpansion` parameter is set to true. For more information, see [Creating a StorageClass](csi_ug_config_create_storageclasses.md).

To expand an existing volume, open the relevant PersistentVolumeClaim \(PVC\) yaml file and increase the `storage` parameter value. For example, if the current `storage` value is set to _1Gi_, you can change it to _10Gi_, as needed. For more information about PVC configuration, see [Creating a PersistentVolumeClaim \(PVC\)](csi_ug_config_create_pvc.md).

Be sure to use the `kubectl apply` command in order to apply your changes.


