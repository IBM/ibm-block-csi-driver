
{{site.data.keyword.attribute-definition-list}}

# Deleting a VolumeGroup with a replication policy

When both Primary and Secondary volume groups are represented on a cluster, delete them in this specific order.

For each VolumeGroup Primary and Secondary pair to be deleted:
   1. Delete the Primary VolumeGroup.
   
   2. Delete the Secondary VolumeGroup.

After the Primary VolumeGroup has been deleted, the Secondary volume group is automatically deleted from the storage system.{: note}

