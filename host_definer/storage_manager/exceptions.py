class StorageException(Exception):
    
    def __str__(self, *args, **kwargs):
        return self.message