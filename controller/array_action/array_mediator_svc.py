from controller.array_action.array_mediator_interface import  ArrayMediator

class SVCArrayMediator(ArrayMediator):
    max_connections = 10  # TODO : need to implement all the interface methods\properties 
    port = 22
    array_type = "svc"
    pass
