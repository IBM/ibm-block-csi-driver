# Promoting a volume group
To promote a replicated volume group within the CSI driver, the VolumeReplication state must be promoted.

Promote the VolumeReplication state, by changing the `spec.replicationState` from `Secondary` to `Primary`. For more information, see [Creating a VolumeReplication](../configuration/creating_volumereplication.md).

## Promoting a replicated volume group
Use the following procedure to promote a replicated volume group:

1. Import the existing volume group. See [Importing an existing volume group](../configuration/importing_existing_volume_group.md).
<br><br>**Attention:** Be sure to import any existing volumes before importing the volume group.

2. Create and apply a new VolumeReplication YAML file for the volume group, with the  `spec.replicationState` parameter being `Primary`. See [Creating a VolumeReplication](../configuration/creating_volumereplication.md).
