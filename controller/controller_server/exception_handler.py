from decorator import decorator

import grpc

import controller.array_action.errors as controller_errors
from controller.common.csi_logger import get_stdout_logger
from controller.controller_server.errors import ValidationException

logger = get_stdout_logger()

status_codes_by_exception = {
    ValidationException: grpc.StatusCode.INVALID_ARGUMENT,
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


def handle_common_exceptions(response_type):
    @decorator
    def decorated_handle_common_exceptions(controller_method, servicer, request, context):
        try:
            return controller_method(servicer, request, context)
        except Exception as ex:
            if type(ex) not in status_codes_by_exception.keys():
                raise
            logger.exception(ex)
            context.set_details(str(ex))
            status_code = status_codes_by_exception.get(type(ex), grpc.StatusCode.INTERNAL)
            context.set_code(status_code)
            return response_type()
    return decorated_handle_common_exceptions
