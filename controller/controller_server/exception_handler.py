from decorator import decorator

import grpc

import controller.array_action.errors as controller_errors
from controller.common.csi_logger import get_stdout_logger
from controller.controller_server.errors import ValidationException

logger = get_stdout_logger()

status_codes_by_exception = {
    ValidationException: grpc.StatusCode.INVALID_ARGUMENT,
    controller_errors.IllegalObjectName: grpc.StatusCode.INVALID_ARGUMENT,
    controller_errors.PoolParameterIsMissing: grpc.StatusCode.INVALID_ARGUMENT,
    controller_errors.ObjectNotFoundError: grpc.StatusCode.NOT_FOUND,
    controller_errors.PermissionDeniedError: grpc.StatusCode.PERMISSION_DENIED,
    controller_errors.NotEnoughSpaceInPool: grpc.StatusCode.RESOURCE_EXHAUSTED
}


def handle_exception(ex, context, status_code, response_type):
    logger.exception(ex)
    context.set_details(str(ex))
    context.set_code(status_code)
    return response_type()


def handle_common_exceptions(response_type):
    @decorator
    def handle_common_exceptions_with_response(controller_method, servicer, request, context):
        try:
            return controller_method(servicer, request, context)
        except Exception as ex:
            return handle_exception(ex, context, status_codes_by_exception.get(type(ex), grpc.StatusCode.INTERNAL),
                                    response_type)

    return handle_common_exceptions_with_response
