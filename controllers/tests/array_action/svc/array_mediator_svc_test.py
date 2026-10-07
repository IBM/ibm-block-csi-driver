import json
import unittest
from unittest.mock import MagicMock
from datetime import datetime, timedelta

from mock import patch, Mock, call, PropertyMock
from munch import Munch
from openapi_client.exceptions import ApiException as SdkApiException

from controllers.common.config import config
import controllers.array_action.errors as array_errors
import controllers.tests.array_action.svc.test_settings as svc_settings
import controllers.tests.array_action.test_settings as array_settings
import controllers.tests.common.test_settings as common_settings
from controllers.array_action.array_action_types import ReplicationRequest, ReplicationInfo, ReplicationStatus
from controllers.array_action.array_mediator_svc import SVCArrayMediator, build_kwargs_from_parameters, \
    FCMAP_STATUS_DONE, YES
from controllers.array_action.settings import (REPLICATION_TYPE_MIRROR, REPLICATION_TYPE_EAR,
                                               RCRELATIONSHIP_STATE_READY, ENDPOINT_TYPE_PRODUCTION,
                                               HEALTHY_DR_LINK_STATES, DEGRADED_DR_LINK_STATES, FAILED_DR_LINK_STATES,
                                               DR_LINK_STATUS_DISCONNECTED,
                                               DR_LINK_STATUS_SUSPENDED_MISSING_CONNECTIVITY)
from controllers.common.node_info import Initiators
from controllers.common.settings import ARRAY_TYPE_SVC, SPACE_EFFICIENCY_THIN, SPACE_EFFICIENCY_COMPRESSED, \
    SPACE_EFFICIENCY_DEDUPLICATED_COMPRESSED, SPACE_EFFICIENCY_DEDUPLICATED_THIN, SPACE_EFFICIENCY_DEDUPLICATED, \
    SPACE_EFFICIENCY_THICK, EAR_VOLUME_FC_MAP_COUNT, SCSI_PROTOCOL, NVME_PROTOCOL
from controllers.tests import utils
from controllers.tests.common.test_settings import OBJECT_INTERNAL_ID, \
    OTHER_OBJECT_INTERNAL_ID, REPLICATION_NAME, SYSTEM_ID, COPY_TYPE

EMPTY_BYTES = b""


def _sdk_error(message, status=400):
    """Create an SdkApiException whose body encodes the given CMMVC message string."""
    ex = SdkApiException(status=status)
    ex.body = json.dumps({"message": message})
    return ex


class TestArrayMediatorSVC(unittest.TestCase):

    def setUp(self):
        self.endpoint = [common_settings.SECRET_MANAGEMENT_ADDRESS_VALUE]
        with patch("controllers.array_action.array_mediator_svc.SVCArrayMediator._connect"):
            self.svc = SVCArrayMediator(common_settings.SECRET_USERNAME_VALUE, common_settings.SECRET_PASSWORD_VALUE,
                                        self.endpoint)
        self.svc.sdk = Mock()
        # Pre-populate _cluster so _system_info property doesn't call the SDK
        self.svc._cluster = Munch({
            svc_settings.LSSYSTEM_LOCATION_ATTR_KEY: svc_settings.LOCAL_LOCATION,
            svc_settings.LSSYSTEM_ID_ALIAS_ATTR_KEY: svc_settings.DUMMY_ID_ALIAS,
            svc_settings.LSSYSTEM_CODE_LEVEL_ATTR_KEY: svc_settings.CODE_LEVEL,
        })
        node = self._mock_node()
        self.svc.sdk.svc_info_api.lsnode_post.return_value = [node]
        lsportip_port = Munch(
            {svc_settings.LSPORTIP_NODE_ID_ATTR_KEY: svc_settings.DUMMY_INTERNAL_ID1,
             svc_settings.LSPORTIP_IP_ADDRESS_ATTR_KEY: array_settings.DUMMY_IP_ADDRESS1,
             svc_settings.LSPORTIP_IP_ADDRESS_6_ATTR_KEY: None})
        lsip_port = Munch({svc_settings.LSIP_NODE_ID_ATTR_KEY: svc_settings.DUMMY_INTERNAL_ID1,
                           svc_settings.LSIP_IP_ADDRESS_ATTR_KEY: array_settings.DUMMY_IP_ADDRESS1,
                           svc_settings.LSIP_PORTSET_ID_ATTR_KEY: svc_settings.DUMMY_PORTSET_ID})
        self.svc.sdk.svc_info_api.lsportip_post.return_value = [lsportip_port]
        self.svc.sdk.svc_info_api.lsip_post.return_value = [lsip_port]
        self.fcmaps = [self._mock_fcmap(common_settings.SOURCE_VOLUME_NAME, svc_settings.DUMMY_FCMAP_ID)]
        self.fcmaps_as_target = [self._mock_fcmap(common_settings.SOURCE_VOLUME_NAME, svc_settings.DUMMY_FCMAP_ID)]
        self.fcmaps_as_source = [self._mock_fcmap(common_settings.SNAPSHOT_NAME, svc_settings.DUMMY_FCMAP_ID)]
        self.svc.sdk.svc_info_api.lsfcmap_post.return_value = self.fcmaps
        # Default lsvdisk_post mock: resolves any UID to source_volume_name for WWN→name lookups
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._get_cli_volume()
        # Default lsmdiskgrp_post mock: returns a pool with empty site (no site restriction)
        self.svc.sdk.svc_info_api.lsmdiskgrp_post.return_value = Munch({'site_name': ''})
        # Simulate addsnapshot_post and chvolumereplicationinternals_id_post not present
        del self.svc.sdk.svc_task_api.addsnapshot_post
        del self.svc.sdk.svc_task_api.chvolumereplicationinternals_id_post

    def _mock_node(self, node_id=svc_settings.DUMMY_INTERNAL_ID1, name=array_settings.DUMMY_NODE1_NAME,
                   iqn=array_settings.DUMMY_NODE1_IQN, status=svc_settings.ONLINE_STATUS):
        return Munch({svc_settings.NODE_ID_ATTR_KEY: node_id,
                      svc_settings.NODE_NAME_ATTR_KEY: name,
                      svc_settings.NODE_ISCSI_NAME_ATTR_KEY: iqn,
                      svc_settings.NODE_STATUS_ATTR_KEY: status})

    def _mock_fcmap(self, source_name, id_value):
        return Munch({svc_settings.FCMAP_SOURCE_VDISK_NAME_ATTR_KEY: source_name,
                      svc_settings.FCMAP_TARGET_VDISK_NAME_ATTR_KEY: common_settings.TARGET_VOLUME_NAME,
                      svc_settings.FCMAP_ID_ATTR_KEY: id_value,
                      svc_settings.FCMAP_STATUS_ATTR_KEY: FCMAP_STATUS_DONE,
                      svc_settings.FCMAP_COPY_RATE_ATTR_KEY: svc_settings.DUMMY_COPY_RATE,
                      svc_settings.FCMAP_RC_CONTROLLED_ATTR_KEY: svc_settings.NO_VALUE_ALIAS})

    def test_init_unsupported_system_version(self):
        code_level_below_min_supported = "7.7.77.77 (build 777.77.7777777777777)"
        sdk_mock = Mock()
        sdk_mock.svc_info_api.lssystem_post.return_value = [
            Munch({svc_settings.LSSYSTEM_LOCATION_ATTR_KEY: svc_settings.LOCAL_LOCATION,
                   svc_settings.LSSYSTEM_CODE_LEVEL_ATTR_KEY: code_level_below_min_supported,
                   svc_settings.LSSYSTEM_ID_ALIAS_ATTR_KEY: svc_settings.DUMMY_ID_ALIAS})]
        with patch("controllers.array_action.array_mediator_svc.StorageVirtualizeAPI",
                   return_value=sdk_mock):
            with self.assertRaises(array_errors.UnsupportedStorageVersionError):
                SVCArrayMediator(common_settings.SECRET_USERNAME_VALUE, common_settings.SECRET_PASSWORD_VALUE,
                                 self.endpoint)

    def test_create_replication_success(self):
        _, replication_request = self._prepare_mocks_for_replication()
        self.svc.sdk.svc_task_api.mkrcrelationship_post.return_value = {"id": "1"}

        self.svc.create_replication(replication_request)
        self.svc.sdk.svc_task_api.startrcrelationship_id_post.assert_called_once()

    def _prepare_rcrelationship_mock(self):
        """Return a raw list-of-dicts suitable as lsrcrelationship_post.return_value.

        Production code wraps this in _SdkResponse, then calls .as_list to get the
        first rcrelationship dict, so we provide a single-element list.
        'id' is required by _delete_replication (rmrcrelationship_id_post(id=rcrelationship.id)).
        'primary'='aux' so that promote/demote tests trigger switchrcrelationship.
        """
        return [{
            svc_settings.RCRELATIONSHIP_ID_ATTR_NAME: "1",
            svc_settings.RCRELATIONSHIP_NAME_ATTR_NAME: REPLICATION_NAME,
            svc_settings.RCRELATIONSHIP_STATE_ATTR_NAME: RCRELATIONSHIP_STATE_READY,
            svc_settings.RCRELATIONSHIP_COPY_TYPE_ATTR_NAME: svc_settings.RCRELATIONSHIP_COPY_TYPE,
            svc_settings.RCRELATIONSHIP_MASTER_CLUSTER_ID_ATTR_NAME: svc_settings.DUMMY_ID_ALIAS,
            svc_settings.RCRELATIONSHIP_PRIMARY_ATTR_NAME: "aux",
        }]

    def _prepare_mocks_for_replication(self):
        replication_request = ReplicationRequest(OBJECT_INTERNAL_ID, OTHER_OBJECT_INTERNAL_ID, SYSTEM_ID, COPY_TYPE,
                                                 REPLICATION_TYPE_MIRROR)
        replication = utils.get_mock_mediator_response_replication(name=REPLICATION_NAME,
                                                                   replication_type=REPLICATION_TYPE_MIRROR)
        rcrelationship_to_return = self._prepare_rcrelationship_mock()
        self.svc.sdk.svc_info_api.lsrcrelationship_post.return_value = rcrelationship_to_return
        return replication, replication_request

    def _prepare_mocks_for_ear_replication(self, is_ear_supported=True):
        replication_request = ReplicationRequest(OBJECT_INTERNAL_ID, OTHER_OBJECT_INTERNAL_ID, SYSTEM_ID, COPY_TYPE,
                                                 REPLICATION_TYPE_EAR, REPLICATION_NAME)
        replication = utils.get_mock_mediator_response_replication(name=REPLICATION_NAME,
                                                                   replication_type=REPLICATION_TYPE_EAR,
                                                                   volume_group_id=OBJECT_INTERNAL_ID)
        if is_ear_supported:
            self.svc.sdk.svc_task_api.chvolumereplicationinternals_id_post = Mock()

        cli_volume = self._get_cli_volume(in_volume_group=True)
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._mock_cli_object(cli_volume)
        return replication, replication_request

    def test_delete_replication_success(self):
        replication, _ = self._prepare_mocks_for_replication()

        self.svc.delete_replication(replication)
        self.svc.sdk.svc_task_api.rmrcrelationship_id_post.assert_called_once()

    def test_delete_replication_no_replication_found(self):
        replication, _ = self._prepare_mocks_for_replication()
        self.svc.sdk.svc_info_api.lsrcrelationship_post.return_value = Munch({})

        self.svc.delete_replication(replication)
        self.svc.sdk.svc_task_api.stoprcrelationship_id_post.assert_not_called()
        filter = f"RC_rel_name={REPLICATION_NAME}"
        self.svc.sdk.svc_info_api.lsrcrelationship_post.assert_called_once()

    def test_promote_replication_volume_success(self):
        replication, _ = self._prepare_mocks_for_replication()

        self.svc.promote_replication_volume(replication)
        self.svc.sdk.svc_task_api.stoprcrelationship_id_post.assert_not_called()
        self.svc.sdk.svc_task_api.switchrcrelationship_id_post.assert_called_once()

    def test_demote_replication_volume_success(self):
        replication, _ = self._prepare_mocks_for_replication()
        # Demote makes aux primary; for switch to be needed, primary must not be 'aux' currently
        self.svc.sdk.svc_info_api.lsrcrelationship_post.return_value = [{
            svc_settings.RCRELATIONSHIP_ID_ATTR_NAME: "1",
            svc_settings.RCRELATIONSHIP_NAME_ATTR_NAME: REPLICATION_NAME,
            svc_settings.RCRELATIONSHIP_STATE_ATTR_NAME: RCRELATIONSHIP_STATE_READY,
            svc_settings.RCRELATIONSHIP_COPY_TYPE_ATTR_NAME: svc_settings.RCRELATIONSHIP_COPY_TYPE,
            svc_settings.RCRELATIONSHIP_MASTER_CLUSTER_ID_ATTR_NAME: svc_settings.DUMMY_ID_ALIAS,
            svc_settings.RCRELATIONSHIP_PRIMARY_ATTR_NAME: "master",
        }]

        self.svc.demote_replication_volume(replication)
        self.svc.sdk.svc_task_api.stoprcrelationship_id_post.assert_not_called()
        self.svc.sdk.svc_task_api.switchrcrelationship_id_post.assert_called_once()

    def test_get_ear_replication_success(self):
        _, replication_request = self._prepare_mocks_for_ear_replication()
        replication_request.replication_policy = None
        # First call (_get_replication_policy): policy not set → None
        # Second call (_get_replication_mode): provides local_location + mode
        vg_replication_with_mode = Munch({'local_location': '1',
                                          'location1_replication_mode': ENDPOINT_TYPE_PRODUCTION})
        self.svc.sdk.svc_info_api.lsvolumegroupreplication_id_post.side_effect = [None, vg_replication_with_mode]

        replication = self.svc.get_replication(replication_request)

        self.assertEqual(replication.replication_type, REPLICATION_TYPE_EAR)
        self.assertEqual(replication.volume_group_id, OBJECT_INTERNAL_ID)

        self.svc.sdk.svc_info_api.lsvolumegroupreplication_id_post.assert_called()

    def test_get_ear_replication_not_supported(self):
        _, replication_request = self._prepare_mocks_for_ear_replication(is_ear_supported=False)

        replication = self.svc.get_replication(replication_request)
        self.assertEqual(replication, None)

        self.svc.sdk.svc_info_api.lsvdisk_post.assert_not_called()
        self.svc.sdk.svc_info_api.lsvolumegroupreplication_id_post.assert_not_called()

    def test_get_ear_replication_illegal_mode_failure(self):
        _, replication_request = self._prepare_mocks_for_ear_replication()

        self.svc.sdk.svc_info_api.lsvolumegroupreplication_id_post.return_value = None

        replication = self.svc.get_replication(replication_request)
        self.assertEqual(replication, None)
        self.assertEqual(2, self.svc.sdk.svc_info_api.lsvolumegroupreplication_id_post.call_count)
        self.svc.sdk.svc_info_api.lsvolumegroupreplication_id_post.assert_called()

    def test_create_ear_replication_success(self):
        _, replication_request = self._prepare_mocks_for_ear_replication()

        self.svc.sdk.svc_info_api.lsvolumegroupreplication_id_post.return_value = None

        self.svc.create_replication(replication_request)
        # _create_ear_replication calls chvolumegroup twice: once to remove existing policy, once to set new
        self.assertEqual(2, self.svc.sdk.svc_task_api.chvolumegroup_id_post.call_count)

    def test_create_ear_replication_not_supported(self):
        _, replication_request = self._prepare_mocks_for_ear_replication(is_ear_supported=False)

        self.svc.create_replication(replication_request)
        self.svc.sdk.svc_info_api.chvolumegroup_id_post.assert_not_called()

    def test_promote_ear_replication_volume_from_independent(self):
        pass

    def test_promote_ear_replication_volume_from_recovery(self):
        pass

    def test_promote_ear_replication_not_supported(self):
        replication, _ = self._prepare_mocks_for_ear_replication(False)

        self.svc.promote_replication_volume(replication)
        self.svc.sdk.svc_info_api.lsvolumegroupreplication_id_post.assert_not_called()
        self.svc.sdk.svc_info_api.chvolumegroupreplication_id_post.assert_not_called()

    def test_delete_ear_replication_success(self):
        replication, _ = self._prepare_mocks_for_ear_replication()
        self.svc.delete_replication(replication)
        self.svc.sdk.svc_task_api.chvolumegroup_id_post.assert_called_once()

    def test_delete_ear_replication_not_supported(self):
        replication, _ = self._prepare_mocks_for_ear_replication(is_ear_supported=False)
        self.svc.delete_replication(replication)

        self.svc.sdk.svc_info_api.chvolumegroup_id_post.assert_not_called()

    def test_demote_ear_replication_volume(self):
        replication, _ = self._prepare_mocks_for_ear_replication(is_ear_supported=True)
        # Mock volume group replication with checkpoint achieved
        mock_vg_replication = Munch({'local_location': '1',
                                     'location1_replication_mode': ENDPOINT_TYPE_PRODUCTION,
                                     'checkpoint_achieved': 'yes',
                                     'dr_link_status': 'running'})
        self.svc.sdk.svc_info_api.lsvolumegroupreplication_id_post.return_value = mock_vg_replication

        self.svc.demote_replication_volume(replication)

        # Verify checkpoint was created and status was checked
        self.svc.sdk.svc_task_api.chvolumegroupreplication_id_post.assert_called_once()
        self.assertEqual(self.svc.sdk.svc_info_api.lsvolumegroupreplication_id_post.call_count, 3)

    def test_demote_ear_replication_not_supported(self):
        replication, _ = self._prepare_mocks_for_ear_replication(is_ear_supported=False)
        self.svc.demote_replication_volume(replication)
        self.svc.sdk.svc_info_api.lsvolumegroupreplication_id_post.assert_not_called()

    def test_default_object_prefix_length_not_larger_than_max(self):
        prefix_length = len(self.svc.default_object_prefix)
        self.assertGreaterEqual(self.svc.max_object_prefix_length, prefix_length)
        self.assertGreaterEqual(self.svc.max_object_prefix_length, prefix_length)

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def _test_mediator_method_client_error(self, mediator_method, args, client_method, client_error, expected_error,
                                           mock_warning):
        mock_warning.return_value = False
        client_method.side_effect = [client_error]
        with self.assertRaises(expected_error):
            mediator_method(*args)

    def _test_mediator_method_client_cli_failure_error(self, mediator_method, args, client_method, error_message_id,
                                                       expected_error):
        self._test_mediator_method_client_error(mediator_method, args, client_method, _sdk_error(error_message_id),
                                                expected_error)

    def _test_get_volume_lsvdisk_cli_failure_error(self, volume_name, error_message_id, expected_error):
        self._test_mediator_method_client_cli_failure_error(self.svc.get_volume, (volume_name,
                                                                                  common_settings.DUMMY_POOL1, False),
                                                            self.svc.sdk.svc_info_api.lsvdisk_post, error_message_id,
                                                            expected_error)

    def test_get_volume_lsvdisk_cli_failure_errors(self):
        self._test_get_volume_lsvdisk_cli_failure_error(common_settings.VOLUME_NAME, "CMMVC5753E",
                                                        array_errors.ObjectNotFoundError)
        self._test_get_volume_lsvdisk_cli_failure_error(svc_settings.INVALID_NAME_1, "CMMVC6017E",
                                                        array_errors.InvalidArgumentError)
        self._test_get_volume_lsvdisk_cli_failure_error(svc_settings.INVALID_NAME_START_WITH_NUMBER, "CMMVC5703E",
                                                        array_errors.InvalidArgumentError)
        self._test_get_volume_lsvdisk_cli_failure_error("", "other error", SdkApiException)

    def _test_get_volume(self, get_cli_volume_args=None, is_virt_snap_func=False, lsvdisk_call_count=2):
        if get_cli_volume_args is None:
            get_cli_volume_args = {}
        cli_volume_mock = self._get_cli_volume(**get_cli_volume_args)
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = cli_volume_mock
        volume = self.svc.get_volume(common_settings.VOLUME_NAME, pool=common_settings.DUMMY_POOL1,
                                     is_virt_snap_func=is_virt_snap_func)
        self.assertEqual(array_settings.DUMMY_CAPACITY_INT, volume.capacity_bytes)
        self.assertEqual(common_settings.DUMMY_POOL1, volume.pool)
        self.assertEqual(ARRAY_TYPE_SVC, volume.array_type)
        self.assertEqual(lsvdisk_call_count, self.svc.sdk.svc_info_api.lsvdisk_post.call_count)
        return volume

    def test_get_volume_success(self):
        self._test_get_volume()

    def test_get_volume_with_source_success(self):
        volume = self._test_get_volume(
            {svc_settings.GET_CLI_VOLUME_VDISK_UID_KEY: common_settings.SOURCE_VOLUME_ID,
             svc_settings.GET_CLI_VOLUME_FCMAP_KEY: svc_settings.DUMMY_INTERNAL_ID1})
        self.assertEqual(common_settings.SOURCE_VOLUME_ID, volume.source_id)

    def test_get_volume_with_source_and_flashcopy_enabled(self):
        volume = self._test_get_volume(
            {svc_settings.GET_CLI_VOLUME_VDISK_UID_KEY: common_settings.SOURCE_VOLUME_ID,
             svc_settings.GET_CLI_VOLUME_FCMAP_KEY: svc_settings.DUMMY_INTERNAL_ID1},
            is_virt_snap_func=True,
            lsvdisk_call_count=1)
        self.assertIsNone(volume.source_id)

    def test_get_volume_hyperswap_has_no_source(self):
        target_cli_volume = self._get_mapped_target_cli_volume()
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._mock_cli_object(target_cli_volume)
        self._prepare_fcmaps_for_hyperswap()

        volume = self.svc.get_volume(common_settings.VOLUME_NAME, pool=common_settings.DUMMY_POOL1,
                                     is_virt_snap_func=False)

        self.assertIsNone(volume.source_id)

    def _prepare_stretched_volume_mock(self):
        cli_volume = self._get_cli_volume(pool_name=[svc_settings.POOL_MANY, common_settings.DUMMY_POOL1,
                                                     common_settings.DUMMY_POOL2])
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = cli_volume

    def test_get_volume_stretched_return_correct_pools(self):
        self._prepare_stretched_volume_mock()

        volume = self.svc.get_volume(common_settings.VOLUME_NAME, pool=common_settings.DUMMY_POOL1,
                                     is_virt_snap_func=False)

        self.assertEqual(common_settings.STRETCHED_POOL, volume.pool)

    def test_get_volume_raise_exception(self):
        self._test_mediator_method_client_error(self.svc.get_volume, (common_settings.VOLUME_NAME,),
                                                self.svc.sdk.svc_info_api.lsvdisk_post, Exception, Exception)

    def test_get_volume_returns_nothing(self):
        vol_ret = Munch({})
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = vol_ret
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.get_volume(common_settings.VOLUME_NAME, pool=common_settings.DUMMY_POOL1, is_virt_snap_func=False)

    def _test_create_volume_mkvolume_cli_failure_error(self, error_message_id, expected_error,
                                                       volume_name=common_settings.VOLUME_NAME):
        self._test_mediator_method_client_cli_failure_error(self.svc.create_volume,
                                                            (volume_name,
                                                             array_settings.DUMMY_CAPACITY_INT,
                                                             SPACE_EFFICIENCY_THIN,
                                                             common_settings.DUMMY_POOL1, None, None, None, None,
                                                             False),
                                                            self.svc.sdk.svc_task_api.mkvolume_post, error_message_id,
                                                            expected_error)

    def test_create_volume_raise_exceptions(self):
        self._test_mediator_method_client_error(self.svc.create_volume,
                                                (common_settings.VOLUME_NAME,
                                                 array_settings.DUMMY_CAPACITY_INT,
                                                 SPACE_EFFICIENCY_THIN,
                                                 common_settings.DUMMY_POOL1,
                                                 None, None, None,
                                                 None, False),
                                                self.svc.sdk.svc_task_api.mkvolume_post, Exception, Exception)
        self._test_create_volume_mkvolume_cli_failure_error(array_settings.DUMMY_ERROR_MESSAGE, SdkApiException)
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC8710E", array_errors.NotEnoughSpaceInPool)
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC6017E", array_errors.InvalidArgumentError,
                                                            svc_settings.INVALID_NAME_1)
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC6527E", array_errors.InvalidArgumentError,
                                                            svc_settings.INVALID_NAME_START_WITH_NUMBER)
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC5738E", array_errors.InvalidArgumentError,
                                                            svc_settings.INVALID_NAME_TOO_LONG)
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC6035E", array_errors.VolumeAlreadyExists)
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC5754E", array_errors.InvalidArgumentError)
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC9292E", array_errors.PoolDoesNotMatchSpaceEfficiency)
        self._test_create_volume_mkvolume_cli_failure_error("CMMVC9301E", array_errors.PoolDoesNotMatchSpaceEfficiency)

    def _test_create_volume_success(self, space_efficiency=None, source_id=None, source_type=None, volume_group=None,
                                    is_virt_snap_func=False, pool=None):
        self.svc.sdk.svc_task_api.mkvolume_post.return_value = Mock()
        vol_ret = self._get_cli_volume()
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = vol_ret
        pool_to_use = pool if pool else common_settings.DUMMY_POOL1
        volume = self.svc.create_volume(common_settings.VOLUME_NAME, array_settings.DUMMY_CAPACITY_INT,
                                        space_efficiency, pool_to_use,
                                        None,
                                        volume_group,
                                        self._mock_source_ids(source_id), source_type,
                                        is_virt_snap_func=is_virt_snap_func)

        self.assertEqual(array_settings.DUMMY_CAPACITY_INT, volume.capacity_bytes)
        self.assertEqual(ARRAY_TYPE_SVC, volume.array_type)
        self.assertEqual(common_settings.VOLUME_UID, volume.id)
        self.assertEqual(common_settings.INTERNAL_VOLUME_ID, volume.internal_id)

    def test_create_volume_with_thin_space_efficiency_success(self):
        self._test_create_volume_success(SPACE_EFFICIENCY_THIN)
        self.svc.sdk.svc_task_api.mkvolume_post.assert_called_once()

    def test_create_volume_with_compressed_space_efficiency_success(self):
        self._test_create_volume_success(SPACE_EFFICIENCY_COMPRESSED)
        self.svc.sdk.svc_task_api.mkvolume_post.assert_called_once()

    def test_create_volume_with_deduplicated_thin_space_efficiency_success(self):
        self._test_create_volume_success(SPACE_EFFICIENCY_DEDUPLICATED_THIN)
        self.svc.sdk.svc_task_api.mkvolume_post.assert_called_once()

    def test_create_volume_with_deduplicated_compressed_space_efficiency_success(self):
        self._test_create_volume_success(SPACE_EFFICIENCY_DEDUPLICATED_COMPRESSED)
        self.svc.sdk.svc_task_api.mkvolume_post.assert_called_once()

    def test_create_volume_with_deduplicated_backward_compatibility_space_efficiency_success(self):
        self._test_create_volume_success(SPACE_EFFICIENCY_DEDUPLICATED)
        self.svc.sdk.svc_task_api.mkvolume_post.assert_called_once()

    def _test_create_volume_with_default_space_efficiency_success(self, space_efficiency):
        self._test_create_volume_success(space_efficiency)
        self.svc.sdk.svc_task_api.mkvolume_post.assert_called_once()

    def _prepare_mocks_for_create_volume_mkvolumegroup(self):
        self.svc.sdk.svc_task_api.addsnapshot_post = Mock()
        self.svc.sdk.svc_task_api.mkvolumegroup_post = Mock()
        self.svc.sdk.svc_task_api.mkvolumegroup_post.return_value = {"id": "0"}
        vol_ret = self._get_cli_volume()
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = vol_ret

    def _mock_source_ids(self, internal_id=""):
        if internal_id:
            source_ids = MagicMock(spec=[svc_settings.SOURCE_IDS_UID, svc_settings.SOURCE_IDS_INTERNAL_ID])
            source_ids.internal_id = internal_id
            source_ids.uid = common_settings.VOLUME_UID  # must be a str for pydantic LsvdiskPostRequest
            return source_ids
        return None

    def test_create_volume_mkvolume_with_flashcopy_enable_no_source(self):
        self._test_create_volume_success(is_virt_snap_func=True)
        self.svc.sdk.svc_task_api.mkvolume_post.assert_called_once()

    def _test_create_volume_mkvolumegroup_success(self, source_type):
        self._prepare_mocks_for_create_volume_mkvolumegroup()
        if source_type == common_settings.VOLUME_OBJECT_TYPE:
            self._prepare_mocks_for_create_snapshot_addsnapshot(snapshot_id=common_settings.INTERNAL_SNAPSHOT_ID)
        self._test_create_volume_success(source_id=common_settings.INTERNAL_SNAPSHOT_ID, source_type=source_type,
                                         is_virt_snap_func=True)

        self.svc.sdk.svc_task_api.mkvolumegroup_post.assert_called_once()
        self.svc.sdk.svc_task_api.chvdisk_id_post.assert_called()
        self.svc.sdk.svc_task_api.rmvolumegroup_id_post.assert_called_once()

    def test_create_volume_mkvolumegroup_from_snapshot_success(self):
        self._test_create_volume_mkvolumegroup_success(source_type=common_settings.SNAPSHOT_OBJECT_TYPE)

    def test_create_volume_mkvolumegroup_from_volume_success(self):
        self._test_create_volume_mkvolumegroup_success(source_type=common_settings.VOLUME_OBJECT_TYPE)

    def test_is_stretch_pool_with_stretch_pool_returns_true(self):
        pools_to_return = [Munch({svc_settings.LSMDISKGRP_SITE_NAME_ATTR_KEY: svc_settings.DUMMY_POOL_SITE}),
                           Munch({svc_settings.LSMDISKGRP_SITE_NAME_ATTR_KEY: svc_settings.DUMMY_VOLUME_SITE1})]
        self.svc.sdk.svc_info_api.lsmdiskgrp_post.side_effect = self._mock_cli_objects(pools_to_return)

        result = self.svc._is_stretch_pool(common_settings.STRETCHED_POOL)

        self.assertTrue(result)

    def test_is_stretch_pool_with_non_stretch_pool_returns_false(self):
        result = self.svc._is_stretch_pool(common_settings.DUMMY_POOL1)

        self.assertFalse(result)

    def test_is_stretch_pool_with_same_site_pools_returns_false(self):
        pools_to_return = [Munch({svc_settings.LSMDISKGRP_SITE_NAME_ATTR_KEY: svc_settings.DUMMY_POOL_SITE}),
                           Munch({svc_settings.LSMDISKGRP_SITE_NAME_ATTR_KEY: svc_settings.DUMMY_POOL_SITE})]
        self.svc.sdk.svc_info_api.lsmdiskgrp_post.side_effect = self._mock_cli_objects(pools_to_return)

        result = self.svc._is_stretch_pool(common_settings.STRETCHED_POOL)

        self.assertFalse(result)

    def test_add_vdisk_copies_with_stretch_pool_adds_copies(self):
        self.svc.sdk.svc_task_api.addvdiskcopy_id_post = Mock()

        self.svc._add_vdisk_copies(True, common_settings.STRETCHED_POOL, common_settings.INTERNAL_VOLUME_ID)

        self.svc.sdk.svc_task_api.addvdiskcopy_id_post.assert_called_once()

    def test_add_vdisk_copies_with_non_stretch_pool_does_nothing(self):
        self.svc.sdk.svc_task_api.addvdiskcopy_id_post = Mock()

        self.svc._add_vdisk_copies(False, common_settings.DUMMY_POOL1, common_settings.INTERNAL_VOLUME_ID)

        self.svc.sdk.svc_task_api.addvdiskcopy_id_post.assert_not_called()

    def test_create_volume_mkvolumegroup_from_snapshot_with_stretch_pool_success(self):
        self._prepare_mocks_for_create_volume_mkvolumegroup()
        self.svc.sdk.svc_task_api.addvdiskcopy_id_post = Mock()
        pools_to_return = [Munch({svc_settings.LSMDISKGRP_SITE_NAME_ATTR_KEY: svc_settings.DUMMY_POOL_SITE}),
                           Munch({svc_settings.LSMDISKGRP_SITE_NAME_ATTR_KEY: svc_settings.DUMMY_VOLUME_SITE1})]
        self.svc.sdk.svc_info_api.lsmdiskgrp_post.side_effect = self._mock_cli_objects(pools_to_return)

        self._test_create_volume_success(source_id=common_settings.INTERNAL_SNAPSHOT_ID,
                                         source_type=common_settings.SNAPSHOT_OBJECT_TYPE,
                                         is_virt_snap_func=True,
                                         pool=common_settings.STRETCHED_POOL)

        self.svc.sdk.svc_task_api.mkvolumegroup_post.assert_called_once()
        self.svc.sdk.svc_task_api.addvdiskcopy_id_post.assert_called_once()

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def test_create_volume_mkvolumegroup_with_rollback(self, mock_warning):
        mock_warning.return_value = False
        self._prepare_mocks_for_create_volume_mkvolumegroup()
        self.svc.sdk.svc_task_api.chvdisk_id_post.side_effect = ["", _sdk_error("CMMVC6035E")]
        with self.assertRaises(array_errors.VolumeAlreadyExists):
            self.svc.create_volume(common_settings.VOLUME_NAME, array_settings.DUMMY_CAPACITY_INT,
                                   svc_settings.DUMMY_SPACE_EFFICIENCY,
                                   common_settings.DUMMY_POOL1, None, None,
                                   self._mock_source_ids(common_settings.INTERNAL_SNAPSHOT_ID),
                                   common_settings.SNAPSHOT_OBJECT_TYPE,
                                   is_virt_snap_func=True)
        self.svc.sdk.svc_task_api.rmvdisk_id_post.assert_called_once()
        self.svc.sdk.svc_task_api.rmvolumegroup_id_post.assert_called()

    def test_create_volume_with_empty_string_space_efficiency_success(self):
        self._test_create_volume_with_default_space_efficiency_success("")

    def test_create_volume_with_thick_space_efficiency_success(self):
        self._test_create_volume_with_default_space_efficiency_success(
            SPACE_EFFICIENCY_THICK)

    def _test_delete_volume_rmvolume_cli_failure_error(self, error_message_id, expected_error,
                                                       volume_name=common_settings.VOLUME_NAME):
        self._test_mediator_method_client_cli_failure_error(self.svc.delete_volume, (volume_name,),
                                                            self.svc.sdk.svc_task_api.rmvdisk_id_post, error_message_id,
                                                            expected_error)

    def test_delete_volume_return_volume_delete_errors(self):
        self._prepare_mocks_for_delete_volume()
        self._test_delete_volume_rmvolume_cli_failure_error("CMMVC5753E", array_errors.ObjectNotFoundError)
        self._test_delete_volume_rmvolume_cli_failure_error("CMMVC8957E", array_errors.ObjectNotFoundError)
        self._test_delete_volume_rmvolume_cli_failure_error(array_settings.DUMMY_ERROR_MESSAGE, SdkApiException)

    def test_delete_volume_has_snapshot_fcmaps_not_removed(self):
        self._prepare_mocks_for_delete_volume()
        fcmaps_as_target = []
        fcmaps = self.fcmaps
        fcmaps[0].copy_rate = svc_settings.DUMMY_ZERO_COPY_RATE
        fcmaps_as_source = fcmaps
        self.svc.sdk.svc_info_api.lsfcmap_post.side_effect = [fcmaps_as_target, fcmaps_as_source]
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.svc.delete_volume(common_settings.VOLUME_NAME)

    def test_delete_volume_still_copy_fcmaps_not_removed(self):
        self._prepare_mocks_for_delete_volume()
        fcmaps_as_target = []
        fcmaps = self.fcmaps
        fcmaps[0].status = svc_settings.DUMMY_FCMAP_BAD_STATUS
        fcmaps_as_source = fcmaps
        self.svc.sdk.svc_info_api.lsfcmap_post.side_effect = [fcmaps_as_target, fcmaps_as_source]
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.svc.delete_volume(common_settings.VOLUME_NAME)

    def _prepare_fcmaps_for_hyperswap(self):
        self.fcmaps_as_target[0].rc_controlled = svc_settings.YES_VALUE_ALIAS
        fcmaps_as_target = self.fcmaps_as_target
        self.fcmaps[0].rc_controlled = svc_settings.YES_VALUE_ALIAS
        fcmaps_as_source = self.fcmaps
        self.svc.sdk.svc_info_api.lsfcmap_post.side_effect = [fcmaps_as_target, fcmaps_as_source]

    def test_delete_volume_does_not_remove_hyperswap_fcmap(self):
        self._prepare_mocks_for_delete_volume()
        self._prepare_fcmaps_for_hyperswap()
        self.svc.delete_volume(common_settings.VOLUME_NAME)

        self.svc.sdk.svc_task_api.rmfcmap_id_post.assert_not_called()

    def test_delete_volume_has_clone_fcmaps_removed(self):
        self._prepare_mocks_for_delete_volume()
        fcmaps_as_target = []
        fcmaps_as_source = self.fcmaps_as_source
        self.svc.sdk.svc_info_api.lsfcmap_post.side_effect = [fcmaps_as_target, fcmaps_as_source]
        self.svc.delete_volume(common_settings.VOLUME_NAME)
        self.svc.sdk.svc_task_api.rmfcmap_id_post.assert_called_once()

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def test_delete_volume_has_clone_rmfcmap_raise_error(self, mock_warning):
        self._prepare_mocks_for_delete_volume()
        mock_warning.return_value = False
        fcmaps_as_target = []
        fcmaps_as_source = self.fcmaps_as_source
        self.svc.sdk.svc_info_api.lsfcmap_post.side_effect = [fcmaps_as_target, fcmaps_as_source]
        self.svc.sdk.svc_task_api.rmfcmap_id_post.side_effect = [_sdk_error(array_settings.DUMMY_ERROR_MESSAGE)]
        with self.assertRaises(SdkApiException):
            self.svc.delete_volume(common_settings.VOLUME_NAME)

    def _prepare_mocks_for_delete_volume(self):
        cli_volume = self._get_cli_volume()
        cli_volume.FC_id = svc_settings.VOLUME_FC_ID_MANY

        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._mock_cli_object(cli_volume)

    def test_delete_volume_success(self):
        self._prepare_mocks_for_delete_volume()
        self.svc.delete_volume(common_settings.VOLUME_NAME)

    def test_copy_to_existing_volume_from_source_success(self):
        self.svc.copy_to_existing_volume(common_settings.VOLUME_UID, common_settings.SOURCE_VOLUME_ID,
                                         array_settings.DUMMY_CAPACITY_INT,
                                         array_settings.DUMMY_SMALL_CAPACITY_INT)
        self.svc.sdk.svc_task_api.mkfcmap_post.assert_called_once()
        self.svc.sdk.svc_task_api.startfcmap_id_post.assert_called_once()

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def _test_copy_to_existing_volume_raise_errors(self, mock_warning, client_return_value, expected_error):
        mock_warning.return_value = False
        self.svc.sdk.svc_info_api.lsvdisk_post.side_effect = [client_return_value, client_return_value]
        with self.assertRaises(expected_error):
            self.svc.copy_to_existing_volume(common_settings.VOLUME_UID, common_settings.SOURCE_VOLUME_ID,
                                             array_settings.DUMMY_CAPACITY_INT,
                                             array_settings.DUMMY_SMALL_CAPACITY_INT)

    def test_copy_to_existing_volume_raise_not_found(self):
        self._test_copy_to_existing_volume_raise_errors(client_return_value=None,
                                                        expected_error=array_errors.ObjectNotFoundError)

    def test_copy_to_existing_volume_raise_illegal_object_id(self):
        self._test_copy_to_existing_volume_raise_errors(client_return_value=_sdk_error("CMMVC6017E"),
                                                        expected_error=array_errors.InvalidArgumentError)
        self._test_copy_to_existing_volume_raise_errors(client_return_value=_sdk_error("CMMVC5741E"),
                                                        expected_error=array_errors.InvalidArgumentError)

    @staticmethod
    def _mock_cli_object(cli_object):
        return cli_object

    @classmethod
    def _mock_cli_objects(cls, cli_objects):
        return map(cls._mock_cli_object, cli_objects)

    @staticmethod
    def _get_cli_volume(with_deduplicated_copy=True, name=common_settings.SOURCE_VOLUME_NAME,
                        pool_name=common_settings.DUMMY_POOL1,
                        vdisk_uid=common_settings.VOLUME_UID,
                        fc_id="", capacity=array_settings.DUMMY_CAPACITY_STR,
                        thick=False,
                        replication_mode=None,
                        fc_map_count=EAR_VOLUME_FC_MAP_COUNT,
                        in_volume_group=False,
                        remote_volume_uid="",
                        remote_internal_id="",
                        partition_name="",
                        remote_volume_uid_supported=True):
        deduplicated_copy = svc_settings.NO_VALUE_ALIAS
        compressed_copy = svc_settings.NO_VALUE_ALIAS
        se_copy = svc_settings.NO_VALUE_ALIAS
        volume_group_id = ""
        volume_group_name = ""
        if in_volume_group:
            volume_group_id = svc_settings.DUMMY_VOLUME_GROUP_ID
            volume_group_name = common_settings.VOLUME_GROUP_NAME
        if with_deduplicated_copy:
            deduplicated_copy = YES
            compressed_copy = YES
        elif not thick:
            se_copy = YES
        cli_volume = Munch({svc_settings.VOLUME_VDISK_UID_ATTR_KEY: vdisk_uid,
                            array_settings.VOLUME_ID_ATTR_KEY: common_settings.INTERNAL_VOLUME_ID,
                            array_settings.VOLUME_NAME_ATTR_KEY: name,
                            svc_settings.VOLUME_CAPACITY_ATTR_KEY: capacity,
                            svc_settings.VOLUME_MDISK_GRP_NAME_ATTR_KEY: pool_name,
                            svc_settings.VOLUME_IO_GROUP_NAME_ATTR_KEY: common_settings.DUMMY_IO_GROUP,
                            svc_settings.VOLUME_FC_ID_ATTR_KEY: fc_id,
                            svc_settings.VOLUME_SE_COPY_ATTR_KEY: se_copy,
                            svc_settings.VOLUME_DEDUPLICATED_COPY_ATTR_KEY: deduplicated_copy,
                            svc_settings.VOLUME_COMPRESSED_COPY_ATTR_KEY: compressed_copy,
                            svc_settings.VOLUME_GROUP_ID_ATTR_KEY: volume_group_id,
                            svc_settings.VOLUME_REPLICATION_MODE_ATTR_KEY: replication_mode,
                            svc_settings.VOLUME_FC_MAP_COUNT_ATTR_KEY: fc_map_count,
                            svc_settings.VOLUME_VG_NAME_ATTR_KEY: volume_group_name,
                            'location2_volume_id': remote_internal_id if not partition_name else '',
                            'location3_volume_id': remote_internal_id if partition_name else '',
                            })
        if remote_volume_uid_supported:
            cli_volume['remote_volume_uid'] = remote_volume_uid
        return cli_volume

    @staticmethod
    def _get_cli_snapshot(snapshot_id=common_settings.INTERNAL_SNAPSHOT_ID):
        return Munch({svc_settings.SNAPSHOT_ID_ATTR_KEY: snapshot_id,
                      svc_settings.SNAPSHOT_NAME_ATTR_KEY: common_settings.SNAPSHOT_NAME,
                      svc_settings.SNAPSHOT_VOLUME_ID_ATTR_KEY: common_settings.INTERNAL_VOLUME_ID,
                      svc_settings.SNAPSHOT_VOLUME_NAME_ATTR_KEY: common_settings.VOLUME_NAME,
                      svc_settings.VOLUME_VG_NAME_ATTR_KEY: None
                      })

    @classmethod
    def _get_mapless_target_cli_volume(cls):
        target_cli_volume = cls._get_cli_volume()
        target_cli_volume.vdisk_UID = common_settings.SNAPSHOT_VOLUME_UID
        target_cli_volume.name = common_settings.SNAPSHOT_NAME
        return target_cli_volume

    @classmethod
    def _get_mapped_target_cli_volume(cls):
        target_cli_volume = cls._get_mapless_target_cli_volume()
        target_cli_volume.FC_id = svc_settings.DUMMY_FCMAP_ID
        return target_cli_volume

    def _prepare_lsvdisk_to_raise_not_found_error(self, mock_warning):
        mock_warning.return_value = False
        self.svc.sdk.svc_info_api.lsvdisk_post.side_effect = [
            _sdk_error("CMMVC5753E")]

    def _prepare_lsvdisk_to_return_mapless_target_volume(self):
        mapless_target_cli_volume = self._get_mapless_target_cli_volume()
        mapless_target_cli_volume_mock = self._mock_cli_object(mapless_target_cli_volume)
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = mapless_target_cli_volume_mock

    def _prepare_lsvdisk_to_return_none(self):
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._mock_cli_object(None)

    def _prepare_mocks_for_delete_snapshot(self):
        target_cli_volume = self._get_mapped_target_cli_volume()
        target_cli_volume.FC_id = svc_settings.VOLUME_FC_ID_MANY
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._mock_cli_object(target_cli_volume)

    def _prepare_mocks_for_get_snapshot(self):
        self._prepare_mocks_for_delete_snapshot()
        self.fcmaps[0].copy_rate = svc_settings.DUMMY_ZERO_COPY_RATE

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def test_get_snapshot_not_exist_return_none(self, mock_warning):
        self._prepare_lsvdisk_to_raise_not_found_error(mock_warning)

        snapshot = self.svc.get_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME,
                                         pool=common_settings.DUMMY_POOL1, is_virt_snap_func=False)

        self.assertIsNone(snapshot)

    def _test_get_snapshot_cli_failure_error(self, snapshot_name, client_method, error_message_id, expected_error,
                                             is_virt_snap_func=False):
        self._test_mediator_method_client_cli_failure_error(self.svc.get_snapshot,
                                                            (common_settings.VOLUME_UID, snapshot_name,
                                                             common_settings.DUMMY_POOL1, is_virt_snap_func),
                                                            client_method, error_message_id, expected_error)

    def _test_get_snapshot_illegal_name_cli_failure_errors(self, client_method, is_virt_snap_func=False):
        self._test_get_snapshot_cli_failure_error(svc_settings.INVALID_NAME_1, client_method, "CMMVC6017E",
                                                  array_errors.InvalidArgumentError, is_virt_snap_func)
        self._test_get_snapshot_cli_failure_error(svc_settings.INVALID_NAME_START_WITH_NUMBER, client_method,
                                                  "CMMVC5703E",
                                                  array_errors.InvalidArgumentError, is_virt_snap_func)

    def test_get_snapshot_lsvdisk_cli_failure_errors(self):
        client_method = self.svc.sdk.svc_info_api.lsvdisk_post
        self._test_get_snapshot_illegal_name_cli_failure_errors(client_method)
        self.svc.sdk.svc_info_api.lsvdisk_post.assert_called()

    def test_get_snapshot_has_no_fc_id_raise_error(self):
        self._prepare_lsvdisk_to_return_mapless_target_volume()

        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.svc.get_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME,
                                  pool=common_settings.DUMMY_POOL1, is_virt_snap_func=False)

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def test_get_snapshot_get_fcmap_not_exist_raise_error(self, mock_warning):
        target_cli_volume = self._get_mapped_target_cli_volume()
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._mock_cli_object(target_cli_volume)
        mock_warning.return_value = False
        self.svc.sdk.svc_info_api.lsfcmap_post.side_effect = [
            _sdk_error("CMMVC5753E")]

        with self.assertRaises(SdkApiException):
            self.svc.get_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME,
                                  pool=common_settings.DUMMY_POOL1, is_virt_snap_func=False)

    def test_get_snapshot_non_zero_copy_rate(self):
        self._prepare_mocks_for_get_snapshot()
        self.fcmaps[0].copy_rate = svc_settings.DUMMY_COPY_RATE
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.svc.get_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME,
                                  pool=common_settings.DUMMY_POOL1, is_virt_snap_func=False)

    def test_get_snapshot_no_fcmap_as_target(self):
        self._prepare_mocks_for_get_snapshot()
        self.svc.sdk.svc_info_api.lsfcmap_post.return_value = []
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.svc.get_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME,
                                  pool=common_settings.DUMMY_POOL1, is_virt_snap_func=False)

    def test_get_snapshot_lsvdisk_success(self):
        self._prepare_mocks_for_get_snapshot()
        snapshot = self.svc.get_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME,
                                         pool=common_settings.DUMMY_POOL1, is_virt_snap_func=False)
        self.assertEqual(common_settings.SNAPSHOT_NAME, snapshot.name)

    def test_get_snapshot_lsvolumesnapshot_cli_failure_errors(self):
        self.svc.sdk.svc_task_api.addsnapshot_post = Mock()
        client_method = self.svc.sdk.svc_info_api.lsvolumesnapshot_post
        self._test_get_snapshot_illegal_name_cli_failure_errors(client_method, True)
        self.svc.sdk.svc_info_api.lsvolumesnapshot_post.assert_called()

    def _prepare_mocks_for_get_snapshot_lsvolumesnapshot(self):
        self.svc.sdk.svc_task_api.addsnapshot_post = Mock()
        self.svc.sdk.svc_info_api.lsvolumesnapshot_post.return_value = self._mock_cli_object(self._get_cli_snapshot())
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._mock_cli_object(self._get_cli_volume())

    def _get_filtervalue(self, key, value):
        return svc_settings.FILTERVALUE_DELIMITER.join([key, value])

    def test_get_snapshot_lsvolumesnapshot_success(self):
        self._prepare_mocks_for_get_snapshot_lsvolumesnapshot()
        snapshot = self.svc.get_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME,
                                         pool=common_settings.DUMMY_POOL1, is_virt_snap_func=True)
        self.assertEqual(common_settings.SNAPSHOT_NAME, snapshot.name)
        filtervalue = self._get_filtervalue(svc_settings.SNAPSHOT_NAME_ATTR_KEY, common_settings.SNAPSHOT_NAME)
        self.svc.sdk.svc_info_api.lsvolumesnapshot_post.assert_called_once()
        self.svc.sdk.svc_info_api.lsvdisk_post.assert_called_once()

    def test_get_snapshot_lsvolumesnapshot_not_supported_error(self):
        with self.assertRaises(array_errors.VirtSnapshotFunctionNotSupportedMessage):
            self.svc.get_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME,
                                  pool=common_settings.DUMMY_POOL1, is_virt_snap_func=True)

    def test_get_object_by_id_snapshot_has_no_fcmap_id_raise_error(self):
        self._prepare_lsvdisk_to_return_mapless_target_volume()
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.svc.get_object_by_id(common_settings.SNAPSHOT_VOLUME_UID, common_settings.SNAPSHOT_OBJECT_TYPE)

    def test_get_object_by_id_return_none(self):
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = None
        returned_value = self.svc.get_object_by_id(common_settings.SNAPSHOT_VOLUME_UID,
                                                   common_settings.SNAPSHOT_OBJECT_TYPE)
        self.assertEqual(None, returned_value)

    def test_get_object_by_id_snapshot_success(self):
        self._prepare_mocks_for_get_snapshot()

        snapshot = self.svc.get_object_by_id(common_settings.SNAPSHOT_VOLUME_UID, common_settings.SNAPSHOT_OBJECT_TYPE)
        self.assertEqual(common_settings.SNAPSHOT_NAME, snapshot.name)
        self.svc.sdk.svc_info_api.lsvdisk_post.assert_called()

    def test_get_object_by_id_snapshot_without_volume_group_name_success(self):
        # Regression test: on SVC < 8.6.0, the concise lsvdisk output does not include
        # volume_group_name. Ensure _get_partition_name_of_cli_volume does not crash.
        self._prepare_mocks_for_get_snapshot()
        target_cli_volume = self._get_mapped_target_cli_volume()
        del target_cli_volume[svc_settings.VOLUME_VG_NAME_ATTR_KEY]
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._mock_cli_object(target_cli_volume)

        snapshot = self.svc.get_object_by_id(common_settings.SNAPSHOT_VOLUME_UID, common_settings.SNAPSHOT_OBJECT_TYPE)
        self.assertEqual(common_settings.SNAPSHOT_NAME, snapshot.name)

    def test_get_object_by_id_snapshot_virt_snap_func_enabled_success(self):
        self._prepare_mocks_for_get_snapshot()
        self._prepare_mocks_for_lsvolumesnapshot()
        snapshot = self.svc.get_object_by_id(common_settings.SNAPSHOT_NAME, common_settings.SNAPSHOT_OBJECT_TYPE,
                                             is_virt_snap_func=True)
        self.assertEqual(common_settings.SNAPSHOT_NAME, snapshot.name)
        self.svc.sdk.svc_info_api.lsvdisk_post.assert_called_once()
        self.svc.sdk.svc_info_api.lsvolumesnapshot_id_post.assert_called_once()

    def test_get_object_by_id_volume_success(self):
        target_cli_volume = self._get_mapped_target_cli_volume()
        target_cli_volume.name = common_settings.VOLUME_NAME
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._mock_cli_object(target_cli_volume)
        volume = self.svc.get_object_by_id(common_settings.VOLUME_UID, common_settings.VOLUME_OBJECT_TYPE)
        self.assertEqual(common_settings.VOLUME_NAME, volume.name)

    def _get_custom_cli_volume(self, support_deduplicated_copy, with_deduplicated_copy,
                               name=common_settings.SOURCE_VOLUME_NAME,
                               pool_name=common_settings.DUMMY_POOL1,
                               replication_mode=None,
                               fc_map_count=EAR_VOLUME_FC_MAP_COUNT):
        volume = self._get_cli_volume(with_deduplicated_copy, name=name, pool_name=pool_name,
                                      replication_mode=replication_mode, fc_map_count=fc_map_count)
        if not support_deduplicated_copy:
            del volume.deduplicated_copy
        return volume

    def _prepare_mocks_for_create_snapshot_mkvolume(self, support_deduplicated_copy=True,
                                                    source_has_deduplicated_copy=False, different_pool_site=False,
                                                    is_source_stretched=False):
        self.svc.sdk.svc_task_api.mkvolume_post.return_value = Mock()
        self.svc.sdk.svc_task_api.mkfcmap_post.return_value = Mock()
        # Default pool mock: return a pool with no site so _get_pool_site succeeds (site_name='')
        self.svc.sdk.svc_info_api.lsmdiskgrp_post.return_value = Munch({'site_name': ''})
        pool = [svc_settings.POOL_MANY, common_settings.DUMMY_POOL1,
                common_settings.DUMMY_POOL2] if is_source_stretched else common_settings.DUMMY_POOL1
        source_volume_to_copy_from = self._get_custom_cli_volume(support_deduplicated_copy,
                                                                 source_has_deduplicated_copy,
                                                                 pool_name=pool)
        volumes_to_return = [source_volume_to_copy_from, source_volume_to_copy_from]

        if different_pool_site:
            if is_source_stretched:
                pools_to_return = [Munch({svc_settings.LSMDISKGRP_SITE_NAME_ATTR_KEY: svc_settings.DUMMY_POOL_SITE}),
                                   Munch({svc_settings.LSMDISKGRP_SITE_NAME_ATTR_KEY: svc_settings.DUMMY_VOLUME_SITE1}),
                                   Munch({svc_settings.LSMDISKGRP_SITE_NAME_ATTR_KEY: svc_settings.DUMMY_POOL_SITE})]
                self.svc.sdk.svc_info_api.lsmdiskgrp_post.side_effect = self._mock_cli_objects(pools_to_return)
            else:
                pools_to_return = [Munch({svc_settings.LSMDISKGRP_SITE_NAME_ATTR_KEY: svc_settings.DUMMY_POOL_SITE}),
                                   Munch({svc_settings.LSMDISKGRP_SITE_NAME_ATTR_KEY: svc_settings.DUMMY_VOLUME_SITE1}),
                                   Munch({svc_settings.LSMDISKGRP_SITE_NAME_ATTR_KEY: svc_settings.DUMMY_VOLUME_SITE2}),
                                   Munch({svc_settings.LSMDISKGRP_SITE_NAME_ATTR_KEY: svc_settings.DUMMY_POOL_SITE})]
                self.svc.sdk.svc_info_api.lsmdiskgrp_post.side_effect = self._mock_cli_objects(pools_to_return)

                auxiliary_volumes = [self._get_cli_volume(name=common_settings.TARGET_VOLUME_NAME,
                                                          pool_name=common_settings.DUMMY_POOL2),
                                     self._get_custom_cli_volume(support_deduplicated_copy,
                                                                 source_has_deduplicated_copy,
                                                                 name=common_settings.VOLUME_NAME,
                                                                 pool_name=common_settings.DUMMY_POOL1)]
                volumes_to_return.extend(auxiliary_volumes)

                rcrelationships_to_return = [Munch({
                    svc_settings.LSRCRELATIONSHIP_AUX_VOLUME_ATTR_KEY: common_settings.TARGET_VOLUME_NAME}),
                    Munch({svc_settings.LSRCRELATIONSHIP_AUX_VOLUME_ATTR_KEY: common_settings.VOLUME_NAME})]
                self.svc.sdk.svc_info_api.lsrcrelationship_post.return_value = rcrelationships_to_return

        target_volume_after_creation = self._get_mapless_target_cli_volume()
        target_volume_after_mapping = self._get_mapped_target_cli_volume()
        target_volume_for_rollback = self._get_mapped_target_cli_volume()
        volumes_to_return.extend([target_volume_after_creation, target_volume_after_mapping,
                                  target_volume_for_rollback])

        self.svc.sdk.svc_info_api.lsvdisk_post.side_effect = self._mock_cli_objects(volumes_to_return)
        self.svc.sdk.svc_task_api.startfcmap_id_post.return_value = Mock()

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def test_create_snapshot_create_volume_error(self, mock_warning):
        source_cli_volume = self._get_cli_volume()
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._mock_cli_object(source_cli_volume)
        mock_warning.return_value = False
        self.svc.sdk.svc_task_api.mkvolume_post.side_effect = [
            _sdk_error(array_settings.DUMMY_ERROR_MESSAGE)]

        with self.assertRaises(SdkApiException):
            self.svc.create_snapshot(common_settings.SOURCE_VOLUME_ID, common_settings.SNAPSHOT_NAME,
                                     space_efficiency=None, pool=common_settings.DUMMY_POOL1,
                                     is_virt_snap_func=False)

    def _test_create_snapshot_lsvdisk_cli_failure_error(self, volume_id, snapshot_name, error_message_id,
                                                        expected_error, space_efficiency=None, pool=None):
        self._test_mediator_method_client_cli_failure_error(self.svc.create_snapshot,
                                                            (volume_id, snapshot_name, space_efficiency, pool, False),
                                                            self.svc.sdk.svc_info_api.lsvdisk_post, error_message_id,
                                                            expected_error)

    def test_create_snapshot_lsvdisk_cli_failure_errors(self):
        self._test_create_snapshot_lsvdisk_cli_failure_error(svc_settings.INVALID_NAME_1, common_settings.SNAPSHOT_NAME,
                                                             "CMMVC6017E",
                                                             array_errors.InvalidArgumentError)
        self._test_create_snapshot_lsvdisk_cli_failure_error(svc_settings.INVALID_NAME_SYMBOLS,
                                                             common_settings.SNAPSHOT_NAME,
                                                             "CMMVC5741E",
                                                             array_errors.InvalidArgumentError)

    def test_create_snapshot_source_not_found_error(self):
        self.svc.sdk.svc_info_api.lsvdisk_post.side_effect = [None, None]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.create_snapshot(common_settings.SOURCE_VOLUME_ID, common_settings.SNAPSHOT_NAME,
                                     space_efficiency=None, pool=common_settings.DUMMY_POOL1,
                                     is_virt_snap_func=False)

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def test_create_snapshot_create_fcmap_error(self, mock_warning):
        self._prepare_mocks_for_create_snapshot_mkvolume()
        mock_warning.return_value = False
        self.svc.sdk.svc_task_api.mkfcmap_post.side_effect = [
            _sdk_error(array_settings.DUMMY_ERROR_MESSAGE)]

        with self.assertRaises(SdkApiException):
            self.svc.create_snapshot(common_settings.SOURCE_VOLUME_ID, common_settings.SNAPSHOT_NAME,
                                     space_efficiency=None, pool=common_settings.DUMMY_POOL1,
                                     is_virt_snap_func=False)

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def test_create_snapshot_start_fcmap_error(self, mock_warning):
        self._prepare_mocks_for_create_snapshot_mkvolume()
        mock_warning.return_value = False
        self.svc.sdk.svc_task_api.startfcmap_id_post.side_effect = [
            _sdk_error(array_settings.DUMMY_ERROR_MESSAGE)]

        with self.assertRaises(SdkApiException):
            self.svc.create_snapshot(common_settings.SOURCE_VOLUME_ID, common_settings.SNAPSHOT_NAME,
                                     space_efficiency=None, pool=common_settings.DUMMY_POOL1,
                                     is_virt_snap_func=False)

    def test_create_snapshot_mkvolume_success(self):
        self._prepare_mocks_for_create_snapshot_mkvolume()

        snapshot = self.svc.create_snapshot(common_settings.SOURCE_VOLUME_ID, common_settings.SNAPSHOT_NAME,
                                            space_efficiency=None, pool=common_settings.DUMMY_POOL1,
                                            is_virt_snap_func=False)

        self.assertEqual(array_settings.DUMMY_CAPACITY_INT, snapshot.capacity_bytes)
        self.assertEqual(ARRAY_TYPE_SVC, snapshot.array_type)
        self.assertEqual(common_settings.SNAPSHOT_VOLUME_UID, snapshot.id)

    def test_create_snapshot_with_different_pool_success(self):
        self._prepare_mocks_for_create_snapshot_mkvolume()

        self.svc.create_snapshot(common_settings.SOURCE_VOLUME_ID, common_settings.SNAPSHOT_NAME, space_efficiency=None,
                                 pool=common_settings.DUMMY_POOL2,
                                 is_virt_snap_func=False)
        self.svc.sdk.svc_task_api.mkvolume_post.assert_called_once()

    def test_create_snapshot_for_hyperswap_volume_with_different_site_success(self):
        self._prepare_mocks_for_create_snapshot_mkvolume(different_pool_site=True)

        self.svc.create_snapshot(common_settings.SOURCE_VOLUME_ID, common_settings.SNAPSHOT_NAME, space_efficiency=None,
                                 pool=common_settings.DUMMY_POOL2,
                                 is_virt_snap_func=False)
        self.svc.sdk.svc_task_api.mkfcmap_post.assert_called_once()

    def test_create_snapshot_for_stretched_volume_with_different_site_success(self):
        self._prepare_mocks_for_create_snapshot_mkvolume(different_pool_site=True, is_source_stretched=True)

        self.svc.create_snapshot(common_settings.SOURCE_VOLUME_ID, common_settings.SNAPSHOT_NAME, space_efficiency=None,
                                 pool=common_settings.DUMMY_POOL2,
                                 is_virt_snap_func=False)
        self.svc.sdk.svc_task_api.mkfcmap_post.assert_called_once()

    def test_create_snapshot_for_stretched_volume_implicit_pool_success(self):
        self._prepare_mocks_for_create_snapshot_mkvolume(is_source_stretched=True)

        self.svc.create_snapshot(common_settings.SOURCE_VOLUME_ID, common_settings.SNAPSHOT_NAME, space_efficiency=None,
                                 pool=None,
                                 is_virt_snap_func=False)
        self.svc.sdk.svc_task_api.mkvolume_post.assert_called_once()
        self.svc.sdk.svc_task_api.mkfcmap_post.assert_called_once()

    def test_create_snapshot_as_stretched_success(self):
        self._prepare_mocks_for_create_snapshot_mkvolume()

        self.svc.create_snapshot(common_settings.SOURCE_VOLUME_ID, common_settings.SNAPSHOT_NAME, space_efficiency=None,
                                 pool=common_settings.STRETCHED_POOL,
                                 is_virt_snap_func=False)
        self.svc.sdk.svc_task_api.mkvolume_post.assert_called_once()
        self.svc.sdk.svc_task_api.mkfcmap_post.assert_called_once()

    def test_create_snapshot_with_specified_source_volume_space_efficiency_success(self):
        self._prepare_mocks_for_create_snapshot_mkvolume(source_has_deduplicated_copy=True)

        self.svc.create_snapshot(common_settings.SOURCE_VOLUME_ID, common_settings.SNAPSHOT_NAME, space_efficiency=None,
                                 pool=None,
                                 is_virt_snap_func=False)
        self.svc.sdk.svc_task_api.mkvolume_post.assert_called_once()

    def test_create_snapshot_with_different_space_efficiency_success(self):
        self._prepare_mocks_for_create_snapshot_mkvolume(source_has_deduplicated_copy=True)

        self.svc.create_snapshot(common_settings.SOURCE_VOLUME_ID, common_settings.SNAPSHOT_NAME,
                                 space_efficiency=SPACE_EFFICIENCY_THIN, pool=None,
                                 is_virt_snap_func=False)
        self.svc.sdk.svc_task_api.mkvolume_post.assert_called_once()

    def test_create_snapshot_no_deduplicated_copy_success(self):
        self._prepare_mocks_for_create_snapshot_mkvolume(support_deduplicated_copy=False)

        snapshot = self.svc.create_snapshot(common_settings.SOURCE_VOLUME_ID, common_settings.SNAPSHOT_NAME,
                                            space_efficiency=None, pool=common_settings.DUMMY_POOL1,
                                            is_virt_snap_func=False)

        self.assertEqual(array_settings.DUMMY_CAPACITY_INT, snapshot.capacity_bytes)
        self.assertEqual(ARRAY_TYPE_SVC, snapshot.array_type)
        self.assertEqual(common_settings.SNAPSHOT_VOLUME_UID, snapshot.id)

    def _prepare_mocks_for_lsvolumesnapshot(self, snapshot_id=common_settings.INTERNAL_VOLUME_ID):
        self.svc.sdk.svc_info_api.lsvolumesnapshot_id_post = Mock()
        self.svc.sdk.svc_info_api.lsvolumesnapshot_id_post.return_value = self._mock_cli_object(
            self._get_cli_snapshot(snapshot_id))

    def _prepare_mocks_for_create_snapshot_addsnapshot(self, snapshot_id=common_settings.INTERNAL_VOLUME_ID,
                                                       is_ear_supported=False,
                                                       replication_mode=None,
                                                       fc_map_count=EAR_VOLUME_FC_MAP_COUNT):
        self.svc.sdk.svc_task_api.addsnapshot_post = Mock()
        # Default pool mock so _get_pool_site succeeds
        self.svc.sdk.svc_info_api.lsmdiskgrp_post.return_value = Munch({'site_name': ''})
        if is_ear_supported:
            self.svc.sdk.svc_task_api.chvolumereplicationinternals_id_post = Mock()
        source_volume_to_copy_from = self._get_custom_cli_volume(False, False, pool_name=common_settings.DUMMY_POOL1,
                                                                 replication_mode=replication_mode,
                                                                 fc_map_count=fc_map_count)
        volumes_to_return = [source_volume_to_copy_from, source_volume_to_copy_from, source_volume_to_copy_from]
        self.svc.sdk.svc_info_api.lsvdisk_post.side_effect = self._mock_cli_objects(volumes_to_return)
        self.svc.sdk.svc_task_api.addsnapshot_post.return_value = {"id": "0"}
        self._prepare_mocks_for_lsvolumesnapshot(snapshot_id)

    def _test_create_snapshot_addsnapshot_success(self, pool=common_settings.DUMMY_POOL1,
                                                  is_ear_supported=False):
        self._prepare_mocks_for_create_snapshot_addsnapshot(is_ear_supported=is_ear_supported)
        snapshot = self.svc.create_snapshot(common_settings.SOURCE_VOLUME_ID, common_settings.SNAPSHOT_NAME,
                                            space_efficiency=None, pool=pool,
                                            is_virt_snap_func=True)
        if not pool:
            pool = common_settings.DUMMY_POOL1
        self.assertEqual(array_settings.DUMMY_CAPACITY_INT, snapshot.capacity_bytes)
        self.svc.sdk.svc_task_api.addsnapshot_post.assert_called_once()
        self.svc.sdk.svc_info_api.lsvolumesnapshot_id_post.assert_called_once()
        self.assertEqual(ARRAY_TYPE_SVC, snapshot.array_type)
        self.assertEqual("", snapshot.id)
        self.assertEqual(common_settings.INTERNAL_VOLUME_ID, snapshot.internal_id)

    def test_create_snapshot_addsnapshot_success(self):
        self._test_create_snapshot_addsnapshot_success()

    def test_create_snapshot_addsnapshot_no_pool_success(self):
        self._test_create_snapshot_addsnapshot_success(pool="")

    def test_create_snapshot_addsnapshot_different_pool_success(self):
        self._test_create_snapshot_addsnapshot_success(pool=common_settings.DUMMY_POOL2)

    def test_create_snapshot_addsnapshot_not_supported_error(self):
        with self.assertRaises(array_errors.VirtSnapshotFunctionNotSupportedMessage):
            self.svc.create_snapshot(common_settings.SOURCE_VOLUME_ID, common_settings.SNAPSHOT_NAME,
                                     space_efficiency=None, pool=common_settings.DUMMY_POOL1,
                                     is_virt_snap_func=True)

    def test_create_snapshot_addsnapshot_ear_success(self):
        self._test_create_snapshot_addsnapshot_success(is_ear_supported=True)

    def test_create_snapshot_addsnapshot_ear_wrong_fc_map_count_error(self):
        self._prepare_mocks_for_create_snapshot_addsnapshot(is_ear_supported=True,
                                                            replication_mode=ENDPOINT_TYPE_PRODUCTION,
                                                            fc_map_count=3)
        with self.assertRaises(array_errors.VirtSnapshotFunctionNotSupportedMessage):
            self.svc.create_snapshot(common_settings.SOURCE_VOLUME_ID, common_settings.SNAPSHOT_NAME,
                                     space_efficiency=None, pool=common_settings.DUMMY_POOL1,
                                     is_virt_snap_func=True)

    def _test_create_snapshot_addsnapshot_cli_failure_error(self, error_message_id, expected_error):
        self._prepare_mocks_for_create_snapshot_addsnapshot()
        self._test_mediator_method_client_cli_failure_error(self.svc.create_snapshot,
                                                            (common_settings.SOURCE_VOLUME_ID,
                                                             common_settings.SNAPSHOT_NAME, "",
                                                             common_settings.DUMMY_POOL1, True),
                                                            self.svc.sdk.svc_task_api.addsnapshot_post, error_message_id,
                                                            expected_error)

    def test_create_snapshot_addsnapshot_raise_exceptions(self):
        self.svc.sdk.svc_task_api.addsnapshot_post = Mock()
        self._test_mediator_method_client_error(self.svc.create_snapshot,
                                                (common_settings.SOURCE_VOLUME_NAME, common_settings.SNAPSHOT_NAME, "",
                                                 common_settings.DUMMY_POOL1),
                                                self.svc.sdk.svc_task_api.addsnapshot_post, Exception, Exception)
        self._test_create_snapshot_addsnapshot_cli_failure_error(array_settings.DUMMY_ERROR_MESSAGE, SdkApiException)
        self._test_create_snapshot_addsnapshot_cli_failure_error("CMMVC8710E", array_errors.NotEnoughSpaceInPool)
        self._test_create_snapshot_addsnapshot_cli_failure_error("CMMVC6017E", array_errors.InvalidArgumentError)
        self._test_create_snapshot_addsnapshot_cli_failure_error("CMMVC6035E", array_errors.SnapshotAlreadyExists)
        self._test_create_snapshot_addsnapshot_cli_failure_error("CMMVC5754E", array_errors.PoolDoesNotExist)

    def test_delete_snapshot_no_volume_raise_snapshot_not_found(self):
        self._prepare_lsvdisk_to_return_none()

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.delete_snapshot(common_settings.SNAPSHOT_NAME, common_settings.INTERNAL_SNAPSHOT_ID)

    def test_delete_snapshot_no_fcmap_id_raise_snapshot_not_found(self):
        self._prepare_lsvdisk_to_return_mapless_target_volume()

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.delete_snapshot(common_settings.SNAPSHOT_NAME, common_settings.INTERNAL_SNAPSHOT_ID)

    def test_delete_snapshot_call_rmfcmap(self):
        self._prepare_mocks_for_delete_snapshot()
        fcmaps_as_target = self.fcmaps
        self.svc.sdk.svc_info_api.lsfcmap_post.side_effect = [fcmaps_as_target, []]
        self.svc.delete_snapshot(common_settings.SNAPSHOT_NAME, common_settings.INTERNAL_SNAPSHOT_ID)

        self.svc.sdk.svc_task_api.rmfcmap_id_post.assert_called_once()

    def test_delete_snapshot_does_not_remove_hyperswap_fcmap(self):
        self._prepare_mocks_for_delete_snapshot()
        self._prepare_fcmaps_for_hyperswap()
        self.svc.delete_snapshot(common_settings.SNAPSHOT_NAME, common_settings.INTERNAL_SNAPSHOT_ID)

        self.svc.sdk.svc_task_api.rmfcmap_id_post.assert_not_called()

    def _test_delete_snapshot_rmvolume_cli_failure_error(self, error_message_id, expected_error,
                                                         snapshot_id=common_settings.SNAPSHOT_VOLUME_UID):
        self._test_mediator_method_client_cli_failure_error(self.svc.delete_snapshot,
                                                            (snapshot_id, common_settings.INTERNAL_SNAPSHOT_ID),
                                                            self.svc.sdk.svc_task_api.rmvdisk_id_post, error_message_id,
                                                            expected_error)

    def test_delete_snapshot_rmvolume_errors(self):
        self._prepare_mocks_for_delete_snapshot()
        self._test_delete_snapshot_rmvolume_cli_failure_error("CMMVC5753E", array_errors.ObjectNotFoundError)
        self._test_delete_snapshot_rmvolume_cli_failure_error("CMMVC8957E", array_errors.ObjectNotFoundError)
        self._test_delete_snapshot_rmvolume_cli_failure_error(array_settings.DUMMY_ERROR_MESSAGE, SdkApiException)

    def test_delete_snapshot_still_copy_fcmaps_not_removed(self):
        self._prepare_mocks_for_delete_volume()
        fcmaps_as_target = self.fcmaps
        fcmaps_as_source = self.fcmaps_as_source
        fcmaps_as_source[0].status = svc_settings.DUMMY_FCMAP_BAD_STATUS
        self.svc.sdk.svc_info_api.lsfcmap_post.side_effect = [fcmaps_as_target, fcmaps_as_source]
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.svc.delete_snapshot(common_settings.SNAPSHOT_NAME, common_settings.INTERNAL_SNAPSHOT_ID)

    def test_delete_snapshot_rmvolume_success(self):
        self._prepare_mocks_for_delete_snapshot()
        self.svc.delete_snapshot(common_settings.SNAPSHOT_NAME, common_settings.INTERNAL_SNAPSHOT_ID)
        self.assertEqual(2, self.svc.sdk.svc_task_api.rmfcmap_id_post.call_count)
        self.svc.sdk.svc_task_api.rmvdisk_id_post.assert_called_once()

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def test_delete_snapshot_with_fcmap_already_stopped_success(self, mock_warning):
        self._prepare_mocks_for_delete_snapshot()
        mock_warning.return_value = False
        self.svc.sdk.svc_task_api.stopfcmap_id_post.side_effect = [_sdk_error("CMMVC5912E")]
        self.svc.delete_snapshot(common_settings.SNAPSHOT_NAME, common_settings.INTERNAL_SNAPSHOT_ID)
        self.assertEqual(2, self.svc.sdk.svc_task_api.rmfcmap_id_post.call_count)
        self.svc.sdk.svc_task_api.rmvdisk_id_post.assert_called_once()

    @patch("controllers.array_action.array_mediator_svc.is_warning_message")
    def test_delete_snapshot_with_stopfcmap_raise_error(self, mock_warning):
        self._prepare_mocks_for_delete_snapshot()
        mock_warning.return_value = False
        self.svc.sdk.svc_task_api.stopfcmap_id_post.side_effect = [_sdk_error(array_settings.DUMMY_ERROR_MESSAGE)]
        with self.assertRaises(SdkApiException):
            self.svc.delete_snapshot(common_settings.SNAPSHOT_NAME, common_settings.INTERNAL_SNAPSHOT_ID)

    def _prepare_mocks_for_delete_snapshot_addsnapshot(self):
        self.svc.sdk.svc_task_api.addsnapshot_post = Mock()

    def _test_delete_snapshot_rmsnapshot_cli_failure_error(self, error_message_id, expected_error):
        self._test_mediator_method_client_cli_failure_error(self.svc.delete_snapshot,
                                                            ("", common_settings.INTERNAL_SNAPSHOT_ID),
                                                            self.svc.sdk.svc_task_api.rmsnapshot_post, error_message_id,
                                                            expected_error)

    def test_delete_snapshot_rmsnapshot_errors(self):
        self._prepare_mocks_for_delete_snapshot_addsnapshot()
        self._test_delete_snapshot_rmsnapshot_cli_failure_error("CMMVC9755E", array_errors.ObjectNotFoundError)
        self._test_delete_snapshot_rmsnapshot_cli_failure_error(array_settings.DUMMY_ERROR_MESSAGE, SdkApiException)

    def test_delete_snapshot_rmsnapshot_success(self):
        self._prepare_mocks_for_delete_snapshot_addsnapshot()
        self.svc.delete_snapshot("", common_settings.INTERNAL_SNAPSHOT_ID)
        self.svc.sdk.svc_task_api.rmsnapshot_post.assert_called_once()

    def test_validate_supported_space_efficiency_raise_error(self):
        space_efficiency = svc_settings.DUMMY_SPACE_EFFICIENCY
        with self.assertRaises(
                array_errors.SpaceEfficiencyNotSupported):
            self.svc.validate_supported_space_efficiency(space_efficiency)

    def test_validate_supported_space_efficiency_success(self):
        no_space_efficiency = ""
        self.svc.validate_supported_space_efficiency(no_space_efficiency)
        thin_space_efficiency = SPACE_EFFICIENCY_THIN
        self.svc.validate_supported_space_efficiency(thin_space_efficiency)
        thick_space_efficiency = SPACE_EFFICIENCY_THICK
        self.svc.validate_supported_space_efficiency(thick_space_efficiency)
        compressed_space_efficiency = SPACE_EFFICIENCY_COMPRESSED
        self.svc.validate_supported_space_efficiency(compressed_space_efficiency)
        deduplicated_space_efficiency = SPACE_EFFICIENCY_DEDUPLICATED
        self.svc.validate_supported_space_efficiency(deduplicated_space_efficiency)
        deduplicated_thin_space_efficiency = SPACE_EFFICIENCY_DEDUPLICATED_THIN
        self.svc.validate_supported_space_efficiency(deduplicated_thin_space_efficiency)
        deduplicated_compressed_space_efficiency = SPACE_EFFICIENCY_DEDUPLICATED_COMPRESSED
        self.svc.validate_supported_space_efficiency(deduplicated_compressed_space_efficiency)

    def _test_build_kwargs_from_parameters(self, space_efficiency, pool, io_group, volume_group, name, size,
                                           expected_extra_kwargs):
        expected_kwargs = {svc_settings.CREATE_VOLUME_NAME_ARGUMENT: name,
                           svc_settings.CREATE_VOLUME_SIZE_UNIT_ARGUMENT: svc_settings.BYTE_UNIT_SYMBOL,
                           svc_settings.CREATE_VOLUME_SIZE_ARGUMENT: size,
                           svc_settings.CREATE_VOLUME_POOL_ARGUMENT: pool}
        expected_kwargs.update(expected_extra_kwargs)
        if io_group:
            expected_kwargs[svc_settings.CREATE_VOLUME_IO_GROUP_ARGUMENT] = io_group
        if volume_group:
            expected_kwargs[svc_settings.CREATE_VOLUME_VOLUME_GROUP_ARGUMENT] = volume_group
        actual_kwargs = build_kwargs_from_parameters(space_efficiency, pool, io_group, volume_group, name, size)
        self.assertDictEqual(actual_kwargs, expected_kwargs)

    def test_build_kwargs_from_parameters(self):
        size = self.svc._convert_size_bytes(1000)
        second_size = self.svc._convert_size_bytes(2048)
        self._test_build_kwargs_from_parameters(SPACE_EFFICIENCY_THIN, common_settings.DUMMY_POOL1, None, None,
                                                common_settings.VOLUME_NAME, size,
                                                {SPACE_EFFICIENCY_THIN: True})
        self._test_build_kwargs_from_parameters(SPACE_EFFICIENCY_COMPRESSED, common_settings.DUMMY_POOL1, None, None,
                                                common_settings.VOLUME_NAME, size,
                                                {SPACE_EFFICIENCY_COMPRESSED: True})
        expected_extra_kwargs = {svc_settings.CREATE_VOLUME_IO_GROUP_ARGUMENT: common_settings.DUMMY_IO_GROUP,
                                 svc_settings.CREATE_VOLUME_VOLUME_GROUP_ARGUMENT: common_settings.DUMMY_VOLUME_GROUP,
                                 SPACE_EFFICIENCY_THIN: True, SPACE_EFFICIENCY_DEDUPLICATED: True}
        self._test_build_kwargs_from_parameters(SPACE_EFFICIENCY_DEDUPLICATED_THIN, common_settings.DUMMY_POOL1,
                                                common_settings.DUMMY_IO_GROUP,
                                                common_settings.DUMMY_VOLUME_GROUP,
                                                common_settings.VOLUME_NAME,
                                                second_size,
                                                expected_extra_kwargs
                                                )
        self._test_build_kwargs_from_parameters(SPACE_EFFICIENCY_DEDUPLICATED_COMPRESSED, common_settings.DUMMY_POOL2,
                                                None, None,
                                                common_settings.VOLUME_NAME, second_size,
                                                {SPACE_EFFICIENCY_COMPRESSED: True,
                                                 SPACE_EFFICIENCY_DEDUPLICATED: True})
        self._test_build_kwargs_from_parameters(SPACE_EFFICIENCY_DEDUPLICATED, common_settings.DUMMY_POOL2, None, None,
                                                common_settings.VOLUME_NAME,
                                                second_size,
                                                {SPACE_EFFICIENCY_COMPRESSED: True,
                                                 SPACE_EFFICIENCY_DEDUPLICATED: True})

    def test_properties(self):
        self.assertEqual(22, SVCArrayMediator.port)
        self.assertEqual(array_settings.DUMMY_SMALL_CAPACITY_INT,
                         SVCArrayMediator.minimal_volume_size_in_bytes)
        self.assertEqual(ARRAY_TYPE_SVC, SVCArrayMediator.array_type)
        self.assertEqual(63, SVCArrayMediator.max_object_name_length)
        self.assertEqual(2, SVCArrayMediator.max_connections)
        self.assertEqual(10, SVCArrayMediator.max_lun_retries)

    def _prepare_lsnvmefabric_mock(self, host_names, nvme_host_names, connectivity_types):
        nvme_host_mocks = []
        self.svc.sdk.svc_info_api.lsnvmefabric_post.return_value = nvme_host_mocks
        if array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE in connectivity_types:
            nvme_host_names = host_names if nvme_host_names is None else nvme_host_names
            if nvme_host_names:
                nvme_host_mocks = [Mock(object_name=host_name) for host_name in nvme_host_names]
                lsnvmefabric_return_values = [[host_mock] * 4 for host_mock in nvme_host_mocks]
                self.svc.sdk.svc_info_api.lsnvmefabric_post.side_effect = lsnvmefabric_return_values

    def _prepare_lsfabric_mock_for_get_host(self, host_names, fc_host_names, connectivity_types):
        fc_host_mocks = []
        self.svc.sdk.svc_info_api.lsfabric_post.return_value = fc_host_mocks
        if array_settings.FC_CONNECTIVITY_TYPE in connectivity_types:
            fc_host_names = host_names if fc_host_names is None else fc_host_names
            if fc_host_names:
                for host_name in fc_host_names:
                    mock = Mock()
                    mock.name = host_name
                    fc_host_mocks.append(mock)
                lsfabric_return_values = [[host_mock] * 4 for host_mock in fc_host_mocks]
                self.svc.sdk.svc_info_api.lsfabric_post.side_effect = lsfabric_return_values

    def _prepare_lshostiplogin_mock(self, host_name, iscsi_host_name, connectivity_types):
        iscsi_host_name = host_name if iscsi_host_name is None else iscsi_host_name
        if array_settings.ISCSI_CONNECTIVITY_TYPE in connectivity_types and iscsi_host_name:
            # Must be a Munch/dict so _SdkResponse.as_single_element works correctly
            self.svc.sdk.svc_info_api.lshostiplogin_id_post.return_value = Munch({'host_name': iscsi_host_name})
        else:
            self.svc.sdk.svc_info_api.lshostiplogin_id_post.side_effect = _sdk_error("CMMVC5804E")

    def _prepare_mocks_for_get_host_by_identifiers(self, nvme_host_names=None, fc_host_names=None,
                                                   iscsi_host_name=None, connectivity_types=None):
        host_name = array_settings.DUMMY_HOST_NAME1
        host_names = [host_name]

        if connectivity_types is None:
            connectivity_types = {array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE,
                                  array_settings.FC_CONNECTIVITY_TYPE,
                                  array_settings.ISCSI_CONNECTIVITY_TYPE}

        self._prepare_lsnvmefabric_mock(host_names, nvme_host_names, connectivity_types)
        self._prepare_lsfabric_mock_for_get_host(host_names, fc_host_names, connectivity_types)
        self._prepare_lshostiplogin_mock(host_name, iscsi_host_name, connectivity_types)

    def _prepare_mocks_for_get_host_by_identifiers_no_hosts(self):
        self._prepare_mocks_for_get_host_by_identifiers(nvme_host_names=[], fc_host_names=[], iscsi_host_name="")
        self.svc.sdk.svc_info_api.lshost_post = Mock(return_value=[])

    def _prepare_mocks_for_get_host_by_identifiers_no_hosts_lsnvmefabric_not_supported(self):
        self._prepare_mocks_for_get_host_by_identifiers(nvme_host_names=[array_settings.DUMMY_HOST_NAME2],
                                                        fc_host_names=[], iscsi_host_name="")
        self.svc.sdk.svc_info_api.lsnvmefabric_post.side_effect = [_sdk_error("CMMVC7205E")] * 4
        self.svc.sdk.svc_info_api.lshost_post = Mock(return_value=[])

    def _prepare_mocks_for_get_host_by_identifiers_slow(self, custom_host=None):
        self._prepare_mocks_for_get_host_by_identifiers_no_hosts()
        host_1 = self._get_host_as_munch(array_settings.DUMMY_HOST_ID1, array_settings.DUMMY_HOST_NAME1, nqn_list=[
            array_settings.DUMMY_NVME_NQN1],
                                         wwpns_list=[array_settings.DUMMY_FC_WWN1],
                                         iscsi_names_list=[array_settings.DUMMY_NODE1_IQN])
        host_2 = self._get_host_as_munch(array_settings.DUMMY_HOST_ID2, array_settings.DUMMY_HOST_NAME2, nqn_list=[
            array_settings.DUMMY_NVME_NQN2],
                                         wwpns_list=[array_settings.DUMMY_FC_WWN2],
                                         iscsi_names_list=[array_settings.DUMMY_NODE2_IQN])
        if custom_host:
            host_3 = custom_host
        else:
            host_3 = self._get_host_as_munch(array_settings.DUMMY_HOST_ID3, array_settings.DUMMY_HOST_NAME3, nqn_list=[
                array_settings.DUMMY_NVME_NQN3],
                                             wwpns_list=[array_settings.DUMMY_FC_WWN3], iscsi_names_list=[
                    array_settings.DUMMY_NODE3_IQN])
        hosts = [host_1, host_2, host_3]
        self.svc.sdk.svc_info_api.lshost_post = Mock()
        self.svc.sdk.svc_info_api.lshost_post.return_value = self._get_hosts_list_result(hosts)
        # _get_all_hosts calls lshost_id_post individually for each host in the list.
        # Repeat the list so tests that call get_host_by_host_identifiers twice do not exhaust it.
        self.svc.sdk.svc_info_api.lshost_id_post.side_effect = hosts * 3

    def _prepare_mocks_for_get_host_by_identifiers_backward_compatible(self):
        self._prepare_mocks_for_get_host_by_identifiers_slow()
        # Simulate old firmware that doesn't support lshostiplogin / lsnvmefabric
        # by raising the "command not supported" SDK error instead of deleting the attribute.
        self.svc.sdk.svc_info_api.lshostiplogin_id_post.side_effect = _sdk_error("CMMVC5804E")
        self.svc.sdk.svc_info_api.lsnvmefabric_post.side_effect = _sdk_error("CMMVC7205E")

    def test_get_host_by_name_success(self):
        self.svc.sdk.svc_info_api.lshost_id_post.return_value = self._get_host_as_munch(
            array_settings.DUMMY_HOST_ID1, array_settings.DUMMY_HOST_NAME1,
            nqn_list=[array_settings.DUMMY_NVME_NQN1],
            wwpns_list=[array_settings.DUMMY_FC_WWN1],
            iscsi_names_list=[array_settings.DUMMY_NODE1_IQN])
        host = self.svc.get_host_by_name(array_settings.DUMMY_HOST_NAME1)
        self.assertEqual(array_settings.DUMMY_HOST_NAME1, host.name)
        self.assertEqual([array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE, array_settings.FC_CONNECTIVITY_TYPE,
                          array_settings.ISCSI_CONNECTIVITY_TYPE],
                         host.connectivity_types)
        self.assertEqual([array_settings.DUMMY_NVME_NQN1], host.initiators.nvme_nqns)
        self.assertEqual([array_settings.DUMMY_FC_WWN1], host.initiators.fc_wwns)
        self.assertEqual([array_settings.DUMMY_NODE1_IQN], host.initiators.iscsi_iqns)

    def test_get_host_by_name_raise_host_not_found(self):
        self.svc.sdk.svc_info_api.lshost_id_post.return_value = None
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_by_name(array_settings.DUMMY_HOST_NAME1)

    def test_get_host_by_identifiers_returns_host_not_found(self):
        self._prepare_mocks_for_get_host_by_identifiers_slow()
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_by_host_identifiers(Initiators([array_settings.DUMMY_NVME_NQN4], [
                array_settings.DUMMY_FC_WWN4], [array_settings.DUMMY_NODE4_IQN]))

    def test_get_host_by_identifier_return_host_not_found_when_no_hosts_exist(self):
        self._prepare_mocks_for_get_host_by_identifiers_no_hosts()
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_by_host_identifiers(Initiators([array_settings.DUMMY_NVME_NQN4], [
                array_settings.DUMMY_FC_WWN4], [array_settings.DUMMY_NODE4_IQN]))

    def test_get_host_by_identifier_return_host_not_found_when_no_hosts_exist_lsnvmefabric_not_supported(self):
        self._prepare_mocks_for_get_host_by_identifiers_no_hosts_lsnvmefabric_not_supported()
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_by_host_identifiers(Initiators([array_settings.DUMMY_NVME_NQN4], [
                array_settings.DUMMY_FC_WWN4], [array_settings.DUMMY_NODE4_IQN]))

    def test_get_host_by_identifiers_slow_raise_multiplehostsfounderror(self):
        self._prepare_mocks_for_get_host_by_identifiers_slow()
        with self.assertRaises(array_errors.MultipleHostsFoundError):
            self.svc.get_host_by_host_identifiers(Initiators([array_settings.DUMMY_NVME_NQN4], [
                array_settings.DUMMY_FC_WWN2], [array_settings.DUMMY_NODE3_IQN]))

    def test_get_host_by_identifiers_raise_multiplehostsfounderror(self):
        self._prepare_mocks_for_get_host_by_identifiers(nvme_host_names=[array_settings.DUMMY_HOST_NAME1],
                                                        fc_host_names=[array_settings.DUMMY_HOST_NAME2])
        with self.assertRaises(array_errors.MultipleHostsFoundError):
            self.svc.get_host_by_host_identifiers(Initiators([array_settings.DUMMY_NVME_NQN4], [
                array_settings.DUMMY_FC_WWN2], [array_settings.DUMMY_NODE3_IQN]))

    def test_get_host_by_identifiers_slow_return_iscsi_host(self):
        self._prepare_mocks_for_get_host_by_identifiers_slow()
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([array_settings.DUMMY_NVME_NQN4], [array_settings.DUMMY_FC_WWN4], [
                array_settings.DUMMY_NODE2_IQN]))
        self.assertEqual(array_settings.DUMMY_HOST_NAME2, hostname)
        self.assertEqual([array_settings.ISCSI_CONNECTIVITY_TYPE], connectivity_types)

    def test_get_host_by_identifiers_return_iscsi_host(self):
        self._prepare_mocks_for_get_host_by_identifiers(iscsi_host_name=array_settings.DUMMY_HOST_NAME1,
                                                        connectivity_types=[array_settings.ISCSI_CONNECTIVITY_TYPE])
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([array_settings.DUMMY_NVME_NQN4], [array_settings.DUMMY_FC_WWN4], [
                array_settings.DUMMY_NODE2_IQN]))
        self.assertEqual(array_settings.DUMMY_HOST_NAME1, hostname)
        self.assertEqual({array_settings.ISCSI_CONNECTIVITY_TYPE}, connectivity_types)
        self.svc.sdk.svc_info_api.lshostiplogin_id_post.assert_called_once()

    def test_get_host_by_identifiers_slow_no_other_ports_return_iscsi_host(self):
        host_with_iqn = self._get_host_as_munch(array_settings.DUMMY_HOST_ID1, common_settings.HOST_NAME,
                                                iscsi_names_list=[
                                                    array_settings.DUMMY_NODE1_IQN])
        self._prepare_mocks_for_get_host_by_identifiers_slow(custom_host=host_with_iqn)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([array_settings.DUMMY_NVME_NQN4], [array_settings.DUMMY_FC_WWN4], [
                array_settings.DUMMY_NODE1_IQN]))
        self.assertEqual(common_settings.HOST_NAME, hostname)
        self.assertEqual([array_settings.ISCSI_CONNECTIVITY_TYPE], connectivity_types)

    def test_get_host_by_identifiers_slow_return_iscsi_host_with_list_iqn(self):
        host_with_iqn_list = self._get_host_as_munch(array_settings.DUMMY_HOST_ID1, common_settings.HOST_NAME,
                                                     wwpns_list=[
                                                         array_settings.DUMMY_FC_WWN1],
                                                     iscsi_names_list=[array_settings.DUMMY_NODE1_IQN,
                                                                       array_settings.DUMMY_NODE2_IQN])
        self._prepare_mocks_for_get_host_by_identifiers_slow(custom_host=host_with_iqn_list)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([array_settings.DUMMY_NVME_NQN4], [array_settings.DUMMY_FC_WWN4], [
                array_settings.DUMMY_NODE1_IQN]))
        self.assertEqual(common_settings.HOST_NAME, hostname)
        self.assertEqual([array_settings.ISCSI_CONNECTIVITY_TYPE], connectivity_types)

    def test_get_host_by_identifiers_slow_return_nvme_host(self):
        self._prepare_mocks_for_get_host_by_identifiers_slow()
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([array_settings.DUMMY_NVME_NQN3], [array_settings.DUMMY_FC_WWN4], [
                array_settings.DUMMY_NODE4_IQN]))
        self.assertEqual(array_settings.DUMMY_HOST_NAME3, hostname)
        self.assertEqual([array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE], connectivity_types)

    def test_get_host_by_identifiers_return_nvme_host(self):
        self._prepare_mocks_for_get_host_by_identifiers(nvme_host_names=[array_settings.DUMMY_HOST_NAME3],
                                                        connectivity_types=[
                                                            array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE])
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([array_settings.DUMMY_NVME_NQN1], [array_settings.DUMMY_FC_WWN4], [
                array_settings.DUMMY_NODE4_IQN]))
        self.assertEqual(array_settings.DUMMY_HOST_NAME3, hostname)
        self.assertEqual({array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE}, connectivity_types)
        self.svc.sdk.svc_info_api.lsnvmefabric_post.assert_called_once()

    def test_get_host_by_identifiers_slow_no_other_ports_return_nvme_host(self):
        host_with_nqn = self._get_host_as_munch(array_settings.DUMMY_HOST_ID1, common_settings.HOST_NAME,
                                                nqn_list=[array_settings.DUMMY_NVME_NQN4])
        self._prepare_mocks_for_get_host_by_identifiers_slow(custom_host=host_with_nqn)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([array_settings.DUMMY_NVME_NQN4], [array_settings.DUMMY_FC_WWN4], [
                array_settings.DUMMY_NODE4_IQN]))
        self.assertEqual(common_settings.HOST_NAME, hostname)
        self.assertEqual([array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE], connectivity_types)

    def test_get_host_by_identifiers_slow_return_fc_host(self):
        host_1 = self._get_host_as_munch(array_settings.DUMMY_HOST_ID1, array_settings.DUMMY_HOST_NAME1, wwpns_list=[
            array_settings.DUMMY_FC_WWN1],
                                         iscsi_names_list=[])
        host_2 = self._get_host_as_munch(array_settings.DUMMY_HOST_ID2, array_settings.DUMMY_HOST_NAME2, wwpns_list=[
            array_settings.DUMMY_FC_WWN2],
                                         iscsi_names_list=[])
        host_3 = self._get_host_as_munch(array_settings.DUMMY_HOST_ID3, array_settings.DUMMY_HOST_NAME3, wwpns_list=[
            array_settings.DUMMY_FC_WWN3, array_settings.DUMMY_FC_WWN4],
                                         iscsi_names_list=[array_settings.DUMMY_NODE3_IQN])
        hosts = [host_1, host_2, host_3]
        self._prepare_mocks_for_get_host_by_identifiers_slow()
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([array_settings.DUMMY_NVME_NQN4], [array_settings.DUMMY_FC_WWN4, array_settings.DUMMY_FC_WWN3], [
                array_settings.DUMMY_NODE4_IQN]))
        self.assertEqual(array_settings.DUMMY_HOST_NAME3, hostname)
        self.assertEqual([array_settings.FC_CONNECTIVITY_TYPE], connectivity_types)

        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([array_settings.DUMMY_NVME_NQN4], [array_settings.DUMMY_FC_WWN3], [
                array_settings.DUMMY_NODE4_IQN]))
        self.assertEqual(array_settings.DUMMY_HOST_NAME3, hostname)
        self.assertEqual([array_settings.FC_CONNECTIVITY_TYPE], connectivity_types)

    def test_get_host_by_identifiers_return_fc_host(self):
        self._prepare_mocks_for_get_host_by_identifiers(fc_host_names=[array_settings.DUMMY_HOST_NAME3],
                                                        connectivity_types=[array_settings.FC_CONNECTIVITY_TYPE])
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([array_settings.DUMMY_NVME_NQN1], [array_settings.DUMMY_FC_WWN4], [
                array_settings.DUMMY_NODE4_IQN]))
        self.assertEqual(array_settings.DUMMY_HOST_NAME3, hostname)
        self.assertEqual({array_settings.FC_CONNECTIVITY_TYPE}, connectivity_types)
        self.svc.sdk.svc_info_api.lsfabric_post.assert_called_once()

    def test_get_host_by_identifiers_slow_no_other_ports_return_fc_host(self):
        host_with_wwpn = self._get_host_as_munch(array_settings.DUMMY_HOST_ID1, common_settings.HOST_NAME, wwpns_list=[
            array_settings.DUMMY_FC_WWN1])
        self._prepare_mocks_for_get_host_by_identifiers_slow(custom_host=host_with_wwpn)
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([array_settings.DUMMY_NVME_NQN4], [array_settings.DUMMY_FC_WWN4, array_settings.DUMMY_FC_WWN1], [
                array_settings.DUMMY_NODE4_IQN]))
        self.assertEqual(common_settings.HOST_NAME, hostname)
        self.assertEqual([array_settings.FC_CONNECTIVITY_TYPE], connectivity_types)

    def test_get_host_by_identifiers_no_other_ports_return_fc_host(self):
        self._prepare_mocks_for_get_host_by_identifiers(fc_host_names=["", array_settings.DUMMY_HOST_NAME2],
                                                        connectivity_types=[array_settings.FC_CONNECTIVITY_TYPE])
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([array_settings.DUMMY_NVME_NQN4], [array_settings.DUMMY_FC_WWN4, array_settings.DUMMY_FC_WWN1], [
                array_settings.DUMMY_NODE4_IQN]))
        self.assertEqual(array_settings.DUMMY_HOST_NAME2, hostname)
        self.assertEqual({array_settings.FC_CONNECTIVITY_TYPE}, connectivity_types)

    def test_get_host_by_identifiers_slow_with_wrong_fc_iscsi_raise_not_found(self):
        host_1 = self._get_host_as_munch(array_settings.DUMMY_HOST_ID1, array_settings.DUMMY_HOST_NAME1, wwpns_list=[
            array_settings.DUMMY_FC_WWN1],
                                         iscsi_names_list=[])
        host_2 = self._get_host_as_munch(array_settings.DUMMY_HOST_ID2, array_settings.DUMMY_HOST_NAME2, wwpns_list=[
            array_settings.DUMMY_FC_WWN3],
                                         iscsi_names_list=[array_settings.DUMMY_NODE2_IQN])
        host_3 = self._get_host_as_munch(array_settings.DUMMY_HOST_ID3, array_settings.DUMMY_HOST_NAME3, wwpns_list=[
            array_settings.DUMMY_FC_WWN3],
                                         iscsi_names_list=[array_settings.DUMMY_NODE3_IQN])
        hosts = [host_1, host_2, host_3]
        self._prepare_mocks_for_get_host_by_identifiers_slow()
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_by_host_identifiers(Initiators([array_settings.DUMMY_NVME_NQN4], [], []))
        # FC wwn2 matches host_2; iscsi iqn1 matches host_1 → two different hosts found
        with self.assertRaises(array_errors.MultipleHostsFoundError):
            self.svc.get_host_by_host_identifiers(Initiators([array_settings.DUMMY_NVME_NQN4],
                                                             [array_settings.DUMMY_FC_WWN4,
                                                              array_settings.DUMMY_FC_WWN2],
                                                             [array_settings.DUMMY_NODE1_IQN]))

    def test_get_host_by_identifiers_slow_backward_compatible_return_nvme_fc_and_iscsi(self):
        self._prepare_mocks_for_get_host_by_identifiers_backward_compatible()
        initiators = Initiators([array_settings.DUMMY_NVME_NQN2], [array_settings.DUMMY_FC_WWN2],
                                [array_settings.DUMMY_NODE2_IQN])
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(initiators)
        self.assertEqual(array_settings.DUMMY_HOST_NAME2, hostname)
        self.assertEqual(
            {array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE, array_settings.FC_CONNECTIVITY_TYPE,
             array_settings.ISCSI_CONNECTIVITY_TYPE},
            set(connectivity_types))

    def test_get_host_by_identifiers_slow_return_nvme_fc_and_iscsi(self):
        self._prepare_mocks_for_get_host_by_identifiers_slow()
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([array_settings.DUMMY_NVME_NQN2], [array_settings.DUMMY_FC_WWN2], [
                array_settings.DUMMY_NODE2_IQN]))
        self.assertEqual(array_settings.DUMMY_HOST_NAME2, hostname)
        self.assertEqual(
            {array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE, array_settings.FC_CONNECTIVITY_TYPE,
             array_settings.ISCSI_CONNECTIVITY_TYPE},
            set(connectivity_types))

    def test_get_host_by_identifiers_return_nvme_fc_and_iscsi(self):
        self._prepare_mocks_for_get_host_by_identifiers()
        hostname, connectivity_types = self.svc.get_host_by_host_identifiers(
            Initiators([array_settings.DUMMY_NVME_NQN1], [array_settings.DUMMY_FC_WWN1], [
                array_settings.DUMMY_NODE1_IQN]))
        self.assertEqual(array_settings.DUMMY_HOST_NAME1, hostname)
        self.assertEqual(
            {array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE, array_settings.FC_CONNECTIVITY_TYPE,
             array_settings.ISCSI_CONNECTIVITY_TYPE},
            connectivity_types)

    def _get_host_as_munch(self, host_id, host_name, nqn_list=None, wwpns_list=None, iscsi_names_list=None,
                           portset_id=None):
        host = Munch(id=host_id, name=host_name)
        if nqn_list:
            host.nqn = nqn_list
        if wwpns_list:
            host.WWPN = wwpns_list
        if iscsi_names_list:
            host.iscsi_name = iscsi_names_list
        if portset_id:
            host.portset_id = portset_id
        return host

    def _get_hosts_list_result(self, hosts_dict):
        return [Munch(host_dict) for host_dict in hosts_dict]

    def test_get_volume_mappings_empty_mapping_list(self):
        self.svc.sdk.svc_info_api.lsvdiskhostmap_id_post.return_value = []
        mappings = self.svc.get_volume_mappings(common_settings.VOLUME_UID)
        self.assertEqual({}, mappings)

    def _test_get_volume_mappings_lsvdisk_cli_failure_error(self, volume_name, error_message_id, expected_error):
        self._test_mediator_method_client_cli_failure_error(self.svc.get_volume_mappings, (volume_name,),
                                                            self.svc.sdk.svc_info_api.lsvdisk_post, error_message_id,
                                                            expected_error)

    def test_get_volume_mappings_lsvdisk_cli_failure_errors(self):
        self._test_get_volume_mappings_lsvdisk_cli_failure_error(svc_settings.INVALID_NAME_1, "CMMVC6017E",
                                                                 array_errors.InvalidArgumentError)
        self._test_get_volume_mappings_lsvdisk_cli_failure_error(svc_settings.INVALID_NAME_SYMBOLS, "CMMVC5741E",
                                                                 array_errors.InvalidArgumentError)

    def test_get_volume_mappings_on_volume_not_found(self):
        self.svc.sdk.svc_info_api.lsvdiskhostmap_id_post.side_effect = [
            _sdk_error(array_settings.DUMMY_ERROR_MESSAGE)]

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.get_volume_mappings(common_settings.VOLUME_UID)

    def _mock_cli_host_map(self, hostmap_id, scsi_id, host_id, host_name=common_settings.HOST_NAME,
                           hostmap_name=svc_settings.DUMMY_HOST_MAP_NAME):
        return Munch({svc_settings.HOST_MAP_ID_ATTR_KEY: hostmap_id,
                      svc_settings.HOST_MAP_NAME_ATTR_KEY: hostmap_name,
                      svc_settings.HOST_MAP_LUN_ATTR_KEY: scsi_id,
                      svc_settings.HOST_MAP_HOST_ID_ATTR_KEY: host_id,
                      svc_settings.HOST_MAP_HOST_NAME_ATTR_KEY: host_name})

    def test_get_volume_mappings_success(self):
        maps = self._get_mock_host_list(("0", "1"))
        self.svc.sdk.svc_info_api.lsvdiskhostmap_id_post.return_value = maps
        mappings = self.svc.get_volume_mappings(common_settings.VOLUME_UID)
        self.assertEqual({"host_0": "0", "host_1": "1"}, mappings)

    def test_get_free_lun_raises_host_not_found_error(self):
        self.svc.sdk.svc_info_api.lshostvdiskmap_id_post.side_effect = [
            _sdk_error(array_settings.DUMMY_ERROR_MESSAGE)]
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc._get_free_lun(common_settings.HOST_NAME)

    def _get_mock_host_list(self, lun_list):
        maps = []
        for index, lun in enumerate(lun_list):
            maps.append(self._mock_cli_host_map(hostmap_id=index,
                                                hostmap_name="host_map_{}".format(index),
                                                scsi_id=str(lun),
                                                host_id=index,
                                                host_name="host_{}".format(index)))
        return maps

    def _test_get_free_lun_host_mappings(self, lun_list, expected_lun="0"):
        maps = self._get_mock_host_list(lun_list)
        self.svc.sdk.svc_info_api.lshostvdiskmap_id_post.return_value = maps
        lun = self.svc._get_free_lun(common_settings.HOST_NAME)
        if lun_list:
            self.assertNotIn(lun, lun_list)
        self.assertEqual(lun, expected_lun)

    @patch("controllers.array_action.array_mediator_svc.choice")
    def test_get_free_lun_with_no_host_mappings(self, random_choice):
        random_choice.return_value = "0"
        self._test_get_free_lun_host_mappings([])

    @patch.object(SVCArrayMediator, "MAX_LUN_NUMBER", 2)
    @patch.object(SVCArrayMediator, "MIN_LUN_NUMBER", 0)
    def test_get_free_lun_success(self):
        self._test_get_free_lun_host_mappings(("1", "2"))

    @patch.object(SVCArrayMediator, "MAX_LUN_NUMBER", 4)
    @patch.object(SVCArrayMediator, "MIN_LUN_NUMBER", 0)
    @patch("controllers.array_action.array_mediator_svc.LUN_INTERVAL", 1)
    def test_get_free_lun_in_interval_success(self):
        self._test_get_free_lun_host_mappings(("0", "1"), expected_lun="2")

    @patch.object(SVCArrayMediator, "MAX_LUN_NUMBER", 3)
    @patch.object(SVCArrayMediator, "MIN_LUN_NUMBER", 1)
    @patch.object(SVCArrayMediator, "MAX_LUN_NUMBER_INCREMENT", 2)
    @patch("controllers.array_action.array_mediator_svc.randint")
    def test_free_lun_no_available_lun(self, random_choice):
        random_choice.return_value = 4
        self._test_get_free_lun_host_mappings(("1", "2", "3"), expected_lun="4")
        random_choice.assert_called_with(4, 5)

    @patch("controllers.array_action.array_mediator_svc.SVCArrayMediator._get_free_lun")
    def _test_map_volume_mkvdiskhostmap_error(self, client_error, expected_error, mock_get_free_lun):
        mock_get_free_lun.return_value = array_settings.DUMMY_LUN_ID
        self._test_mediator_method_client_error(self.svc.map_volume, (
            common_settings.VOLUME_UID, common_settings.HOST_NAME,
            array_settings.DUMMY_CONNECTIVITY_TYPE),
                                                self.svc.sdk.svc_task_api.mkvdiskhostmap_id_post, client_error,
                                                expected_error)

    def test_map_volume_mkvdiskhostmap_errors(self):
        self._test_map_volume_mkvdiskhostmap_error(_sdk_error("CMMVC5804E"),
                                                   array_errors.ObjectNotFoundError)
        self._test_map_volume_mkvdiskhostmap_error(_sdk_error("CMMVC5754E"),
                                                   array_errors.HostNotFoundError)
        self._test_map_volume_mkvdiskhostmap_error(_sdk_error("CMMVC5879E"),
                                                   array_errors.LunAlreadyInUseError)
        self._test_map_volume_mkvdiskhostmap_error(_sdk_error(array_settings.DUMMY_ERROR_MESSAGE),
                                                   array_errors.MappingError)
        self._test_map_volume_mkvdiskhostmap_error(Exception, Exception)

    @patch("controllers.array_action.array_mediator_svc.SVCArrayMediator._get_free_lun")
    def test_map_volume_success(self, mock_get_free_lun):
        mock_get_free_lun.return_value = array_settings.DUMMY_LUN_ID
        self.svc.sdk.svc_task_api.mkvdiskhostmap_id_post.return_value = None
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._get_cli_volume(name=common_settings.VOLUME_NAME)
        lun = self.svc.map_volume(common_settings.VOLUME_UID, common_settings.HOST_NAME,
                                  array_settings.DUMMY_CONNECTIVITY_TYPE)
        self.assertEqual(array_settings.DUMMY_LUN_ID, lun)
        self.svc.sdk.svc_task_api.mkvdiskhostmap_id_post.assert_called_once()

    def test_map_volume_nvme_success(self):
        self.svc.sdk.svc_task_api.mkvdiskhostmap_id_post.return_value = None
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._get_cli_volume(name=common_settings.VOLUME_NAME)
        lun = self.svc.map_volume(common_settings.VOLUME_UID, common_settings.HOST_NAME,
                                  array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE)
        self.assertEqual("", lun)
        self.svc.sdk.svc_task_api.mkvdiskhostmap_id_post.assert_called_once()

    def _test_unmap_volume_rmvdiskhostmap_error(self, client_error, expected_error):
        self._test_mediator_method_client_error(self.svc.unmap_volume, (
            common_settings.VOLUME_UID, common_settings.HOST_NAME),
                                                self.svc.sdk.svc_task_api.rmvdiskhostmap_id_post, client_error,
                                                expected_error)

    def test_unmap_volume_rmvdiskhostmap_errors(self):
        self._test_unmap_volume_rmvdiskhostmap_error(_sdk_error("CMMVC5753E"),
                                                     array_errors.ObjectNotFoundError)
        self._test_unmap_volume_rmvdiskhostmap_error(_sdk_error("CMMVC5754E"),
                                                     array_errors.HostNotFoundError)
        self._test_unmap_volume_rmvdiskhostmap_error(_sdk_error("CMMVC5842E"),
                                                     array_errors.VolumeAlreadyUnmappedError)
        self._test_unmap_volume_rmvdiskhostmap_error(_sdk_error("CMMVC5804E"),
                                                     array_errors.VolumeNotMappedToHostError)
        self._test_unmap_volume_rmvdiskhostmap_error(_sdk_error(
            array_settings.DUMMY_ERROR_MESSAGE),
            array_errors.UnmappingError)
        self._test_unmap_volume_rmvdiskhostmap_error(Exception, Exception)

    def test_unmap_volume_success(self):
        self.svc.sdk.svc_task_api.rmvdiskhostmap_id_post.return_value = None
        self.svc.unmap_volume(common_settings.VOLUME_UID, common_settings.HOST_NAME)

    def _prepare_mocks_for_get_iscsi_targets(self, portset_id=None):
        host = self._get_host_as_munch(array_settings.DUMMY_HOST_ID1, common_settings.HOST_NAME, wwpns_list=[
            array_settings.DUMMY_FC_WWN1],
                                       iscsi_names_list=[array_settings.DUMMY_NODE1_IQN,
                                                         array_settings.DUMMY_NODE2_IQN],
                                       portset_id=portset_id)
        self.svc.sdk.svc_info_api.lshost_id_post = Mock()
        self.svc.sdk.svc_info_api.lshost_id_post.return_value = host

    def test_get_iscsi_targets_cmd_error_raise_host_not_found(self):
        self.svc.sdk.svc_info_api.lshost_id_post.return_value = None
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_iscsi_targets_by_iqn(common_settings.HOST_NAME)

    def test_get_iscsi_targets_cmd_error_raise_no_targets_error(self):
        self._prepare_mocks_for_get_iscsi_targets()
        self.svc.sdk.svc_info_api.lsportip_post.side_effect = [
            _sdk_error(array_settings.DUMMY_ERROR_MESSAGE)]
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn(common_settings.HOST_NAME)

    def test_get_iscsi_targets_cli_error_raise_no_targets_error(self):
        self._prepare_mocks_for_get_iscsi_targets()
        self.svc.sdk.svc_info_api.lsportip_post.side_effect = [
            _sdk_error(array_settings.DUMMY_ERROR_MESSAGE)]
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn(common_settings.HOST_NAME)

    def test_get_iscsi_targets_no_online_node_raise_no_targets_error(self):
        self._prepare_mocks_for_get_iscsi_targets()
        node = self._mock_node(status=svc_settings.OFFLINE_STATUS)
        self.svc.sdk.svc_info_api.lsnode_post.return_value = [node]
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn(common_settings.HOST_NAME)

    def test_get_iscsi_targets_no_nodes_nor_ips_raise_no_targets_error(self):
        self._prepare_mocks_for_get_iscsi_targets()
        self.svc.sdk.svc_info_api.lsnode_post.return_value = []
        self.svc.sdk.svc_info_api.lsportip_post.return_value = []
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn(common_settings.HOST_NAME)

    def test_get_iscsi_targets_no_port_with_ip_raise_no_targets_error(self):
        self._prepare_mocks_for_get_iscsi_targets()
        port_1 = self._mock_cli_port_ip(ip_addr=None, ip_addr6="")
        port_2 = self._mock_cli_port_ip(node_id=svc_settings.DUMMY_INTERNAL_ID2, ip_addr="", ip_addr6=None)
        self.svc.sdk.svc_info_api.lsportip_post.return_value = [port_1, port_2]
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn(common_settings.HOST_NAME)

    def test_get_iscsi_targets_no_ip_raise_no_targets_error(self):
        self._prepare_mocks_for_get_iscsi_targets()
        self.svc.sdk.svc_info_api.lsportip_post.return_value = []
        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.svc.get_iscsi_targets_by_iqn(common_settings.HOST_NAME)

    def test_get_iscsi_targets_with_lsportip_success(self):
        self._prepare_mocks_for_get_iscsi_targets()
        ips_by_iqn = self.svc.get_iscsi_targets_by_iqn(common_settings.HOST_NAME)
        self.svc.sdk.svc_info_api.lsportip_post.assert_called_once()
        self.assertEqual({array_settings.DUMMY_NODE1_IQN: [array_settings.DUMMY_IP_ADDRESS1]},
                         ips_by_iqn)

    def test_get_iscsi_targets_with_lsip_success(self):
        self._prepare_mocks_for_get_iscsi_targets(portset_id=svc_settings.DUMMY_INTERNAL_ID1)
        ips_by_iqn = self.svc.get_iscsi_targets_by_iqn(common_settings.HOST_NAME)
        filtervalue = self._get_filtervalue(svc_settings.LSIP_PORTSET_ID_ATTR_KEY, svc_settings.DUMMY_INTERNAL_ID1)
        self.svc.sdk.svc_info_api.lsip_post.assert_called_once()
        self.svc.sdk.svc_info_api.lsportip_post.assert_not_called()
        self.assertEqual({array_settings.DUMMY_NODE1_IQN: [array_settings.DUMMY_IP_ADDRESS1]},
                         ips_by_iqn)

    def test_get_iscsi_targets_with_exception(self):
        self.svc.sdk.svc_info_api.lsnode_post.side_effect = [Exception]
        with self.assertRaises(Exception):
            self.svc.get_iscsi_targets_by_iqn(common_settings.HOST_NAME)

    def _mock_cli_port_ip(self, node_id=svc_settings.DUMMY_INTERNAL_ID1,
                          ip_addr=array_settings.DUMMY_IP_ADDRESS1,
                          ip_addr6=None):
        return Munch({svc_settings.NODE_ID_KEY: node_id,
                      svc_settings.IP_ADDRESS_KEY: ip_addr,
                      svc_settings.ADDRESS_6_ATTR_KEY: ip_addr6})

    def test_get_iscsi_targets_with_multi_nodes(self):
        self._prepare_mocks_for_get_iscsi_targets()
        node1 = self._mock_node()
        node2 = self._mock_node(svc_settings.DUMMY_INTERNAL_ID2, array_settings.DUMMY_NODE2_NAME,
                                array_settings.DUMMY_NODE2_IQN)
        self.svc.sdk.svc_info_api.lsnode_post.return_value = [node1, node2]
        port_1 = self._mock_cli_port_ip()
        port_2 = self._mock_cli_port_ip(ip_addr=array_settings.DUMMY_IP_ADDRESS2)
        port_3 = self._mock_cli_port_ip(svc_settings.DUMMY_INTERNAL_ID2, "",
                                        ip_addr6=array_settings.DUMMY_IP_ADDRESS_6_1)
        self.svc.sdk.svc_info_api.lsportip_post.return_value = [port_1, port_2, port_3]

        ips_by_iqn = self.svc.get_iscsi_targets_by_iqn(common_settings.HOST_NAME)

        self.assertEqual(ips_by_iqn, {
            array_settings.DUMMY_NODE1_IQN: [array_settings.DUMMY_IP_ADDRESS1,
                                             array_settings.DUMMY_IP_ADDRESS2],
            array_settings.DUMMY_NODE2_IQN: ["[{}]".format(array_settings.DUMMY_IP_ADDRESS_6_1)]})

    def test_get_array_fc_wwns_failed(self):
        self.svc.sdk.svc_info_api.lsfabric_post.side_effect = [
            _sdk_error(array_settings.DUMMY_ERROR_MESSAGE)]
        with self.assertRaises(SdkApiException):
            self.svc.get_array_fc_wwns(common_settings.HOST_NAME)

    def _mock_lsfabric_port(self, remote_wwpn, remote_nportid, port_id, node_name, local_wwpn, local_port,
                            local_nportid, state):
        return Munch({svc_settings.LSFABRIC_PORT_REMOTE_WWPN_ATTR_KEY: remote_wwpn,
                      svc_settings.LSFABRIC_PORT_REMOTE_NPORTID_ATTR_KEY: remote_nportid,
                      svc_settings.LSFABRIC_PORT_ID_ATTR_KEY: port_id,
                      svc_settings.LSFABRIC_PORT_NODE_NAME_ATTR_KEY: node_name,
                      svc_settings.LSFABRIC_PORT_LOCAL_WWPN_ATTR_KEY: local_wwpn,
                      svc_settings.LSFABRIC_PORT_LOCAL_PORT_ATTR_KEY: local_port,
                      svc_settings.LSFABRIC_PORT_LOCAL_NPORTID_ATTR_KEY: local_nportid,
                      svc_settings.LSFABRIC_PORT_STATE_ATTR_KEY: state,
                      svc_settings.LSFABRIC_PORT_NAME_ATTR_KEY: svc_settings.DUMMY_PORT_NAME,
                      svc_settings.LSFABRIC_PORT_CLUSTER_NAME_ATTR_KEY: "",
                      svc_settings.LSFABRIC_PORT_TYPE_ATTR_KEY: common_settings.HOST_NAME})

    def test_get_array_fc_wwns_success(self):
        port_1 = self._mock_lsfabric_port(svc_settings.DUMMY_REMOTE_WWPN1,
                                          svc_settings.DUMMY_REMOTE_NPORTID1,
                                          svc_settings.DUMMY_INTERNAL_ID1,
                                          array_settings.DUMMY_NODE1_NAME,
                                          svc_settings.DUMMY_LOCAL_WWPN1,
                                          svc_settings.DUMMY_LOCAL_PORT1,
                                          svc_settings.DUMMY_LOCAL_NPORTID1,
                                          svc_settings.ACTIVE_STATE)
        port_2 = self._mock_lsfabric_port(svc_settings.DUMMY_REMOTE_WWPN2,
                                          svc_settings.DUMMY_REMOTE_NPORTID2,
                                          svc_settings.DUMMY_INTERNAL_ID2,
                                          array_settings.DUMMY_NODE2_NAME,
                                          svc_settings.DUMMY_LOCAL_WWPN2,
                                          svc_settings.DUMMY_LOCAL_PORT2,
                                          svc_settings.DUMMY_LOCAL_NPORTID2,
                                          svc_settings.INACTIVE_STATE)
        self.svc.sdk.svc_info_api.lsfabric_post.return_value = [port_1, port_2]
        wwns = self.svc.get_array_fc_wwns(common_settings.HOST_NAME)
        self.assertEqual([svc_settings.DUMMY_LOCAL_WWPN1, svc_settings.DUMMY_LOCAL_WWPN2], wwns)

    def _prepare_mocks_for_expand_volume(self):
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._get_cli_volume(
            name=common_settings.VOLUME_NAME,
            capacity=array_settings.DUMMY_SMALL_CAPACITY_STR)
        self.svc.sdk.svc_info_api.lsfcmap_post.return_value = []

    def test_expand_volume_success(self):
        self._prepare_mocks_for_expand_volume()
        self.svc.expand_volume(common_settings.VOLUME_UID, array_settings.DUMMY_CAPACITY_INT)
        self.svc.sdk.svc_task_api.chvolume_id_post.assert_called_once()

    def test_expand_volume_success_with_size_rounded_up(self):
        self._prepare_mocks_for_expand_volume()
        self.svc.expand_volume(common_settings.VOLUME_UID, 513)
        self.svc.sdk.svc_task_api.chvolume_id_post.assert_called_once()

    def test_expand_volume_in_hyperswap(self):
        self._prepare_mocks_for_expand_volume()
        del self.svc.sdk.svc_task_api.chvolume_id_post
        self._prepare_fcmaps_for_hyperswap()
        self.svc.expand_volume(common_settings.VOLUME_UID, array_settings.DUMMY_CAPACITY_INT)

        self.svc.sdk.svc_task_api.expandvolume_id_post.assert_called_once()

    def test_expand_volume_with_chvolume_not_supported_uses_expandvdisksize(self):
        self._prepare_mocks_for_expand_volume()
        del self.svc.sdk.svc_task_api.chvolume_id_post
        self.svc.expand_volume(common_settings.VOLUME_UID, array_settings.DUMMY_CAPACITY_INT)
        self.svc.sdk.svc_task_api.expandvdisksize_id_post.assert_called_once()

    def test_expand_volume_with_chvolume_not_supported_and_hyperswap_uses_expandvolume(self):
        self._prepare_mocks_for_expand_volume()
        del self.svc.sdk.svc_task_api.chvolume_id_post
        self._prepare_fcmaps_for_hyperswap()
        self.svc.expand_volume(common_settings.VOLUME_UID, array_settings.DUMMY_CAPACITY_INT)
        self.svc.sdk.svc_task_api.expandvolume_id_post.assert_called_once()
        self.svc.sdk.svc_task_api.expandvdisksize_id_post.assert_not_called()

    def test_expand_volume_raise_object_not_found(self):
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = None
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.expand_volume(common_settings.VOLUME_UID, array_settings.DUMMY_CAPACITY_INT)
        self.svc.sdk.svc_task_api.chvolume_id_post.assert_not_called()

    def _test_expand_volume_chvolume_errors(self, client_error, expected_error):
        self._prepare_mocks_for_expand_volume()
        self._test_mediator_method_client_error(self.svc.expand_volume, (common_settings.VOLUME_UID, 2, None),
                                                self.svc.sdk.svc_task_api.chvolume_id_post, client_error, expected_error)

    def test_expand_volume_chvolume_errors(self):
        self._test_expand_volume_chvolume_errors(_sdk_error("CMMVC5753E"), array_errors.ObjectNotFoundError)
        self._test_expand_volume_chvolume_errors(_sdk_error("CMMVC8957E"), array_errors.ObjectNotFoundError)
        self._test_expand_volume_chvolume_errors(_sdk_error("CMMVC5860E"),
                                                 array_errors.NotEnoughSpaceInPool)
        self._test_expand_volume_chvolume_errors(_sdk_error(array_settings.DUMMY_ERROR_MESSAGE),
                                                 SdkApiException)
        self._test_expand_volume_chvolume_errors(Exception(array_settings.DUMMY_ERROR_MESSAGE), Exception)

    def _test_expand_volume_expandvdisksize_errors(self, client_error, expected_error):
        self._prepare_mocks_for_expand_volume()
        self._test_mediator_method_client_error(self.svc.expand_volume, (common_settings.VOLUME_UID, 2, None),
                                                self.svc.sdk.svc_task_api.expandvdisksize_id_post, client_error, expected_error)

    def test_expand_volume_expandvdisksize_errors(self):
        del self.svc.sdk.svc_task_api.chvolume_id_post
        self._test_expand_volume_expandvdisksize_errors(_sdk_error("CMMVC5753E"), array_errors.ObjectNotFoundError)
        self._test_expand_volume_expandvdisksize_errors(_sdk_error("CMMVC8957E"), array_errors.ObjectNotFoundError)
        self._test_expand_volume_expandvdisksize_errors(_sdk_error("CMMVC5860E"),
                                                        array_errors.NotEnoughSpaceInPool)
        self._test_expand_volume_expandvdisksize_errors(_sdk_error(array_settings.DUMMY_ERROR_MESSAGE),
                                                        SdkApiException)
        self._test_expand_volume_expandvdisksize_errors(Exception(array_settings.DUMMY_ERROR_MESSAGE), Exception)

    def _test_expand_volume_expandvolume_errors(self, client_error, expected_error):
        self._prepare_mocks_for_expand_volume()
        self._prepare_fcmaps_for_hyperswap()
        self._test_mediator_method_client_error(self.svc.expand_volume, (common_settings.VOLUME_UID, 2, None),
                                                self.svc.sdk.svc_task_api.expandvolume_id_post, client_error, expected_error)

    def test_expand_volume_expandvolume_errors(self):
        del self.svc.sdk.svc_task_api.chvolume_id_post
        self._test_expand_volume_expandvolume_errors(_sdk_error("CMMVC5753E"), array_errors.ObjectNotFoundError)
        self._test_expand_volume_expandvolume_errors(_sdk_error("CMMVC8957E"), array_errors.ObjectNotFoundError)
        self._test_expand_volume_expandvolume_errors(_sdk_error("CMMVC5860E"),
                                                     array_errors.NotEnoughSpaceInPool)
        self._test_expand_volume_expandvolume_errors(_sdk_error(array_settings.DUMMY_ERROR_MESSAGE),
                                                     SdkApiException)
        self._test_expand_volume_expandvolume_errors(Exception(array_settings.DUMMY_ERROR_MESSAGE), Exception)

    def _expand_volume_lsvdisk_errors(self, client_error, expected_error, volume_id=common_settings.VOLUME_UID):
        self._test_mediator_method_client_error(self.svc.expand_volume, (volume_id,
                                                                         array_settings.DUMMY_CAPACITY_INT,
                                                                         None),
                                                self.svc.sdk.svc_info_api.lsvdisk_post, client_error, expected_error)

    def test_expand_volume_lsvdisk_errors(self):
        self._expand_volume_lsvdisk_errors(_sdk_error("CMMVC6017E"), array_errors.InvalidArgumentError,
                                           svc_settings.INVALID_NAME_1)
        self._expand_volume_lsvdisk_errors(_sdk_error("CMMVC5741E"), array_errors.InvalidArgumentError,
                                           svc_settings.INVALID_NAME_SYMBOLS)
        self._expand_volume_lsvdisk_errors(_sdk_error(array_settings.DUMMY_ERROR_MESSAGE), SdkApiException)
        self._expand_volume_lsvdisk_errors(Exception(array_settings.DUMMY_ERROR_MESSAGE), Exception)

    def test_create_host_nvme_success(self):
        self.svc.create_host(
            common_settings.HOST_NAME,
            Initiators(
                [array_settings.DUMMY_NVME_NQN1],
                [array_settings.DUMMY_FC_WWN1, array_settings.DUMMY_FC_WWN2],
                [array_settings.DUMMY_NODE1_IQN]),
            array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE, array_settings.DUMMY_MULTIPLE_IO_GROUP_STRING)
        self.svc.sdk.svc_task_api.mkhost_post.assert_called_once()

    def test_create_host_nvme_success_force_portset(self):
        with patch('os.getenv', return_value='2'):
            self.svc.create_host(
                common_settings.HOST_NAME,
                Initiators(
                    [array_settings.DUMMY_NVME_NQN1],
                    [array_settings.DUMMY_FC_WWN1, array_settings.DUMMY_FC_WWN2],
                    [array_settings.DUMMY_NODE1_IQN]),
                array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE, array_settings.DUMMY_MULTIPLE_IO_GROUP_STRING)
            self.svc.sdk.svc_task_api.mkhost_post.assert_called_once()

    def test_create_host_fc_success(self):
        self.svc.create_host(
            common_settings.HOST_NAME,
            Initiators(
                [], [array_settings.DUMMY_FC_WWN1, array_settings.DUMMY_FC_WWN2],
                [array_settings.DUMMY_NODE1_IQN]),
            array_settings.FC_CONNECTIVITY_TYPE, array_settings.DUMMY_MULTIPLE_IO_GROUP_STRING)
        self.svc.sdk.svc_task_api.mkhost_post.assert_called_once()

    def _test_create_host_one_port_failure(self, error_code):
        self.svc.sdk.svc_task_api.mkhost_post.side_effect = [_sdk_error(error_code), Mock()]
        self.svc.create_host(common_settings.HOST_NAME,
                             Initiators([], [array_settings.DUMMY_FC_WWN1, array_settings.DUMMY_FC_WWN2], []),
                             array_settings.FC_CONNECTIVITY_TYPE, array_settings.DUMMY_MULTIPLE_IO_GROUP_STRING)
        self.assertEqual(self.svc.sdk.svc_task_api.mkhost_post.call_count, 2)

    def test_create_host_fc_when_one_port_is_not_valid_success(self):
        self._test_create_host_one_port_failure('CMMVC5867E')

    def test_create_host_fc_when_one_portset_invalid_type(self):
        self._test_create_host_one_port_failure('CMMVC9699E')

    def test_create_host_fc_when_one_portset_value_too_long(self):
        self._test_create_host_one_port_failure('CMMVC5703E')

    def test_create_host_fc_when_one_portset_not_found(self):
        self._test_create_host_one_port_failure('CMMVC5868E')

    def test_create_host_iscsi_success(self):
        self.svc.create_host(common_settings.HOST_NAME, Initiators([], [], [array_settings.DUMMY_NODE1_IQN]),
                             array_settings.ISCSI_CONNECTIVITY_TYPE, array_settings.DUMMY_MULTIPLE_IO_GROUP_STRING)
        self.svc.sdk.svc_task_api.mkhost_post.assert_called_once()

    def test_create_host_iscsi_with_empty_io_groupsuccess(self):
        self.svc.create_host(common_settings.HOST_NAME, Initiators([], [], [array_settings.DUMMY_NODE1_IQN]),
                             array_settings.ISCSI_CONNECTIVITY_TYPE, "")
        self.svc.sdk.svc_task_api.mkhost_post.assert_called_once()

    def test_create_host_fc_when_two_ports_are_not_valid_failed(self):
        self.svc.sdk.svc_task_api.mkhost_post.side_effect = [_sdk_error('CMMVC5867E'), _sdk_error('CMMVC5867E')]
        with self.assertRaises(array_errors.NoPortIsValid):
            self.svc.create_host(common_settings.HOST_NAME,
                                 Initiators([], [array_settings.DUMMY_FC_WWN1, array_settings.DUMMY_FC_WWN2], []),
                                 array_settings.FC_CONNECTIVITY_TYPE, array_settings.DUMMY_MULTIPLE_IO_GROUP_STRING)
        self.assertEqual(self.svc.sdk.svc_task_api.mkhost_post.call_count, 2)

    def test_create_host_with_empty_ports_failed(self):
        with self.assertRaises(array_errors.NoPortFoundByConnectivityType):
            self.svc.create_host(common_settings.HOST_NAME,
                                 Initiators([], [], []),
                                 "",
                                 array_settings.DUMMY_MULTIPLE_IO_GROUP_STRING)
        self.svc.sdk.svc_task_api.mkhost_post.assert_not_called()

    def test_create_host_with_connectivity_type_failed(self):
        with self.assertRaises(array_errors.UnsupportedConnectivityTypeError):
            self.svc.create_host(common_settings.HOST_NAME,
                                 Initiators([], [], [array_settings.DUMMY_NODE1_IQN]),
                                 svc_settings.MKHOST_NVME_PROTOCOL_VALUE,
                                 array_settings.DUMMY_MULTIPLE_IO_GROUP_STRING)
        self.svc.sdk.svc_task_api.mkhost_post.assert_not_called()

    def test_create_host_with_invalid_io_group_failed(self):
        self.svc.sdk.svc_task_api.mkhost_post.side_effect = [_sdk_error('CMMVC5729E')]
        with self.assertRaises(array_errors.IoGroupIsInValid):
            self.svc.create_host(common_settings.HOST_NAME,
                                 Initiators([], [array_settings.DUMMY_FC_WWN1, array_settings.DUMMY_FC_WWN2], []),
                                 array_settings.FC_CONNECTIVITY_TYPE, array_settings.DUMMY_MULTIPLE_IO_GROUP_STRING)

    def _test_create_host_mkhost_errors(self, client_error, expected_error, connectivity_type=""):
        self._test_mediator_method_client_error(self.svc.create_host,
                                                (common_settings.HOST_NAME,
                                                 Initiators([], [], [array_settings.DUMMY_NODE1_IQN]),
                                                 connectivity_type, array_settings.DUMMY_MULTIPLE_IO_GROUP_STRING),
                                                self.svc.sdk.svc_task_api.mkhost_post, client_error,
                                                expected_error)

    def test_create_host_errors(self):
        self._test_create_host_mkhost_errors(_sdk_error('CMMVC6035E'),
                                             array_errors.HostAlreadyExists, array_settings.ISCSI_CONNECTIVITY_TYPE)
        self._test_create_host_mkhost_errors(Exception("Failed"), Exception, array_settings.ISCSI_CONNECTIVITY_TYPE)

    def _test_delete_host_rmhost_errors(self, client_error, expected_error):
        self._test_mediator_method_client_error(self.svc.delete_host,
                                                (common_settings.HOST_NAME,),
                                                self.svc.sdk.svc_task_api.rmhost_id_post, client_error,
                                                expected_error)

    def test_delete_host_errors(self):
        self._test_delete_host_rmhost_errors(Exception("Failed"), Exception)

    def _prepare_mocks_for_add_ports_to_host(self, port_count):
        self.svc.sdk.svc_info_api.lshost_id_post = Mock()
        self.svc.sdk.svc_info_api.lshost_id_post.return_value = Munch({
            svc_settings.PORT_COUNT_FIELD: port_count})

    def test_add_nvme_ports_to_host_success(self):
        self._prepare_mocks_for_add_ports_to_host(1)
        self.svc.add_ports_to_host(common_settings.HOST_NAME,
                                   Initiators([array_settings.DUMMY_NVME_NQN1],
                                              [array_settings.DUMMY_FC_WWN1, array_settings.DUMMY_FC_WWN2],
                                              [array_settings.DUMMY_NODE1_IQN]),
                                   array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE)
        self.svc.sdk.svc_task_api.addhostport_id_post.assert_called_once()

    def _test_add_fc_ports_to_host_success(self):
        self._prepare_mocks_for_add_ports_to_host(1)
        self.svc.add_ports_to_host(common_settings.HOST_NAME,
                                   Initiators([array_settings.DUMMY_NVME_NQN1],
                                              [array_settings.DUMMY_FC_WWN1, array_settings.DUMMY_FC_WWN2],
                                              [array_settings.DUMMY_NODE1_IQN]),
                                   array_settings.FC_CONNECTIVITY_TYPE)
        self.assertEqual(self.svc.sdk.svc_task_api.addhostport_id_post.call_count, 2)
        self.svc.sdk.svc_task_api.addhostport_id_post.assert_called()

    def test_add_fc_ports_to_host_success(self):
        self._test_add_fc_ports_to_host_success()

    def test_add_fc_ports_to_host_when_one_port_is_not_valid_success(self):
        self.svc.sdk.svc_task_api.addhostport_id_post.side_effect = [_sdk_error('CMMVC5867E'), Mock()]
        self._test_add_fc_ports_to_host_success()

    def test_add_iscsi_ports_to_host_success(self):
        self._prepare_mocks_for_add_ports_to_host(1)
        self.svc.add_ports_to_host(common_settings.HOST_NAME,
                                   Initiators([array_settings.DUMMY_NVME_NQN1],
                                              [array_settings.DUMMY_FC_WWN1, array_settings.DUMMY_FC_WWN2],
                                              [array_settings.DUMMY_NODE1_IQN]),
                                   array_settings.ISCSI_CONNECTIVITY_TYPE)
        self.svc.sdk.svc_task_api.addhostport_id_post.assert_called_once()

    def test_add_fc_ports_to_host_when_two_ports_are_not_valid_falied(self):
        self._prepare_mocks_for_add_ports_to_host(0)
        self.svc.sdk.svc_task_api.addhostport_id_post.side_effect = [_sdk_error('CMMVC5867E'), _sdk_error('CMMVC5867E')]
        with self.assertRaises(array_errors.NoPortIsValid):
            self.svc.add_ports_to_host(common_settings.HOST_NAME,
                                       Initiators([], [array_settings.DUMMY_FC_WWN1, array_settings.DUMMY_FC_WWN2], []),
                                       array_settings.FC_CONNECTIVITY_TYPE)
        self.assertEqual(self.svc.sdk.svc_task_api.addhostport_id_post.call_count, 2)

    def test_add_ports_to_host_falied(self):
        self._test_mediator_method_client_error(self.svc.add_ports_to_host,
                                                (common_settings.HOST_NAME,
                                                 Initiators([], [], [array_settings.DUMMY_NODE1_IQN]),
                                                 array_settings.ISCSI_CONNECTIVITY_TYPE),
                                                self.svc.sdk.svc_task_api.addhostport_id_post, Exception("Failed"), Exception)

    def test_remove_nvme_ports_from_host_success(self):
        self.svc.remove_ports_from_host(common_settings.HOST_NAME,
                                        [array_settings.DUMMY_NVME_NQN1],
                                        array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE)
        self.svc.sdk.svc_task_api.rmhostport_id_post.assert_called_once()

    def _test_remove_fc_ports_from_host_success(self):
        self.svc.remove_ports_from_host(common_settings.HOST_NAME,
                                        [array_settings.DUMMY_FC_WWN1, array_settings.DUMMY_FC_WWN2],
                                        array_settings.FC_CONNECTIVITY_TYPE)
        self.assertEqual(self.svc.sdk.svc_task_api.rmhostport_id_post.call_count, 2)
        self.svc.sdk.svc_task_api.rmhostport_id_post.assert_called()

    def test_remove_fc_ports_from_host_success(self):
        self._test_remove_fc_ports_from_host_success()

    def test_remove_fc_ports_from_host_when_one_port_is_not_valid_success(self):
        self.svc.sdk.svc_task_api.rmhostport_id_post.side_effect = [_sdk_error('CMMVC5867E'), Mock()]
        self._test_remove_fc_ports_from_host_success()

    def test_remove_iscsi_ports_from_host_success(self):
        self.svc.remove_ports_from_host(common_settings.HOST_NAME,
                                        [array_settings.DUMMY_NODE1_IQN],
                                        array_settings.ISCSI_CONNECTIVITY_TYPE)
        self.svc.sdk.svc_task_api.rmhostport_id_post.assert_called_once()

    def test_remove_ports_from_host_falied(self):
        self._test_mediator_method_client_error(self.svc.remove_ports_from_host,
                                                (common_settings.HOST_NAME,
                                                 [array_settings.DUMMY_NODE1_IQN],
                                                 array_settings.ISCSI_CONNECTIVITY_TYPE),
                                                self.svc.sdk.svc_task_api.rmhostport_id_post, Exception("Failed"), Exception)

    def _prepare_mocks_for_get_host_with_ports(self, attribute_name):
        self.svc.sdk.svc_info_api.lshost_id_post = Mock()
        self.svc.sdk.svc_info_api.lshost_id_post.return_value = Munch({
            attribute_name: ['port1', 'port2']})

    def _test_get_host_connectivity_port(self, connectivity_attribute_name, connectivity_type):
        self._prepare_mocks_for_get_host_with_ports(connectivity_attribute_name)
        result = self.svc.get_host_connectivity_ports(common_settings.HOST_NAME, connectivity_type)
        self.assertEqual(result, ['port1', 'port2'])

    def test_get_host_connectivity_port_success(self):
        self._test_get_host_connectivity_port(svc_settings.HOST_NQN, array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE)
        self._test_get_host_connectivity_port(svc_settings.HOST_WWPN, array_settings.FC_CONNECTIVITY_TYPE)
        self._test_get_host_connectivity_port(svc_settings.HOST_ISCSI_NAME, array_settings.ISCSI_CONNECTIVITY_TYPE)

    def test_get_host_connectivity_port_falied(self):
        self._prepare_mocks_for_get_host_with_ports(svc_settings.HOST_NQN)
        with self.assertRaises(array_errors.UnsupportedConnectivityTypeError):
            self.svc.get_host_connectivity_ports(common_settings.HOST_NAME, 'some_connectivity_type')

    def _test_get_host_connectivity_type(self, connectivity_attribute_name, connectivity_type):
        self._prepare_mocks_for_get_host_with_ports(connectivity_attribute_name)
        result = self.svc.get_host_connectivity_type(common_settings.HOST_NAME)
        self.assertEqual(result, connectivity_type)

    def test_get_host_connectivity_type_success(self):
        self._test_get_host_connectivity_type(svc_settings.HOST_NQN, array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE)
        self._test_get_host_connectivity_type(svc_settings.HOST_WWPN, array_settings.FC_CONNECTIVITY_TYPE)
        self._test_get_host_connectivity_type(svc_settings.HOST_ISCSI_NAME, array_settings.ISCSI_CONNECTIVITY_TYPE)
        self._test_get_host_connectivity_type('some_connectivity_attribute_name', None)

    def test_add_io_group_to_host_success(self):
        self.svc.add_io_group_to_host(common_settings.HOST_NAME, array_settings.DUMMY_MULTIPLE_IO_GROUP_STRING)
        self.svc.sdk.svc_task_api.addhostiogrp_id_post.assert_called_once()

    def test_add_empty_io_group_to_host_success(self):
        self.svc.add_io_group_to_host(common_settings.HOST_NAME, '')
        self.svc.sdk.svc_task_api.addhostiogrp_id_post.assert_not_called()

    def test_add_io_group_to_not_exist_host_falied(self):
        self.svc.sdk.svc_task_api.addhostiogrp_id_post.side_effect = [_sdk_error('CMMVC5754E')]
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.add_io_group_to_host(common_settings.HOST_NAME, array_settings.DUMMY_MULTIPLE_IO_GROUP_STRING)

    def test_add_invlid_io_group_to_host_falied(self):
        self.svc.sdk.svc_task_api.addhostiogrp_id_post.side_effect = [_sdk_error('CMMVC5729E')]
        with self.assertRaises(array_errors.IoGroupIsInValid):
            self.svc.add_io_group_to_host(common_settings.HOST_NAME, array_settings.DUMMY_MULTIPLE_IO_GROUP_STRING)

    def test_add_io_group_to_host_falied(self):
        self._test_mediator_method_client_error(
            self.svc.add_io_group_to_host, (common_settings.HOST_NAME, array_settings.DUMMY_MULTIPLE_IO_GROUP_STRING),
            self.svc.sdk.svc_task_api.addhostiogrp_id_post, Exception("Failed"),
            Exception)

    def test_remove_io_group_from_host_success(self):
        self.svc.remove_io_group_from_host(common_settings.HOST_NAME, array_settings.DUMMY_MULTIPLE_IO_GROUP_STRING)
        self.svc.sdk.svc_task_api.rmhostiogrp_id_post.assert_called_once()

    def test_remove_empty_io_group_from_host_success(self):
        self.svc.remove_io_group_from_host(common_settings.HOST_NAME, '')
        self.svc.sdk.svc_task_api.rmhostiogrp_id_post.assert_not_called()

    def test_remove_io_group_from_not_exist_host_falied(self):
        self.svc.sdk.svc_task_api.rmhostiogrp_id_post.side_effect = [_sdk_error('CMMVC5754E')]
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.remove_io_group_from_host(common_settings.HOST_NAME, array_settings.DUMMY_MULTIPLE_IO_GROUP_STRING)

    def test_remove_invlid_io_group_from_host_falied(self):
        self.svc.sdk.svc_task_api.rmhostiogrp_id_post.side_effect = [_sdk_error('CMMVC5729E')]
        with self.assertRaises(array_errors.IoGroupIsInValid):
            self.svc.remove_io_group_from_host(common_settings.HOST_NAME, array_settings.DUMMY_MULTIPLE_IO_GROUP_STRING)

    def test_remove_io_group_from_host_falied(self):
        self._test_mediator_method_client_error(
            self.svc.remove_io_group_from_host,
            (common_settings.HOST_NAME, array_settings.DUMMY_MULTIPLE_IO_GROUP_STRING),
            self.svc.sdk.svc_task_api.rmhostiogrp_id_post, Exception("Failed"),
            Exception)

    def _get_lshostiogrp(self, io_group_ids, io_group_names):
        return {
            'id': io_group_ids,
            'name': io_group_names
        }

    def _assert_get_host_io_group(self, io_group_ids, io_group_names, result):
        self.assertEqual(type(result.id), list)
        self.assertEqual(type(result.name), list)
        if isinstance(io_group_ids, str):
            io_group_ids = io_group_ids.split(" ")
        if isinstance(io_group_names, str):
            io_group_names = io_group_names.split(" ")
        self.assertEqual(result, self._get_lshostiogrp(io_group_ids, io_group_names))

    def _test_get_host_io_group_success(self, io_group_ids, io_group_names):
        self.svc.sdk.svc_info_api.lshostiogrp_id_post = Mock()
        self.svc.sdk.svc_info_api.lshostiogrp_id_post.return_value = Munch(
            self._get_lshostiogrp(io_group_ids, io_group_names))
        result = self.svc.get_host_io_group(common_settings.HOST_NAME)
        self.svc.sdk.svc_info_api.lshostiogrp_id_post.assert_called_once()
        self._assert_get_host_io_group(io_group_ids, io_group_names, result)

    def test_get_host_multiple_io_groups_success(self):
        self._test_get_host_io_group_success(svc_settings.MULTIPLE_IO_GROUP_IDS, svc_settings.MULTIPLE_IO_GROUP_NAMES)

    def test_get_host_single_io_group_success(self):
        self._test_get_host_io_group_success(svc_settings.SINGLE_IO_GROUP_ID, svc_settings.SINGLE_IO_GROUP_NAME)

    def test_get_io_group_from_not_exist_host_falied(self):
        self.svc.sdk.svc_info_api.lshostiogrp_id_post = Mock()
        self.svc.sdk.svc_info_api.lshostiogrp_id_post.side_effect = [_sdk_error('CMMVC5754E')]
        with self.assertRaises(array_errors.HostNotFoundError):
            self.svc.get_host_io_group(common_settings.HOST_NAME)

    def test_get_host_io_group_falied(self):
        self._test_mediator_method_client_error(self.svc.get_host_io_group,
                                                (common_settings.HOST_NAME),
                                                self.svc.sdk.svc_info_api.lshostiogrp_id_post, Exception("Failed"), Exception)

    def test_change_host_protocol_to_scsi_success(self):
        self.svc.change_host_protocol(common_settings.HOST_NAME, SCSI_PROTOCOL)
        self.svc.sdk.svc_task_api.chhost_id_post.assert_called_once()

    def test_change_host_protocol_to_nvme_success(self):
        self.svc.change_host_protocol(common_settings.HOST_NAME, NVME_PROTOCOL)
        self.svc.sdk.svc_task_api.chhost_id_post.assert_called_once()

    def _test_change_host_protocol_chhost_errors(self, client_error, expected_error):
        self._test_mediator_method_client_error(self.svc.change_host_protocol,
                                                (common_settings.HOST_NAME, SCSI_PROTOCOL),
                                                self.svc.sdk.svc_task_api.chhost_id_post, client_error,
                                                expected_error)

    def test_change_host_protocol_errors(self):
        self._test_change_host_protocol_chhost_errors(_sdk_error('CMMVC5709E'), array_errors.UnSupportedParameter)
        self._test_change_host_protocol_chhost_errors(_sdk_error('CMMVC5753E'), array_errors.HostNotFoundError)
        self._test_change_host_protocol_chhost_errors(_sdk_error('CMMVC9331E'),
                                                      array_errors.CannotChangeHostProtocolBecauseOfMappedPorts)
        self._test_change_host_protocol_chhost_errors(Exception("Failed"), Exception)

    def _mock_cli_volume_group(self, uid="", volume_count=0):
        volume_group = {svc_settings.VOLUME_GROUP_VOLUME_COUNT_ATTR_KEY: str(volume_count),
                        svc_settings.VOLUME_GROUP_NAME_ATTR_KEY: common_settings.VOLUME_GROUP_NAME,
                        svc_settings.VOLUME_GROUP_ATTR_KEY: common_settings.INTERNAL_VOLUME_GROUP_ID}
        if uid:
            volume_group["uid"] = uid

        return Munch(volume_group)

    def _prepare_lsvolumegroup(self, no_return=None, volume_count=0):
        if no_return:
            return_value = None
        else:
            return_value = self._mock_cli_volume_group(volume_count=volume_count)
        self.svc.sdk.svc_info_api.lsvolumegroup_id_post.return_value = return_value

    def test_create_volume_group_success(self):
        self.svc.sdk.svc_task_api.mkvolumegroup_post.return_value = {"id": "1"}
        self._prepare_lsvolumegroup()

        self.svc.create_volume_group(common_settings.VOLUME_GROUP_NAME, None)

        self.svc.sdk.svc_info_api.lsvolumegroup_id_post.assert_called_once()
        self.svc.sdk.svc_task_api.mkvolumegroup_post.assert_called_once()

    def test_get_volume_group_success(self):
        self._prepare_lsvolumegroup()
        volume_group = self.svc.get_volume_group(common_settings.INTERNAL_VOLUME_GROUP_ID)

        self.svc.sdk.svc_info_api.lsvolumegroup_id_post.assert_called_once()
        self.assertEqual(0, len(volume_group.volumes))

    def test_get_volume_group_not_found_failed(self):
        self._prepare_lsvolumegroup(no_return=True)
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.get_volume_group(common_settings.INTERNAL_VOLUME_GROUP_ID)

    def test_get_volume_group_with_volumes_success(self):
        self._prepare_lsvolumegroup(volume_count=1)
        cli_volume = self._get_cli_volume()
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = [cli_volume]

        volume_group = self.svc.get_volume_group(common_settings.INTERNAL_VOLUME_GROUP_ID)

        self.svc.sdk.svc_info_api.lsvolumegroup_id_post.assert_called_once()
        self.svc.sdk.svc_info_api.lsvdisk_post.assert_called_once()
        self.assertEqual(common_settings.VOLUME_GROUP_NAME, volume_group.name)
        self.assertEqual(1, len(volume_group.volumes))

    def test_delete_volume_group_success(self):
        self.svc.delete_volume_group(common_settings.INTERNAL_VOLUME_GROUP_ID)

        self.svc.sdk.svc_task_api.rmvolumegroup_id_post.assert_called_once()

    def test_add_volume_to_volume_group_success(self):
        cli_volume = self._get_cli_volume()
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = cli_volume

        self.svc.add_volume_to_volume_group(common_settings.INTERNAL_VOLUME_GROUP_ID, common_settings.VOLUME_UID, None)

        self.svc.sdk.svc_task_api.chvdisk_id_post.assert_called_once()

    def test_add_volume_to_volume_group_already_in_volume_group_failed(self):
        cli_volume = self._get_cli_volume(in_volume_group=True)
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = cli_volume

        with self.assertRaises(array_errors.VolumeAlreadyInVolumeGroup):
            self.svc.add_volume_to_volume_group(common_settings.INTERNAL_VOLUME_GROUP_ID, common_settings.VOLUME_UID,
                                                None)

        self.svc.sdk.svc_task_api.chvdisk_id_post.assert_not_called()

    def test_remove_volume_from_volume_group_success(self):
        cli_volume = self._get_cli_volume(in_volume_group=True)
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = cli_volume
        self.svc.remove_volume_from_volume_group(common_settings.VOLUME_UID, None, None)

        self.svc.sdk.svc_task_api.chvdisk_id_post.assert_called_once()

    @patch('{}.is_call_home_enabled'.format('controllers.array_action.array_mediator_svc'))
    @patch('controllers.array_action.array_mediator_svc.SVC_REGISTRATION_CACHE')
    def test_register_plugin_when_there_is_no_registered_storage_success(self, mock_cache, is_enabled_mock):
        mock_cache.get.return_value = {}
        is_enabled_mock.return_value = True

        self._test_register_plugin_success(True)

    @patch('{}.is_call_home_enabled'.format('controllers.array_action.array_mediator_svc'))
    @patch('controllers.array_action.array_mediator_svc.SVC_REGISTRATION_CACHE')
    def test_register_plugin_when_unique_key_is_registered_more_than_two_hours_ago_success(
            self, mock_cache, is_enabled_mock):
        current_time = datetime.now()
        three_hours_ago = current_time - timedelta(hours=3)
        mock_cache.get.return_value = {'test_key': three_hours_ago}
        is_enabled_mock.return_value = True

        self._test_register_plugin_success(True)

    @patch('{}.is_call_home_enabled'.format('controllers.array_action.array_mediator_svc'))
    @patch('controllers.array_action.array_mediator_svc.SVC_REGISTRATION_CACHE')
    def test_do_not_register_plugin_when_unique_key_is_registered_less_than_two_hours_ago_success(
            self, mock_cache, is_enabled_mock):
        current_time = datetime.now()
        one_hours_ago = current_time - timedelta(hours=1)
        mock_cache.get.return_value = {'test_key': one_hours_ago}
        is_enabled_mock.return_value = True

        self._test_register_plugin_success(False)

    @patch('{}.is_call_home_enabled'.format('controllers.array_action.array_mediator_svc'))
    @patch('controllers.array_action.array_mediator_svc.SVC_REGISTRATION_CACHE')
    def test_assert_no_exception_when_register_fail_success(self, mock_cache, is_enabled_mock):
        self.svc.sdk.svc_task_api.registerplugin_post.side_effect = [Exception]
        mock_cache.get.return_value = {}
        is_enabled_mock.return_value = True

        self._test_register_plugin_success(True)

    @patch('{}.is_call_home_enabled'.format('controllers.array_action.array_mediator_svc'))
    @patch('controllers.array_action.array_mediator_svc.SVC_REGISTRATION_CACHE')
    def test_do_not_register_plugin_when_call_home_is_not_enabled_success(self, mock_cache, is_enabled_mock):
        is_enabled_mock.return_value = False

        self._test_register_plugin_success(False)
        self.assertEqual(mock_cache.get.call_count, 0)

    def _test_register_plugin_success(self, should_register):
        self.svc.register_plugin('test_key', 'some_metadata')

        if should_register:
            self.svc.sdk.svc_task_api.registerplugin_post.assert_called_once()
        else:
            self.svc.sdk.svc_task_api.registerplugin_post.assert_not_called()

    @patch('{}.is_call_home_enabled'.format('controllers.array_action.array_mediator_svc'))
    @patch('{}.get_odf_call_home_version'.format('controllers.array_action.array_mediator_svc'))
    @patch('controllers.array_action.array_mediator_svc.SVC_REGISTRATION_CACHE')
    def test_register_plugin_with_odf_success(self, mock_cache, mock_odf_version, is_enabled_mock):
        current_time = datetime.now()
        three_hours_ago = current_time - timedelta(hours=3)
        mock_cache.get.return_value = {'test_key': three_hours_ago}
        mock_odf_version.return_value = '1.7.0'
        is_enabled_mock.return_value = True

        self.svc.register_plugin('test_key', 'some_metadata')

        call_1 = call(name='block.csi.ibm.com',
                      uniquekey='test_key',
                      version=config.identity.version,
                      metadata='some_metadata')

        call_2 = call(name='odf.ibm.com',
                      uniquekey='basic',
                      version='1.7.0')

        self.svc.sdk.svc_task_api.registerplugin_post.assert_called()

    def _mock_vg_replication(self, partition_name='', local_location='1', dr_link_status='running',
                             loc2_system_name='', loc3_system_name='', loc2_status='', loc3_status='',
                             loc2_within_rpo='', loc3_within_rpo='', loc2_running_rp='', loc3_running_rp=''):
        return Munch({
            'partition_name': partition_name,
            'local_location': local_location,
            'dr_link_status': dr_link_status,
            'location2_system_name': loc2_system_name,
            'location2_status': loc2_status,
            'location2_within_rpo': loc2_within_rpo,
            'location2_running_recovery_point': loc2_running_rp,
            'location3_system_name': loc3_system_name,
            'location3_status': loc3_status,
            'location3_within_rpo': loc3_within_rpo,
            'location3_running_recovery_point': loc3_running_rp,
            'replication_policy_name': REPLICATION_NAME,
        })

    # --- _get_recovery_location_index tests ---
    def test_get_recovery_location_index_non_partition_local_at_location1_returns_location2(self):
        vg_replication = self._mock_vg_replication(partition_name='', local_location='1')
        result = self.svc._get_recovery_location_index(vg_replication)
        self.assertEqual('2', result)

    def test_get_recovery_location_index_non_partition_local_at_location2_returns_location1(self):
        vg_replication = self._mock_vg_replication(partition_name='', local_location='2')
        result = self.svc._get_recovery_location_index(vg_replication)
        self.assertEqual('1', result)

    def test_get_recovery_location_index_partition_local_at_location1_returns_location3(self):
        vg_replication = self._mock_vg_replication(partition_name='SKGPTN0', local_location='1')
        result = self.svc._get_recovery_location_index(vg_replication)
        self.assertEqual('3', result)

    def test_get_recovery_location_index_partition_local_at_location3_returns_location1(self):
        vg_replication = self._mock_vg_replication(partition_name='SKGPTN0', local_location='3')
        result = self.svc._get_recovery_location_index(vg_replication)
        self.assertEqual('1', result)

    # --- _map_dr_link_status_to_proto_status tests ---
    def test_map_dr_link_status_healthy_states(self):
        for status in HEALTHY_DR_LINK_STATES:
            with self.subTest(status=status):
                result = self.svc._map_dr_link_status_to_proto_status(status)
                self.assertEqual(ReplicationStatus.HEALTHY, result)

    def test_map_dr_link_status_degraded_states(self):
        for status in DEGRADED_DR_LINK_STATES:
            with self.subTest(status=status):
                result = self.svc._map_dr_link_status_to_proto_status(status)
                self.assertEqual(ReplicationStatus.DEGRADED, result)

    def test_map_dr_link_status_failed_states(self):
        for status in FAILED_DR_LINK_STATES:
            with self.subTest(status=status):
                result = self.svc._map_dr_link_status_to_proto_status(status)
                self.assertEqual(ReplicationStatus.ERROR, result)

    def test_map_dr_link_status_unknown_returns_unknown(self):
        result = self.svc._map_dr_link_status_to_proto_status('some_unknown_state')
        self.assertEqual(ReplicationStatus.UNKNOWN, result)

    # --- _build_replication_info tests ---
    def test_build_replication_info(self):
        vg_replication = self._mock_vg_replication(
            partition_name='',
            dr_link_status='running',
            loc2_status='healthy',
            loc2_within_rpo='yes',
            loc2_running_rp='0',
        )

        result = self.svc._build_replication_info(vg_replication)

        self.assertIsInstance(result, ReplicationInfo)
        self.assertIsNotNone(result.last_sync_time)
        self.assertEqual(ReplicationStatus.HEALTHY, result.replication_status)
        self.assertIsNotNone(result.status_message)

    # --- get_last_async_snapshot_info tests ---
    def test_get_last_async_snapshot_info_returns_replication_info(self):
        vg_replication = self._mock_vg_replication(partition_name='', dr_link_status='running')
        self.svc.sdk.svc_info_api.lsvolumegroupreplication_id_post.return_value = vg_replication

        result = self.svc.get_last_async_snapshot_info(OBJECT_INTERNAL_ID)

        self.svc.sdk.svc_info_api.lsvolumegroupreplication_id_post.assert_called_once()
        self.assertIsInstance(result, ReplicationInfo)
        self.assertIsNotNone(result.last_sync_time)
        self.assertEqual(ReplicationStatus.HEALTHY, result.replication_status)

    def test_get_last_async_snapshot_info_no_record_raises_object_not_found(self):
        self.svc.sdk.svc_info_api.lsvolumegroupreplication_id_post.return_value = None
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.get_last_async_snapshot_info(OBJECT_INTERNAL_ID)

    # --- _demote_ear_replication_volume skip-on-disconnect tests ---
    def test_demote_ear_replication_volume_skips_when_non_partition_remote_disconnected(self):
        self.svc.sdk.svc_task_api.chvolumereplicationinternals_id_post = Mock()
        vg_replication = self._mock_vg_replication(partition_name='', dr_link_status=DR_LINK_STATUS_DISCONNECTED)
        self.svc.sdk.svc_info_api.lsvolumegroupreplication_id_post.return_value = vg_replication

        self.svc._demote_ear_replication_volume(REPLICATION_NAME)

        self.svc.sdk.svc_task_api.chvolumegroupreplication_id_post.assert_not_called()

    def test_demote_ear_replication_volume_skips_when_partition_remote_suspended_missing_connectivity(self):
        self.svc.sdk.svc_task_api.chvolumereplicationinternals_id_post = Mock()
        vg_replication = self._mock_vg_replication(partition_name='SKGPTN0',
                                                   dr_link_status=DR_LINK_STATUS_SUSPENDED_MISSING_CONNECTIVITY)
        self.svc.sdk.svc_info_api.lsvolumegroupreplication_id_post.return_value = vg_replication

        self.svc._demote_ear_replication_volume(REPLICATION_NAME)

        self.svc.sdk.svc_task_api.chvolumegroupreplication_id_post.assert_not_called()

    # --- get_replication_destination_info tests ---

    def _mock_dr_mediator(self, vdisk_uid=common_settings.DESTINATION_VOLUME_UID):
        dest_cli_volume = self._get_cli_volume(in_volume_group=True, vdisk_uid=vdisk_uid)
        dr_mediator = Mock()
        dr_mediator._get_cli_volume.return_value = dest_cli_volume
        dr_mediator._generate_thin_volume_response.return_value = self.svc._generate_thin_volume_response(
            dest_cli_volume)
        return dr_mediator

    def _mock_lsvolumegroupreplication(self, vg_replication=None):
        self.svc.sdk.svc_info_api.lsvolumegroupreplication_id_post.return_value = vg_replication

    def test_get_replication_destination_info_without_internal_id_resolves_from_uid(self):
        cli_volume = self._get_cli_volume(
            in_volume_group=True,
            remote_volume_uid=common_settings.DUMMY_REMOTE_VOLUME_UID,
            remote_internal_id=common_settings.DUMMY_REMOTE_INTERNAL_ID)
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._mock_cli_object(cli_volume)
        self._mock_lsvolumegroupreplication(self._mock_vg_replication(partition_name=''))

        result = self.svc.get_replication_destination_info("", common_settings.VOLUME_UID)

        self.svc.sdk.svc_info_api.lsvdisk_post.assert_called()
        self.assertIsNotNone(result)

    def test_get_replication_destination_info_without_internal_id_not_found_raises(self):
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._mock_cli_object(None)

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.svc.get_replication_destination_info("", common_settings.VOLUME_UID)

    def test_get_replication_destination_info_primary_path_non_partition(self):
        cli_volume = self._get_cli_volume(
            in_volume_group=True,
            remote_volume_uid=common_settings.DUMMY_REMOTE_VOLUME_UID,
            remote_internal_id=common_settings.DUMMY_REMOTE_INTERNAL_ID,
            partition_name='')
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._mock_cli_object(cli_volume)
        self._mock_lsvolumegroupreplication(self._mock_vg_replication(partition_name=''))

        result = self.svc.get_replication_destination_info(OBJECT_INTERNAL_ID, common_settings.VOLUME_UID)

        self.assertIsNotNone(result)
        self.assertIn(common_settings.DUMMY_REMOTE_VOLUME_UID, result)
        self.assertIn(common_settings.DUMMY_REMOTE_INTERNAL_ID, result)
        self.assertNotIn(common_settings.VOLUME_UID, result)

    def test_get_replication_destination_info_primary_path_partition(self):
        cli_volume = self._get_cli_volume(
            in_volume_group=True,
            remote_volume_uid=common_settings.DUMMY_REMOTE_VOLUME_UID,
            remote_internal_id=common_settings.DUMMY_REMOTE_INTERNAL_ID,
            partition_name='SKGPTN0')
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._mock_cli_object(cli_volume)
        self._mock_lsvolumegroupreplication(self._mock_vg_replication(partition_name='SKGPTN0'))

        result = self.svc.get_replication_destination_info(OBJECT_INTERNAL_ID, common_settings.VOLUME_UID)

        self.assertIsNotNone(result)
        self.assertIn(common_settings.DUMMY_REMOTE_VOLUME_UID, result)
        self.assertIn(common_settings.DUMMY_REMOTE_INTERNAL_ID, result)
        self.assertNotIn(common_settings.VOLUME_UID, result)

    def test_get_replication_destination_info_primary_path_fields_not_ready_returns_none(self):
        cli_volume = self._get_cli_volume(in_volume_group=True)
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._mock_cli_object(cli_volume)
        self._mock_lsvolumegroupreplication(self._mock_vg_replication(partition_name=''))

        result = self.svc.get_replication_destination_info(OBJECT_INTERNAL_ID, common_settings.VOLUME_UID)

        self.assertIsNone(result)
        self.svc.sdk.svc_info_api.lsvolumegroupreplication_id_post.assert_called_once()

    def test_get_replication_destination_info_fallback_returns_destination_handle(self):
        cli_volume = self._get_cli_volume(in_volume_group=True, remote_volume_uid_supported=False)
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._mock_cli_object(cli_volume)

        result = self.svc.get_replication_destination_info(
            OBJECT_INTERNAL_ID, common_settings.VOLUME_UID, dr_mediator=self._mock_dr_mediator())

        self.assertIsNotNone(result)
        self.assertIn(common_settings.DESTINATION_VOLUME_UID, result)
        self.assertNotIn(common_settings.VOLUME_UID, result)

    def test_get_replication_destination_info_fallback_destination_not_available_returns_none(self):
        cli_volume = self._get_cli_volume(in_volume_group=True, remote_volume_uid_supported=False)
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._mock_cli_object(cli_volume)
        dr_mediator = Mock()
        dr_mediator._get_cli_volume.return_value = None

        result = self.svc.get_replication_destination_info(
            OBJECT_INTERNAL_ID, common_settings.VOLUME_UID, dr_mediator=dr_mediator)

        self.assertIsNone(result)

    def test_get_replication_destination_info_no_dr_mediator_and_field_unsupported_raises(self):
        cli_volume = self._get_cli_volume(in_volume_group=True, remote_volume_uid_supported=False)
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._mock_cli_object(cli_volume)

        with self.assertRaises(array_errors.ReplicationDestinationInfoNotSupportedError):
            self.svc.get_replication_destination_info(
                OBJECT_INTERNAL_ID, common_settings.VOLUME_UID, dr_mediator=None)

    def test_get_replication_destination_info_force_dr_mediator_uses_remote_even_if_supported(self):
        cli_volume = self._get_cli_volume(
            in_volume_group=True,
            remote_volume_uid=common_settings.DUMMY_REMOTE_VOLUME_UID,
            remote_internal_id=common_settings.DUMMY_REMOTE_INTERNAL_ID)
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._mock_cli_object(cli_volume)

        result = self.svc.get_replication_destination_info(
            OBJECT_INTERNAL_ID, common_settings.VOLUME_UID,
            dr_mediator=self._mock_dr_mediator(), force_dr_mediator=True)

        self.assertIsNotNone(result)
        self.assertIn(common_settings.DESTINATION_VOLUME_UID, result)
        self.svc.sdk.svc_info_api.lsvolumegroupreplication_id_post.assert_not_called()

    def test_get_replication_destination_info_force_dr_mediator_no_dr_mediator_raises(self):
        cli_volume = self._get_cli_volume(
            in_volume_group=True,
            remote_volume_uid=common_settings.DUMMY_REMOTE_VOLUME_UID,
            remote_internal_id=common_settings.DUMMY_REMOTE_INTERNAL_ID)
        self.svc.sdk.svc_info_api.lsvdisk_post.return_value = self._mock_cli_object(cli_volume)

        with self.assertRaises(array_errors.ReplicationDestinationInfoNotSupportedError):
            self.svc.get_replication_destination_info(
                OBJECT_INTERNAL_ID, common_settings.VOLUME_UID, dr_mediator=None, force_dr_mediator=True)
