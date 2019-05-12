from array_mediator_interface import  ArrayMediator

class SVCArrayMediator(ArrayMediator):
    CONNECTION_LIMIT = 10
    PORT = 22
    ARRAY_TYPE = "svc"
    pass
