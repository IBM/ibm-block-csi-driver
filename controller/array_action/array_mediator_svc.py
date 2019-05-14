from array_mediator_interface import  ArrayMediator

class SVCArrayMediator(ArrayMediator):
    max_connections = 10
    port = 22
    array_type = "svc"
    pass
