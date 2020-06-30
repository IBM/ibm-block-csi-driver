from abc import ABC, abstractmethod


class ArrayMediator(ABC):

    @abstractmethod
    def __init__(self, user, password, address):
        """
        This is the init function for the class.
        it should establish the connection to the storage system.

        Args:
            user     : user name for connecting to the endpoint
            password : password for connecting to the endpoint
            endpoint : storage array fqdn or ip

        Raises:
            CredentialsError
        """
        raise NotImplementedError

    @abstractmethod
    def disconnect(self):
        """
        This function disconnect the storage system connection that was opened in the init phase.

        Returns:
            None
        """
        raise NotImplementedError

    @abstractmethod
    def create_volume(self, vol_name, size_in_bytes, capabilities, pool, volume_prefix=""):
        """
        This function should create a volume in the storage system.

        Args:
            vol_name      : name of the volume to be created in the stoarge system
            size_in_bytes : size in bytes of the volume
            capabilities  : dict of capabilities {<capbility_name>:<value>}
            pool          : pool name to create the volume in
            volume_prefix : name prefix of the volume

        Returns:
            volume_id : the volume WWN.

        Raises:
            VolumeAlreadyExists
            PoolDoesNotExist
            PoolDoesNotMatchCapabilities
            IllegalObjectName
            VolumeNameIsNotSupported
            PermissionDenied
        """
        raise NotImplementedError

    @abstractmethod
    def copy_to_existing_volume_from_snapshot(self, name, src_snap_name, src_snap_capacity_in_bytes,
                                              min_vol_size_in_bytes, pool=None):
        """
        This function should create a volume from snapshot in the storage system.

        Args:
            name                         : name of the volume to be created in the storage system
            src_snap_name                : name of snapshot to create from
            src_snap_capacity_in_bytes   : capacity of snapshot to create from
            min_vol_size_in_bytes        : if snapshot capacity is lower than this value vol will
                                           be increased to this value
            pool: pool of the volume and snapshot to find them more efficiently.

        Returns:
            Volume

        Raises:
            VolumeNotFoundError
            SnapshotNotFoundError
            IllegalObjectName
            PermissionDenied
        """
        raise NotImplementedError

    @abstractmethod
    def delete_volume(self, volume_id):
        """
        This function should delete a volume in the storage system.

        Args:
            vol_id : wwn of the volume to delete

        Returns:
            None

        Raises:
            VolumeNotFound
            PermissionDenied
        """
        raise NotImplementedError

    @abstractmethod
    def get_volume(self, volume_name, volume_context=None, volume_prefix=""):
        """
        This function return volume info about the volume.

        Args:
            volume_name: name of the volume on storage system.
            volume_context: context of the volume to find the volume more efficiently.
            volume_prefix : name prefix of the volume


        Returns:
           Volume

        Raises:
            VolumeNotFound
            IllegalObjectName
            PermissionDenied
        """
        raise NotImplementedError

    @abstractmethod
    def get_volume_name(self, volume_id):
        """
        This function return volume name.
        Args:
           volume_id : volume id
        Returns:
           volume name
        Raises:
            VolumeNotFound
            IllegalObjectID
        """
        raise NotImplementedError

    @abstractmethod
    def is_volume_has_snapshots(self, volume_id):
        """
        Args:
           volume_id : volume id
        Returns:
           Is volume has snapshots
        Raises:
            VolumeNotFound
            IllegalObjectID
        """
        raise NotImplementedError

    @abstractmethod
    def get_volume_mappings(self, volume_id):
        """
        This function return volume mappings.

        Args:
           volume_id : the volume WWN.

        Returns:
           mapped_host_luns : a dict like this: {<host name>:<lun id>,...}

        Raises:
            VolumeNotFound
        """
        raise NotImplementedError

    @abstractmethod
    def map_volume(self, volume_id, host_name):
        """
        This function will find the next available lun for the host and map the volume to it.

        Args:
           volume_id : the volume WWN.
           host_name : the name of the host to map the volume to.

        Returns:
           lun : the lun_id the volume was mapped to.

        Raises:
            NoAvailableLun
            LunAlreadyInUse
            VolumeNotFound
            HostNotFound
            PermissionDenied
            MappingError
        """
        raise NotImplementedError

    @abstractmethod
    def unmap_volume(self, volume_id, host_name):
        """
        This function will un-map the volume from the host.

        Args:
           volume_id : the volume WWN.
           host_name : the name of the host to un-map the volume from.

        Returns:
           None

        Raises:
            VolumeAlreadyUnmapped
            VolumeNotFound
            HostNotFound
            PermissionDenied
            UnMappingError
        """
        raise NotImplementedError

    @abstractmethod
    def get_snapshot(self, snapshot_name, volume_context=None):
        """
        This function return snapshot info about the snapshot.
        Args:
            snapshot_name : name of the snapshot in the storage system
            volume_context: context of the volume to find the snapshot more efficiently.
        Returns:
           Snapshot
        Raises:
            SnapshotNameBelongsToVolumeError
            IllegalObjectName
            PermissionDenied
        """
        raise NotImplementedError

    @abstractmethod
    def get_snapshot_by_id(self, snapshot_id):
        """
        This function return snapshot info about the snapshot.
        Args:
            snapshot_id : id of the snapshot in the storage system
        Returns:
           Snapshot
        Raises:
            SnapshotIdBelongsToVolumeError
        """
        raise NotImplementedError

    @abstractmethod
    def create_snapshot(self, name, volume_name, volume_context=None):
        """
        This function should create a snapshot from volume in the storage system.
        Args:
            name           : name of the snapshot to be created in the storage system
            volume_name    : name of the volume to be created from
            volume_context: context of the volume to find the snapshot more efficiently.
        Returns:
            Snapshot
        Raises:
            SnapshotAlreadyExists
            VolumeNotFound
            IllegalObjectName
            PermissionDenied
        """
        raise NotImplementedError

    @abstractmethod
    def delete_snapshot(self, snapshot_id):
        """
        This function should delete a snapshot in the storage system.
        Args:
            snapshot_id : wwn of the snapshot to delete
        Returns:
            None
        Raises:
            SnapshotNotFound
            PermissionDenied
        """
        raise NotImplementedError

    @abstractmethod
    def get_iscsi_targets_by_iqn(self):
        """
        This function will return a mapping of the storage array iscsi names to their iscsi target IPs

        Args:
            None

        Returns:
            ips_by_iqn : A dict mapping array-iqns to their list of IPs ({iqn1:[ip1, ip2], iqn2:[ip3, ip4, ...], ...})

        Raises:
            PermissionDeniedError
            NoIscsiTargetsFoundError
        """
        raise NotImplementedError

    @abstractmethod
    def get_array_fc_wwns(self, host_name=None):
        """
        This function will return the wwn of the connected
        FC port of the storage array

        Args:
            None

        Returns:
            wwn : the wwn of the storage

        Raises:
            None
        """
        raise NotImplementedError

    @abstractmethod
    def get_host_by_host_identifiers(self, initiators):
        """
        This function will find the host name by iscsi iqn or fc wwns.

        Args:
           initiators : initiators (e.g. fc wwns, iqn) of the wanted host.

        Returns:
           connectivity_types : list of connectivity types ([iscis, fc] or just [iscsi],..)
           hostname           : the name of the host

        Raises:
            HostNotFound
            multipleHostsFoundError
            PermissionDenied
        """
        raise NotImplementedError

    @abstractmethod
    def is_active(self):
        """
        This function will return True if the storage connection is still active.

        """
        raise NotImplementedError

    @abstractmethod
    def validate_supported_capabilities(self, capabilities):
        """
        This function will check if the capabilities passed to the create volume are valid

        Args:
           capabilities : as passed from the storage class

        Returns:
            None

        Raises:
            CapabilityNotSupported
        """
        raise NotImplementedError

    @property
    @abstractmethod
    def identifier(self):
        """
        The storage system identifier.
        """
        raise NotImplementedError

    @property
    @abstractmethod
    def array_type(self):
        """
        The storage system type.
        """
        raise NotImplementedError

    @property
    @abstractmethod
    def port(self):
        """
        The storage system management port number.
        """
        raise NotImplementedError

    @property
    @abstractmethod
    def max_volume_name_length(self):
        """
        The max allowed volume name length
        """
        raise NotImplementedError

    @property
    @abstractmethod
    def max_volume_prefix_length(self):
        """
        The max allowed length of a volume name prefix.
        """
        raise NotImplementedError

    @property
    @abstractmethod
    def max_snapshot_name_length(self):
        """
        The max allowed snapshot name length
        """
        raise NotImplementedError

    @property
    @abstractmethod
    def max_snapshot_prefix_length(self):
        """
        The max allowed length of a snapshot name prefix.
        """
        raise NotImplementedError

    @property
    @abstractmethod
    def max_connections(self):
        """
        The max number of concurrent connections to the storage system.
        """
        raise NotImplementedError

    @property
    @abstractmethod
    def minimal_volume_size_in_bytes(self):
        """
        The minimal volume size in bytes (used in case trying to provision volume with zero size).
        """
        raise NotImplementedError

    @property
    @abstractmethod
    def max_lun_retries(self):
        """
            The maximum number of times a map operation will retry if lun is already in use
        """
        raise NotImplementedError
