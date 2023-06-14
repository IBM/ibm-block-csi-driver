import grpc
from decorator import decorator

from controllers.common.csi_logger import get_stdout_logger
from controllers.common.utils import set_current_thread_name
from controllers.servers.errors import ObjectAlreadyProcessingError
from controllers.servers.settings import VOLUME_TYPE_NAME, VOLUME_GROUP_TYPE_NAME, LOCK_REPLICATION_REQUEST_ATTR
from controllers.servers.csi.exception_handler import handle_exception, handle_common_exceptions
from controllers.servers.csi.sync_lock import SyncLock

logger = get_stdout_logger()


def csi_method(error_response_type, lock_request_attribute=''):
    @decorator
    def call_csi_method(controller_method, servicer, request, context):
        lock_id = getattr(request, lock_request_attribute, None)
        return _set_sync_lock(lock_id, lock_request_attribute, error_response_type,
                              controller_method, servicer, request, context)

    return call_csi_method

def csi_fence_method(error_response_type):
    @decorator
    def call_csi_method(controller_method, servicer, request, context):
        lock_id = request.parameters.get('fenceToken', '')
        return _set_sync_lock(lock_id, 'fenceToken', error_response_type,
                              controller_method, servicer, request, context)

    return call_csi_method

def csi_replication_method(error_response_type):
    @decorator
    def call_csi_method(controller_method, servicer, request, context):
        replication_id = getattr(request, LOCK_REPLICATION_REQUEST_ATTR, None)
        if replication_id:
            if replication_id.HasField(VOLUME_GROUP_TYPE_NAME):
                lock_id = replication_id.volumegroup.volume_group_id
            elif replication_id.HasField(VOLUME_TYPE_NAME):
                lock_id = replication_id.volume.volume_id
            else:
                lock_id = None
        return _set_sync_lock(lock_id, LOCK_REPLICATION_REQUEST_ATTR, error_response_type,
                              controller_method, servicer, request, context)

    return call_csi_method


def _set_sync_lock(lock_id, lock_request_attribute, error_response_type,
                   controller_method, servicer, request, context):
    set_current_thread_name(lock_id)
    controller_method_name = controller_method.__name__
    logger.info(controller_method_name)
    try:
        with SyncLock(lock_request_attribute, lock_id, controller_method_name):
            response = handle_common_exceptions(controller_method, servicer, request, context, error_response_type)
    except ObjectAlreadyProcessingError as ex:
        return handle_exception(ex, context, grpc.StatusCode.ABORTED, error_response_type)
    logger.info("finished {}".format(controller_method_name))
    return response
