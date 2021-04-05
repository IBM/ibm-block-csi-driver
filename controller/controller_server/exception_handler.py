from decorator import decorator

import grpc

import controller.array_action.errors as controller_errors
from controller.common.csi_logger import get_stdout_logger
from controller.controller_server.errors import ObjectIdError
from controller.controller_server.errors import ValidationException
from controller.csi_general import csi_pb2

logger = get_stdout_logger()

exceptions_to_status_codes_map = {
    ValidationException: grpc.StatusCode.INVALID_ARGUMENT,
    ObjectIdError: grpc.StatusCode.NOT_FOUND,
    controller_errors.IllegalObjectName: grpc.StatusCode.INVALID_ARGUMENT,
    controller_errors.SpaceEfficiencyNotSupported: grpc.StatusCode.INVALID_ARGUMENT,
    controller_errors.PoolDoesNotExist: grpc.StatusCode.INVALID_ARGUMENT,
    controller_errors.PoolDoesNotMatchCapabilities: grpc.StatusCode.INVALID_ARGUMENT,
    controller_errors.PoolParameterIsMissing: grpc.StatusCode.INVALID_ARGUMENT,
    controller_errors.ExpectedSnapshotButFoundVolumeError: grpc.StatusCode.INVALID_ARGUMENT,
    controller_errors.ObjectNotFoundError: grpc.StatusCode.NOT_FOUND,
    controller_errors.PermissionDeniedError: grpc.StatusCode.PERMISSION_DENIED,
    controller_errors.VolumeAlreadyExists: grpc.StatusCode.ALREADY_EXISTS,
    controller_errors.NotEnoughSpaceInPool: grpc.StatusCode.RESOURCE_EXHAUSTED
}


response_types = {
    "CreateVolume": csi_pb2.CreateVolumeResponse
}


@decorator
def handle_requests_safely(call, servicer, request, context):
    try:
        return call(servicer, request, context)
    except Exception as ex:
        logger.exception(ex)
        context.set_details(str(ex))
        status_code = exceptions_to_status_codes_map.get(type(ex), grpc.StatusCode.INTERNAL)
        context.set_code(status_code)
        return response_types[call.__name__]()
