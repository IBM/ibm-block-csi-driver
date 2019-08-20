import abc


class ArrayMediator:

    @abc.abstractmethod
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

    @abc.abstractmethod
    def disconnect(self):
        """
        This function disconnect the storage system connection that was opened in the init phase.

        Returns:
            None
        """
        raise NotImplementedError

    @abc.abstractmethod
    def create_volume(self, vol_name, size_in_bytes, capabilities, pool):
        """
        This function should create a volume in the storage system.

        Args:
            vol_name      : name of the volume to be created in the stoarge system
            size_in_bytes : size in bytes of the volume
            capabilities  : dict of capabilities {<capbility_name>:<value>}
            pool          : pool name to create the volume in.

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

    @abc.abstractmethod
    def delete_volume(self, volume_id):
        """
        This function should delete a volume in the storage system.

        Args:
            vol_id : wwn of the volume to delete

        Returns:
            None

        Raises:
            volumeNotFound
            PermissionDenied
        """
        raise NotImplementedError

    @abc.abstractmethod
    def get_volume(self, volume_name):
        """
        This function return volume info about the volume.

        Args:
            vol_name : name of the volume to be created in the storage system

        Returns:
           Volume

        Raises:
            volumeNotFound
            IllegalObjectName
            PermissionDenied
        """
        raise NotImplementedError

    @abc.abstractmethod
    def get_volume_mappings(self, volume_id):
        """
        This function return volume mappings.

        Args:
           volume_id : the volume WWN.

        Returns:
           mapped_host_luns : a dict like this: {<host name>:<lun id>,...}

        Raises:
            volumeNotFound
        """
        raise NotImplementedError

    @abc.abstractmethod
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
            volumeNotFound
            hostNotFound
            PermissionDenied
            MappingError
        """
        raise NotImplementedError

    @abc.abstractmethod
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
            volumeNotFound
            hostNotFound
            PermissionDenied
            MappingError
        """
        raise NotImplementedError

    @abc.abstractmethod
    def get_array_iscsi_name(self):
        """
        This function will return the iscsi name of the storage array

        Args:
            None

        Returns:
            iscsi_name : the iscsi address of the storage

        Raises:
            None
        """
        raise NotImplementedError

    @abc.abstractmethod
    def get_host_by_host_identifiers(self, iscis_iqn):
        """
        This function will find the name of the volume by the volume_id and unmap the volume from the host.

        Args:
           iscis_iqn : the iscsi iqn of the wanted host.

        Returns:
           connectivity_types : list of connectivity types ([iscis, fc] or just [iscsi],..)
           hostname           : the name of the host

        Raises:
            hostNotFound
            multipleHostsFoundError
            PermissionDenied
        """
        raise NotImplementedError

    @abc.abstractmethod
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
    @abc.abstractmethod
    def array_type(self):
        """
        The storage system type.
        """
        raise NotImplementedError

    @property
    @abc.abstractmethod
    def port(self):
        """
        The storage system management port number.
        """
        raise NotImplementedError

    @property
    @abc.abstractmethod
    def max_vol_name_length(self):
        """
        The max number of concurrent connections to the storage system.
        """
        raise NotImplementedError

    @property
    @abc.abstractmethod
    def max_connections(self):
        """
        The max number of concurrent connections to the storage system.
        """
        raise NotImplementedError

    @property
    @abc.abstractmethod
    def minimal_volume_size_in_bytes(self):
        """
        The minimal volume size in bytes (used in case trying to provision volume with zero size).
        """
        raise NotImplementedError

    @property
    @abc.abstractmethod
    def max_lun_retries(self):
        """
            The maximum number of times a map operation will retry if lun is already in use
        """
        raise NotImplementedError
