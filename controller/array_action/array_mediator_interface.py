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
    def create_volume(self, volume_name, size_in_bytes, space_efficiency, pool):
        """
        This function should create a volume in the storage system.

        Args:
            volume_name      : name of the volume to be created in the storage system
            size_in_bytes : size in bytes of the volume
            space_efficiency  : space efficiency (None for default)
            pool          : pool name to create the volume in

        Returns:
            volume_id : the volume WWN.

        Raises:
            VolumeAlreadyExists
            PoolDoesNotExist
            PoolDoesNotMatchSpaceEfficiency
            IllegalObjectName
            VolumeNameIsNotSupported
            PermissionDenied
        """
        raise NotImplementedError

    @abstractmethod
    def copy_to_existing_volume_from_source(self, name, source_name, source_capacity_in_bytes,
                                            minimum_volume_size_in_bytes, pool=None):
        """
        This function should create a volume from source volume or snapshot in the storage system.

        Args:
            name                            : name of the volume to be created in the storage system
            source_name                     : name of source to create from
            source_capacity_in_bytes        : capacity of source to create from
            minimum_volume_size_in_bytes    : if source capacity is lower than this value volume will
                                            be increased to this value
            pool                            : pool of the volume and source object to find them more efficiently.

        Returns:
            Volume

        Raises:
            ObjectNotFoundError
            IllegalObjectName
            PermissionDenied
            PoolParameterIsMissing
        """
        raise NotImplementedError

    @abstractmethod
    def delete_volume(self, volume_id):
        """
        This function should delete a volume in the storage system.

        Args:
            volume_id : wwn of the volume to delete

        Returns:
            None

        Raises:
            ObjectNotFound
            IllegalObjectID
            PermissionDenied
            ObjectIsStillInUse
        """
        raise NotImplementedError

    @abstractmethod
    def get_volume(self, volume_name, pool=None):
        """
        This function return volume info about the volume.

        Args:
            volume_name: name of the volume on storage system.
            pool: pool of the volume to find the volume more efficiently.


        Returns:
           Volume

        Raises:
            ObjectNotFound
            IllegalObjectName
            PermissionDenied
            PoolParameterIsMissing
        """
        raise NotImplementedError

    @abstractmethod
    def expand_volume(self, volume_id, required_bytes):
        """
        Expand the volume size on storage.
        Args:
           volume_id        : volume id
           required_bytes   : capacity of the volume after expansion
        Returns:
           None
        Raises:
            ObjectNotFound
            IllegalObjectID
            ObjectIsStillInUse
            NotEnoughSpaceInPool
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
            ObjectNotFound
            IllegalObjectID
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
            ObjectNotFound
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
            ObjectNotFound
            HostNotFound
            PermissionDenied
            UnMappingError
        """
        raise NotImplementedError

    @abstractmethod
    def get_snapshot(self, volume_id, snapshot_name, pool=None):
        """
        This function return snapshot info about the snapshot.
        Args:
            volume_id : id of the source volume (used to get pool in case pool parameter not given)
            snapshot_name : name of the snapshot in the storage system
            pool: pool to find the snapshot in (if not given, pool taken from source volume)
        Returns:
           Snapshot
        Raises:
            ExpectedSnapshotButFoundVolumeError
            IllegalObjectName
            IllegalObjectID
            PermissionDenied
        """
        raise NotImplementedError

    @abstractmethod
    def get_object_by_id(self, object_id, object_type):
        """
        This function return info about volume or snapshot.
        Args:
            object_id   : id of the object in the storage system
            object_type : volume or snapshot
        Returns:
           Snapshot
           Volume
           None
        Raises:
            ExpectedSnapshotButFoundVolumeError
        """
        raise NotImplementedError

    @abstractmethod
    def create_snapshot(self, volume_id, snapshot_name, pool=None):
        """
        This function should create a snapshot from volume in the storage system.
        Args:
            volume_id : id of the volume to be created from
            snapshot_name : name of the snapshot to be created in the storage system
            pool : pool to create the snapshot in (if not given, pool taken from source volume)
        Returns:
            Snapshot
        Raises:
            SnapshotAlreadyExists
            ObjectNotFound
            IllegalObjectName
            IllegalObjectID
            PermissionDenied
            NotEnoughSpaceInPool
            SnapshotSourcePoolMismatch
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
            ObjectNotFound
            IllegalObjectID
            PermissionDenied
            ObjectIsStillInUse
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
    def validate_supported_space_efficiency(self, space_efficiency):
        """
        This function will check if the space efficiency passed to the create volume is valid

        Args:
           space_efficiency : as passed from the storage class

        Returns:
            None

        Raises:
            SpaceEfficiencyNotSupported
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
    def max_object_name_length(self):
        """
        The max allowed volume or snapshot name length
        """
        raise NotImplementedError

    @property
    @abstractmethod
    def max_object_prefix_length(self):
        """
        The max allowed length of a volume or snapshot name prefix.
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
    def maximal_volume_size_in_bytes(self):
        raise NotImplementedError

    @property
    @abstractmethod
    def max_lun_retries(self):
        """
            The maximum number of times a map operation will retry if lun is already in use
        """
        raise NotImplementedError

    @property
    @abstractmethod
    def default_object_prefix(self):
        """
            The default prefix for object names
        """
        raise NotImplementedError
