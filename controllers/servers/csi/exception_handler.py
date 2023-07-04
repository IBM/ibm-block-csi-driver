import grpc

import controllers.array_action.errors as array_errors
from controllers.common.csi_logger import get_stdout_logger
from controllers.servers.errors import ValidationException

logger = get_stdout_logger()

status_codes_by_exception = {
    NotImplementedError: grpc.StatusCode.UNIMPLEMENTED,
    ValidationException: grpc.StatusCode.INVALID_ARGUMENT,
    array_errors.InvalidArgumentError: grpc.StatusCode.INVALID_ARGUMENT,
    array_errors.PoolParameterIsMissing: grpc.StatusCode.INVALID_ARGUMENT,
    array_errors.VirtSnapshotFunctionNotSupportedMessage: grpc.StatusCode.INVALID_ARGUMENT,
    array_errors.ObjectNotFoundError: grpc.StatusCode.NOT_FOUND,
    array_errors.HostNotFoundError: grpc.StatusCode.NOT_FOUND,
    array_errors.PermissionDeniedError: grpc.StatusCode.PERMISSION_DENIED,
    array_errors.ObjectIsStillInUseError: grpc.StatusCode.FAILED_PRECONDITION,
    array_errors.CredentialsError: grpc.StatusCode.UNAUTHENTICATED,
    array_errors.ObjectAlreadyExistError: grpc.StatusCode.ALREADY_EXISTS,
}


def _build_non_ok_response(message, context, status_code, response_type):
    context.set_details(message)
    context.set_code(status_code)
    return response_type()


def build_error_response(message, context, status_code, response_type):
    logger.error(message)
    return _build_non_ok_response(message, context, status_code, response_type)


def handle_exception(exception, context, status_code, response_type):
    logger.exception(exception)
    return _build_non_ok_response(str(exception), context, status_code, response_type)


def handle_common_exceptions(controller_method, servicer, request, context, response_type):
    try:
        return controller_method(servicer, request, context)
    except Exception as exception:
        status_code = status_codes_by_exception.get(type(exception), grpc.StatusCode.INTERNAL)
        return handle_exception(exception, context, status_code, response_type)
