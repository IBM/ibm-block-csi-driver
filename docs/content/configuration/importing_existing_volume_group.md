
{{site.data.keyword.attribute-definition-list}}

# Importing an existing volume group

Use this information to import volume groups that were created externally from the IBM® block storage CSI driver, by using a VolumeGroupContent YAML file.

Read these important notices before importing a volume group. If these conditions are not met existing volumes within the volume group are deleted.{: attention}

1. Before you begin importing existing volume groups from the storage system, be sure to import any existing volumes that belong to the volume groups that will be imported (see [Importing an existing volume](importing_existing_volume.md)). When importing volumes, add the relevant labels to ensure that the volume is pointing to the volume group (see [Creating a PVC within a volume group with the dynamic volume group feature](creating_pvc.md#creating-a-pvc-within-a-volume-group-with-the-dynamic-volume-group-feature)).

2. Volume groups can only be managed by **either** the associated VolumeGroup **or** the associated StorageClass (with the `volume_group` parameter). If a volume group is imported and a StorageClass is already associated with it, then each volume of this StorageClass can be automatically deleted after the import.

Before starting to import an existing volume group, find the `volumeGroupHandle` in the existing volume group in order to include the information in the VolumeGroupContent YAML file. 

The `volumeGroupHandle` is formatted as `SVC:id;name`.

Through the IBM Storage Virtualize command-line, find both the `id` and `name` attributes, by using the `lsvolumegroup` command.

For more information, see **Command-line interface** > **Volume commands** > **lsvolumegroup** within your specific product documentation on [IBM Documentation](https://www.ibm.com/docs/). The volume group name can also be found through the management GUI. Go to **Volumes** > **Volume Groups** from the side bar.{: tip}
  
Use this procedure to help build a VolumeGroupContent YAML file for your volume groups.

1. Create a VolumeGroupContent YAML file.

   Update the volumeGroupHandle according to the volume group information found previously.
   
    apiVersion: csi.ibm.com/v1
    kind: VolumeGroupContent
    metadata:
      name: demo-volumegroupcontent
    spec:
      source:
        driver: block.csi.ibm.com
        volumeGroupHandle: SVC:id;name

Be sure to include the `volumeGroupHandle` parameter or errors may occur.{: attention}

2. Create a VolumeGroup YAML file.

    apiVersion: csi.ibm.com/v1
    kind: VolumeGroup
    metadata:
      name: demo-volumegroup-from-content
    spec:
      volumeGroupClassName: demo-volumegroupclass
      source:
        volumeGroupContentName: demo-volumegroupcontent
        selector: 
          matchLabels:
            demo-volumegroup-key: demo-volumegroup-value

Be sure to include the `volumeGroupClassName`. For more information about creating a VolumeGroup YAML file, see [Creating a VolumeGroup](creating_volumegroup.md).{: important}
