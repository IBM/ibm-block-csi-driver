from decorator import decorator

import grpc

import controller.array_action.errors as array_errors
from controller.common.csi_logger import get_stdout_logger
from controller.controller_server.errors import ValidationException

logger = get_stdout_logger()

status_codes_by_exception = {
    NotImplementedError: grpc.StatusCode.UNIMPLEMENTED,
    ValidationException: grpc.StatusCode.INVALID_ARGUMENT,
    array_errors.IllegalObjectID: grpc.StatusCode.INVALID_ARGUMENT,
    array_errors.IllegalObjectName: grpc.StatusCode.INVALID_ARGUMENT,
    array_errors.PoolParameterIsMissing: grpc.StatusCode.INVALID_ARGUMENT,
    array_errors.ObjectNotFoundError: grpc.StatusCode.NOT_FOUND,
    array_errors.HostNotFoundError: grpc.StatusCode.NOT_FOUND,
    array_errors.PermissionDeniedError: grpc.StatusCode.PERMISSION_DENIED,
    array_errors.ObjectIsStillInUseError: grpc.StatusCode.FAILED_PRECONDITION,
    array_errors.CredentialsError: grpc.StatusCode.UNAUTHENTICATED
}


def _build_non_ok_response(message, context, status_code, response_type):
    context.set_details(message)
    context.set_code(status_code)
    return response_type()


def build_error_response(message, context, status_code, response_type):
    logger.error(message)
    return _build_non_ok_response(message, context, status_code, response_type)


def handle_exception(ex, context, status_code, response_type):
    logger.exception(ex)
    return _build_non_ok_response(str(ex), context, status_code, response_type)


def handle_common_exceptions(response_type):
    @decorator
    def handle_common_exceptions_with_response(controller_method, servicer, request, context):
        try:
            return controller_method(servicer, request, context)
        except Exception as ex:
            status_code = status_codes_by_exception.get(type(ex), grpc.StatusCode.INTERNAL)
            return handle_exception(ex, context, status_code, response_type)

    return handle_common_exceptions_with_response
