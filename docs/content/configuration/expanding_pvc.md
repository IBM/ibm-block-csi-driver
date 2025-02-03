
{{site.data.keyword.attribute-definition-list}}

# Expanding a PersistentVolumeClaim (PVC)

Use this information to expand existing volumes.

Before expanding an existing volume, be sure that the relevant StorageClass `allowVolumeExpansion` parameter is set to true. For more information, see [Creating a StorageClass](creating_volumestorageclass.md).{: important}

To expand an existing volume, open the relevant PersistentVolumeClaim (PVC) YAML file and increase the `storage` parameter value. For example, if the current `storage` value is set to _1Gi_, you can change it to _10Gi_, as needed. For more information about PVC configuration, see [Creating a PersistentVolumeClaim (PVC)](creating_pvc.md).

Be sure to use the `kubectl apply` command in order to apply your changes.


