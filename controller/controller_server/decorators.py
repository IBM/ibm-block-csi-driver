import grpc
from decorator import decorator

from controller.common.utils import set_current_thread_name
from controller.controller_server.errors import ObjectAlreadyProcessingError
from controller.controller_server.exception_handler import handle_exception, status_codes_by_exception


def csi_method(error_response_type, lock_request_attribute=None):
    @decorator
    def handle_csi_method(server_method, servicer, request, context):
        if lock_request_attribute:
            lock_id = getattr(request, lock_request_attribute)
            set_current_thread_name(lock_id)
            controller_method_name = server_method.__name__
            try:
                servicer.sync_lock.add_object_lock(lock_request_attribute, lock_id, controller_method_name)
            except ObjectAlreadyProcessingError as ex:
                return handle_exception(ex, context, grpc.StatusCode.ABORTED, error_response_type)
        try:
            response = server_method(servicer, request, context)
        except Exception as ex:
            status_code = status_codes_by_exception.get(type(ex), grpc.StatusCode.INTERNAL)
            response = handle_exception(ex, context, status_code, error_response_type)
        if lock_request_attribute:
            servicer.sync_lock.remove_object_lock(lock_request_attribute, lock_id, controller_method_name)
        return response

    return handle_csi_method
