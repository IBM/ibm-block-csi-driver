
class NoConnctionAvailableException(Exception):
    def __init__(self, endpoint):
        self.message = "no connection available to endpoint : {0}".format(endpoint)
    
    def __str__(self, *args, **kwargs):
        return self.message 
    