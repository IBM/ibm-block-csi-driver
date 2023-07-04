from abc import ABC, abstractmethod


class ArrayMediator(ABC):

    @abstractmethod
    def __init__(self, user, password, endpoint):
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
    def create_volume(self, name, size_in_bytes, space_efficiency, pool, io_group, volume_group, source_ids,
                      source_type, is_virt_snap_func):
        """
        This function should create a volume in the storage system.

        Args:
            name              : name of the volume to be created in the storage system
            size_in_bytes     : size in bytes of the volume
            space_efficiency  : space efficiency (None for default)
            pool              : pool name to create the volume in
            io_group          : i/o group to create the volume in
            volume_group      : volume group to create the volume in
            source_ids        : ObjectIds of source to create from
            source_type       : volume or snapshot
            is_virt_snap_func : indicate if svc's snapshot function feature is enabled

        Returns:
            volume_id : the volume WWN.

        Raises:
            VolumeAlreadyExists : optional
            InvalidArgument
            PermissionDenied
        """
        raise NotImplementedError

    @abstractmethod
    def copy_to_existing_volume(self, volume_id, source_id, source_capacity_in_bytes,
                                minimum_volume_size_in_bytes):
        """
        This function should create a volume from source volume or snapshot in the storage system.

        Args:
            volume_id                       : id of the volume to be created in the storage system
            source_id                       : id of source to create from
            source_capacity_in_bytes        : capacity of source to create from
            minimum_volume_size_in_bytes    : if source capacity is lower than this value volume will
                                            be increased to this value

        Returns:
            Volume

        Raises:
            ObjectNotFoundError
            IllegalObjectID
            PermissionDenied
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
            InvalidArgument
            PermissionDenied
            ObjectIsStillInUse
        """
        raise NotImplementedError

    @abstractmethod
    def get_volume(self, name, pool, is_virt_snap_func, source_type):
        """
        This function return volume info about the volume.

        Args:
            name              : name of the volume on storage system.
            pool              : pool of the volume to find the volume more efficiently.
            is_virt_snap_func : indicate if svc's snapshot function feature is enabled
            source_type       : volume or snapshot


        Returns:
           Volume

        Raises:
            ObjectNotFound
            InvalidArgument
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
            InvalidArgument
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
            InvalidArgument
        """
        raise NotImplementedError

    @abstractmethod
    def map_volume(self, volume_id, host_name, connectivity_type):
        """
        This function will find the next available lun for the host and map the volume to it.

        Args:
           volume_id : the volume WWN.
           host_name : the name of the host to map the volume to.
           connectivity_type : connectivity type (e.g. iscsi, fc, etc...)

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
    def get_snapshot(self, volume_id, snapshot_name, pool, is_virt_snap_func):
        """
        This function return snapshot info about the snapshot.
        Args:
            volume_id         : id of the source volume (used to get pool in case pool parameter not given)
            snapshot_name     : name of the snapshot in the storage system
            pool              : pool to find the snapshot in (if not given, pool taken from source volume)
            is_virt_snap_func : indicate if svc's snapshot function feature is enabled
        Returns:
           Snapshot
        Raises:
            ExpectedSnapshotButFoundVolumeError
            InvalidArgument
            PermissionDenied
        """
        raise NotImplementedError

    @abstractmethod
    def get_object_by_id(self, object_id, object_type, is_virt_snap_func=False):
        """
        This function return info about volume or snapshot.
        Args:
            object_id         : id of the object in the storage system
            object_type       : volume or snapshot
            is_virt_snap_func : indicate if svc's snapshot function feature is enabled
        Returns:
           Snapshot
           Volume
           None
        Raises:
            ExpectedSnapshotButFoundVolumeError
        """
        raise NotImplementedError

    @abstractmethod
    def create_snapshot(self, volume_id, snapshot_name, space_efficiency, pool, is_virt_snap_func):
        """
        This function should create a snapshot from volume in the storage system.
        Args:
            volume_id           : id of the volume to be created from
            snapshot_name       : name of the snapshot to be created in the storage system
            space_efficiency    : space efficiency (if empty/None, space efficiency taken from source volume)
            pool                : pool to create the snapshot in (if empty/None, pool taken from source volume)
            is_virt_snap_func   : indicate if svc's snapshot function feature is enabled
        Returns:
            Snapshot
        Raises:
            ObjectNotFound
            InvalidArgument
            PermissionDenied
            NotEnoughSpaceInPool
            SnapshotSourcePoolMismatch
        """
        raise NotImplementedError

    @abstractmethod
    def delete_snapshot(self, snapshot_id, internal_snapshot_id):
        """
        This function should delete a snapshot in the storage system.
        Args:
            snapshot_id : wwn of the snapshot to delete
            internal_snapshot_id : storage internal snapshot id
        Returns:
            None
        Raises:
            ObjectNotFound
            InvalidArgument
            PermissionDenied
            ObjectIsStillInUse
        """
        raise NotImplementedError

    @abstractmethod
    def get_iscsi_targets_by_iqn(self, host_name):
        """
        This function will return a mapping of the storage array iscsi names to their iscsi target IPs

        Args:
            host_name : used to filter relevant hosts

        Returns:
            ips_by_iqn : A dict mapping array-iqns to their list of IPs ({iqn1:[ip1, ip2], iqn2:[ip3, ip4, ...], ...})

        Raises:
            PermissionDeniedError
            NoIscsiTargetsFoundError
            HostNotFoundError
        """
        raise NotImplementedError

    @abstractmethod
    def get_array_fc_wwns(self, host_name):
        """
        This function will return the wwn of the connected
        FC port of the storage array

        Args:
            host_name : used to filter relevant hosts

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
    def get_host_by_name(self, host_name):
        """
        This function will find the host by name.

        Args:
           host_name : name of the host in the storage system

        Returns:
            Host

        Raises:
            HostNotFoundError
        """
        raise NotImplementedError

    @abstractmethod
    def create_host(self, host_name, initiators, connectivity_type, io_group):
        """
        This function should create a host in the storage system.

        Args:
           host_name         : name of the host to be created in the storage system
           initiators        : initiators (e.g. fc wwns, iqn) of the host.
           connectivity_type : the connectivity_type chosen by the user


        Returns:
            None

        Raises:
            HostAlreadyExists
            NoPortIsValid
            IoGroupIsInValid
        """
        raise NotImplementedError

    @abstractmethod
    def delete_host(self, host_name):
        """
        This function should delete a host in the storage system.

        Args:
            host_name : name of the host in the storage system

        Returns:
            None

        Raises:
            None
        """
        raise NotImplementedError

    @abstractmethod
    def add_ports_to_host(self, host_name, initiators, connectivity_type):
        """
        This function should add ports to host in the storage system.

        Args:
           host_name         : name of the host to be created in the storage system
           initiators        : initiators (e.g. fc wwns, iqn) of the host.
           connectivity_type : the connectivity_type chosen by the user

        Returns:
            None

        Raises:
            NoPortIsValid
        """
        raise NotImplementedError

    @abstractmethod
    def remove_ports_from_host(self, host_name, ports, connectivity_type):
        """
        This function should remove ports from host in the storage system.

        Args:
           host_name         : name of the host to be created in the storage system
           ports             : ports (e.g. fc wwns, iqn) of the host.
           connectivity_type : the connectivity_type chosen by the user

        Returns:
            None

        Raises:
            None
        """
        raise NotImplementedError

    @abstractmethod
    def get_host_connectivity_ports(self, host_name, connectivity_type):
        """
        This function should return ports from connectivity type on host in the storage system.

        Args:
           host_name         : name of the host to be created in the storage system
           connectivity_type : the connectivity_type chosen by the user

        Returns:
            list

        Raises:
            HostNotFoundError
            UnsupportedConnectivityTypeError
        """
        raise NotImplementedError

    @abstractmethod
    def get_host_connectivity_type(self, host_name):
        """
        This function should return the ports' connectivity type from host in the storage system.

        Args:
           host_name  : name of the host to be created in the storage system

        Returns:
            string

        Raises:
            HostNotFoundError
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
           space_efficiency : as passed from the CSI request

        Returns:
            None

        Raises:
            SpaceEfficiencyNotSupported
        """
        raise NotImplementedError

    @abstractmethod
    def get_replication(self, replication_request):
        """
        This function will return the volume replication relationship info

        Args:
            replication_request : class containing all necessary parameters for replication

        Returns:
            Replication

        Raises:
            ObjectNotFound
            InvalidArgument
            PermissionDenied
        """
        raise NotImplementedError

    @abstractmethod
    def create_replication(self, replication_request):
        """
        This function will create and activate a volume replication relationship

        Args:
            replication_request : class containing all necessary parameters for replication

        Returns:
            None

        Raises:
            ObjectNotFound
            InvalidArgument
            PermissionDenied
        """
        raise NotImplementedError

    @abstractmethod
    def delete_replication(self, replication):
        """
        This function will disable and delete a volume replication relationship

        Args:
            replication : replication to be deleted

        Returns:
            None

        Raises:
            ObjectNotFound
            InvalidArgument
            PermissionDenied
        """
        raise NotImplementedError

    @abstractmethod
    def promote_replication_volume(self, replication):
        """
        This function will promote the role of the volume in the connected system to be primary

        Args:
            replication : replication to be promoted

        Returns:
            None

        Raises:
            ObjectNotFound
            InvalidArgument
            PermissionDenied
        """
        raise NotImplementedError

    @abstractmethod
    def demote_replication_volume(self, replication):
        """
        This function will demote the role of the volume in the connected system to be secondary

        Args:
            replication : replication to be demoted

        Returns:
            None

        Raises:
            ObjectNotFound
            InvalidArgument
            PermissionDenied
        """
        raise NotImplementedError

    @abstractmethod
    def add_io_group_to_host(self, host_name, io_group):
        """
        This function should add io_group to host.

        Args:
           host_name : name of the host in the storage system
           io_group  : the io_group to add to the host

        Returns:
            None

        Raises:
            HostNotFoundError
            IoGroupIsInValid
        """
        raise NotImplementedError

    @abstractmethod
    def remove_io_group_from_host(self, host_name, io_group):
        """
        This function should remove io_group from host.

        Args:
           host_name : name of the host in the storage system
           io_group  : the io_group to remove from the host

        Returns:
            None

        Raises:
            HostNotFoundError
            IoGroupIsInValid
        """
        raise NotImplementedError

    @abstractmethod
    def get_host_io_group(self, host_name):
        """
        This function should return the io_group from host.

        Args:
           host_name : name of the host in the storage system

        Returns:
            List

        Raises:
            HostNotFoundError
        """
        raise NotImplementedError

    @abstractmethod
    def change_host_protocol(self, host_name, protocol):
        """
        This function should change the protocol of a host.

        Args:
           host_name : name of the host in the storage system
           protocol  : the new protocol

        Returns:
            None

        Raises:
            HostNotFoundError
            UnSupportedParameter
            CannotChangeHostProtocolBecauseOfMappedPorts
        """
        raise NotImplementedError

    @abstractmethod
    def register_plugin(self, unique_key,  metadata):
        """
        This function should register CSI plugin with unique_key and metadata accordingly to the feature it used.

        Args:
           unique_key : a unique key that will represent a feature
           metadata  : a metadata that will add some information

        Returns:
            None

        Raises:
            None
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
