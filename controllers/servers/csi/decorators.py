import grpc
from decorator import decorator

from controllers.array_action import errors as array_errors
from controllers.common.csi_logger import get_stdout_logger
from controllers.common.utils import set_current_thread_name
from controllers.servers import utils
from controllers.servers.errors import ObjectAlreadyProcessingError
from controllers.servers.settings import (VOLUME_TYPE_NAME, VOLUME_GROUP_TYPE_NAME,
                                          LOCK_REPLICATION_REQUEST_ATTR, UNIQUE_KEY_KEY)
from controllers.array_action.settings import METADATA_KEY
from controllers.array_action.registration_maps import REGISTRATION_MAP
from controllers.servers.csi.exception_handler import handle_exception, handle_common_exceptions, build_error_response
from controllers.servers.csi.sync_lock import SyncLock

logger = get_stdout_logger()


def csi_method(error_response_type, lock_request_attribute=''):
    @decorator
    def call_csi_method(controller_method, servicer, request, context):
        lock_id = getattr(request, lock_request_attribute, None)
        return _set_sync_lock(lock_id, lock_request_attribute, error_response_type,
                              controller_method, servicer, request, context)

    return call_csi_method


def csi_replication_method(error_response_type, lock_volume_group_for_ramen=False):
    @decorator
    def call_csi_method(controller_method, servicer, request, context):
        replication_id = getattr(request, LOCK_REPLICATION_REQUEST_ATTR, None)
        lock_id = None
        if replication_id:
            if replication_id.HasField(VOLUME_GROUP_TYPE_NAME):
                lock_id = replication_id.volumegroup.volume_group_id
            elif replication_id.HasField(VOLUME_TYPE_NAME):
                lock_id = replication_id.volume.volume_id

                if lock_volume_group_for_ramen:
                    try:
                        vg_id = utils.resolve_vg_lock_id_for_ramen(request)
                    except array_errors.ObjectNotFoundError as ex:
                        return build_error_response(str(ex), context, grpc.StatusCode.NOT_FOUND, error_response_type)
                    if vg_id is not None:
                        logger.info("Ramen VG lock: volume '{}' resolved to volume group '{}', "
                                    "acquiring lock on volume group".format(lock_id, vg_id))
                        lock_id = vg_id

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


def register_csi_plugin():
    @decorator
    def call_csi_plugin_registration(mediator_method, mediator_class, *args):
        plugin_fields = REGISTRATION_MAP.get(mediator_method.__name__, {})
        if plugin_fields:
            mediator_class.register_plugin(plugin_fields[UNIQUE_KEY_KEY], plugin_fields[METADATA_KEY])
        return mediator_method(mediator_class, *args)

    return call_csi_plugin_registration
