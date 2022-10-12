from abc import ABC, abstractmethod


class VolumeGroupInterface(ABC):

    @abstractmethod
    def create_volume_group(self, name):
        """
        This function should create a volume group in the storage system.

        Args:
            name      : name of the volume group to be created in the storage system

        Returns:
            VolumeGroup

        Raises:
            VolumeGroupAlreadyExists
            InvalidArgument
            PermissionDenied
        """
        raise NotImplementedError

    @abstractmethod
    def get_volume_group(self, volume_group_id):
        """
        This function return volume group info about the volume.

        Args:
            volume_group_id  : id of the volume group on storage system.

        Returns:
           VolumeGroup

        Raises:
            ObjectNotFound
            InvalidArgument
            PermissionDenied
        """
        raise NotImplementedError

    @abstractmethod
    def delete_volume_group(self, volume_group_id):
        """
        This function should delete a volume group in the storage system.

        Args:
            volume_group_id : id of the volume group to delete.

        Returns:
            None

        Raises:
            ObjectNotFound
            InvalidArgument
            PermissionDenied
            ObjectIsStillInUse
        """
        raise NotImplementedError

    def add_volume_to_volume_group(self, volume_group_id, volume_id):
        """
        This function should add a volume to a volume group in the storage system.

        Args:
            volume_group_id  : id of the volume group on storage system.
            volume_id  : id of the volume on storage system.

        Returns:
            None

        Raises:
            ObjectNotFound
            InvalidArgument
            PermissionDenied
            ObjectIsStillInUse
        """
        raise NotImplementedError

    def remove_volume_from_volume_group(self, volume_group_id, volume_id):
        """
        This function should remove a volume from a volume group in the storage system.

        Args:
            volume_group_id  : id of the volume group on storage system.
            volume_id  : id of the volume on storage system.

        Returns:
            None

        Raises:
            ObjectNotFound
            InvalidArgument
            PermissionDenied
            ObjectIsStillInUse
        """
        raise NotImplementedError
