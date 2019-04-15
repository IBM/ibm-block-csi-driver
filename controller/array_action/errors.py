
class NoConnctionAvailableException(Exception):
    def __init__(self, endpoint):
        self.message = "no connection available to endpoint : {0}".format(endpoint)
    
    def __str__(self, *args, **kwargs):
        return self.message 
 

class CredentailsError(Exception):
    def __init__(self,  endpoint):
        self.message = "credential error has occurred while connecting to endpoint : {0} ".format(endpoint)
    
    def __str__(self, *args, **kwargs):
        return self.message 
