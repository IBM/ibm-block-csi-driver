import grpc
from decorator import decorator

from controller.common.utils import set_current_thread_name
from controller.controller_server.errors import ObjectAlreadyProcessingError
from controller.controller_server.exception_handler import handle_exception, handle_common_exceptions


def csi_method(error_response_type, lock_request_attribute=''):
    @decorator
    def handle_csi_method(server_method, servicer, request, context):
        lock_id = getattr(request, lock_request_attribute, None)
        set_current_thread_name(lock_id)
        controller_method_name = server_method.__name__
        try:
            with servicer.sync_lock(lock_request_attribute, lock_id, controller_method_name):
                return handle_common_exceptions(server_method, servicer, request, context, error_response_type)
        except ObjectAlreadyProcessingError as ex:
            return handle_exception(ex, context, grpc.StatusCode.ABORTED, error_response_type)

    return handle_csi_method
