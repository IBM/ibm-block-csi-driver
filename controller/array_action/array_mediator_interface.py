import abc 

class ArrayMediator():
    
    @abc.abstractmethod
    def __init__(self, user, password, address):
        """ 
        This is the init function for the class.
        it should establish the connection to the storage system.  
        
        Args:
            user : user name for connecting to the endpoint
            password : password for connecting to the endpoint
            endpoint : storage array fqdn or ip
        
        Returns:  
            empty.
            
        Errors:
            CredentialsError
        
        """ 
        raise NotImplementedError
    
    @abc.abstractmethod
    def create_volume(self, vol_name, size_in_bytes, capabilities, pool):
        """ 
        This function should create a volume in the storage system. 
        
        Args:
            vol_name : name of the volume to be created in the stoarge system
            size_in_bytes: size in bytes of the volume
            capabilities : dict of capabilities {<capbility_name>:<value>}
            pool : pool name to create the volume in.
        
        Returns:  
            volume_id : the volume WWN. 
            
        Errors:
            VolumeAlreadyExists
            PoolDoesNotExist
            PoolDoesNotMatchCapabilities
            CapabilityNotSupported (user is passing capability value that does not exist, or wrong spelling..)
            IllegalObjectName 

        """ 
        raise NotImplementedError
    
    @abc.abstractmethod
    def delete_volume(self, volume_id):
        """ 
        This function should delete a volume in the storage system. 
        
        Args:
            vol_id : wwn of the volume to delete
        
        Returns:  
           empty
            
        Errors:
            volumeNotFound

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
            
        Errors:
            volumeNotFound
            IllegalObjectName

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
            
        Errors:
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
            
        Errors:
            noAvailableLun
            volumeNotFound
            hostNotFound
        
        """ 
        raise NotImplementedError     
    
    @abc.abstractmethod
    def unmap_volume(self, volume_id, host_name):
        """ 
        This function will find the name of the volume by the volume_id and unmap the volume from the host.
                
        Args:
           volume_id : the volume WWN.
           host_name : the name of the host to map the volume to.
        
        Returns:  
           empty
            
        Errors:
            volumeNotFound
            volAlreadyUnmapped
            hostNotFound
        
        """ 
        raise NotImplementedError 
    
    @abc.abstractmethod
    def get_host_by_host_identifiers(self, iscis_iqn, fc_initiators):
        """ 
        This function will find the name of the volume by the volume_id and unmap the volume from the host.
                
        Args:
           iscis_iqn : the iscsi iqn of the wanted host.
           fc_initiators : fc initiators of the wanted host.
        
        Returns:  
           connectivity_types : list of connectivity types ([iscis, fc] or just [iscsi],..)
           hostname : the name of the host
            
        Errors:
            hostNotFound
            multipleHostsFoundError
        
        """ 
        raise NotImplementedError     
    
    