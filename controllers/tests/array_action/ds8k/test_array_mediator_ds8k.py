import unittest

from mock import patch, NonCallableMagicMock, Mock
from munch import Munch
from pyds8k.exceptions import ClientError, ClientException, InternalServerError, NotFound

import controllers.array_action.errors as array_errors
import controllers.tests.array_action.ds8k.test_settings as ds8k_settings
import controllers.tests.array_action.test_settings as array_settings
import controllers.tests.common.test_settings as common_settings
from controllers.array_action.array_action_types import Volume, Snapshot
from controllers.array_action.array_mediator_ds8k import DS8KArrayMediator, FLASHCOPY_PERSISTENT_OPTION, \
    FLASHCOPY_PERMIT_SPACE_EFFICIENT_TARGET_OPTION
from controllers.array_action.array_mediator_ds8k import LOGIN_PORT_WWPN, LOGIN_PORT_STATE, \
    LOGIN_PORT_STATE_ONLINE
from controllers.common.node_info import Initiators
from controllers.common.settings import SPACE_EFFICIENCY_THIN, SPACE_EFFICIENCY_NONE


class TestArrayMediatorDS8K(unittest.TestCase):

    def setUp(self):
        self.endpoint = ["1.2.3.4"]
        self.client_mock = NonCallableMagicMock()
        patcher = patch("controllers.array_action.array_mediator_ds8k.RESTClient")
        self.connect_mock = patcher.start()
        self.addCleanup(patcher.stop)
        self.connect_mock.return_value = self.client_mock

        self.client_mock.get_system.return_value = Munch(
            {
                ds8k_settings.GET_SYSTEM_ID_ATTR_KEY: ds8k_settings.DUMMY_GET_SYSTEM_ID,
                ds8k_settings.GET_SYSTEM_BUNDLE_ATTR_KEY: ds8k_settings.DUMMY_SYSTEM_BUNDLE,
                ds8k_settings.GET_SYSTEM_WWNN_ATTR_KEY: ds8k_settings.DUMMY_SYSTEM_WWNN,
            }
        )

        self.volume_response = self._get_volume_response(ds8k_settings.DUMMY_VOLUME_ID1,
                                                         common_settings.VOLUME_NAME,
                                                         space_efficiency=ds8k_settings.DS8K_SPACE_EFFICIENCY_THIN)

        self.snapshot_response = self._get_volume_response(ds8k_settings.DUMMY_VOLUME_ID2,
                                                           common_settings.SNAPSHOT_NAME)

        self.flashcopy_response = self._get_flashcopy_response(ds8k_settings.DUMMY_VOLUME_ID1,
                                                               ds8k_settings.DUMMY_VOLUME_ID2,
                                                               ds8k_settings.ENABLED_BACKGROUND_COPY
                                                               )

        self.array = DS8KArrayMediator(common_settings.SECRET_USERNAME_VALUE, common_settings.SECRET_PASSWORD_VALUE,
                                       self.endpoint)
        self.array.volume_cache = Mock()

    def _get_volume_response(self, volume_id, volume_name, cap=ds8k_settings.DUMMY_VOLUME_CAPACITY,
                             pool=common_settings.DUMMY_POOL1,
                             space_efficiency=SPACE_EFFICIENCY_NONE):
        return Munch({ds8k_settings.VOLUME_CAPACITY_ATTR_KEY: cap,
                      array_settings.VOLUME_ID_ATTR_KEY: volume_id,
                      array_settings.VOLUME_NAME_ATTR_KEY: volume_name,
                      ds8k_settings.VOLUME_POOL_ID_ATTR_KEY: pool,
                      ds8k_settings.VOLUME_SE_ATTR_KEY: space_efficiency,
                      ds8k_settings.VOLUME_FLASHCOPY_ATTR_KEY: ""}
                     )

    def _get_flashcopy_id(self, source_volume, target_volume):
        return ds8k_settings.FLASHCOPY_ID_DELIMITER.join([source_volume, target_volume])

    def _get_flashcopy_response(self, source_volume, target_volume,
                                background_copy=ds8k_settings.ENABLED_BACKGROUND_COPY,
                                state=ds8k_settings.VALID_STATE):
        return Munch(
            {ds8k_settings.FLASHCOPY_SOURCE_VOLUME_ATTR_KEY: source_volume,
             ds8k_settings.FLASHCOPY_TARGET_VOLUME_ATTR_KEY: target_volume,
             ds8k_settings.FLASHCOPY_ID_ATTR_KEY: self._get_flashcopy_id(source_volume, target_volume),
             ds8k_settings.FLASHCOPY_STATE_ATTR_KEY: state,
             ds8k_settings.FLASHCOPY_BACKGROUND_COPY_ATTR_KEY: background_copy,
             ds8k_settings.PYDS8K_REPRESENTATION_KEY: {}
             }
        )

    def test_connect_with_incorrect_credentials(self):
        self.client_mock.get_system.side_effect = \
            ClientError("400", "BE7A002D")
        with self.assertRaises(array_errors.CredentialsError):
            DS8KArrayMediator(common_settings.SECRET_USERNAME_VALUE, common_settings.SECRET_PASSWORD_VALUE,
                              self.endpoint)

    def test_connect_to_unsupported_system(self):
        self.client_mock.get_system.return_value = \
            Munch({ds8k_settings.GET_SYSTEM_BUNDLE_ATTR_KEY: ds8k_settings.DUMMY_UNSUPPORTED_SYSTEM_BUNDLE})
        with self.assertRaises(array_errors.UnsupportedStorageVersionError):
            DS8KArrayMediator(common_settings.SECRET_USERNAME_VALUE, common_settings.SECRET_PASSWORD_VALUE,
                              self.endpoint)

    def test_connect_with_error(self):
        self.client_mock.get_system.side_effect = \
            ClientError("400", array_settings.DUMMY_ERROR_MESSAGE)
        with self.assertRaises(ClientError) as ex:
            DS8KArrayMediator(common_settings.SECRET_USERNAME_VALUE, common_settings.SECRET_PASSWORD_VALUE,
                              self.endpoint)
        self.assertEqual(array_settings.DUMMY_ERROR_MESSAGE, ex.exception.message)

    def test_validate_space_efficiency_thin_success(self):
        self.array.validate_supported_space_efficiency(
            SPACE_EFFICIENCY_THIN
        )

    def test_validate_space_efficiency_none_success(self):
        self.array.validate_supported_space_efficiency(
            SPACE_EFFICIENCY_NONE
        )

    def test_validate_space_efficiency_fail(self):
        with self.assertRaises(array_errors.SpaceEfficiencyNotSupported):
            self.array.validate_supported_space_efficiency(ds8k_settings.UNSUPPORTED_SPACE_EFFICIENCY)

    def test_get_volume_with_no_pool(self):
        with self.assertRaises(array_errors.PoolParameterIsMissing):
            self.array.get_volume(common_settings.VOLUME_NAME, None, False)

    def _test_get_volume(self, with_cache=False):
        if with_cache:
            self.array.volume_cache.get.return_value = self.volume_response.id
            self.client_mock.get_volume.return_value = self.volume_response
        else:
            self.array.volume_cache.get.return_value = None
            self.client_mock.get_volumes_by_pool.return_value = [self.volume_response]
        volume = self.array.get_volume(self.volume_response.name, pool=self.volume_response.pool,
                                       is_virt_snap_func=False)

        self.assertEqual(self.volume_response.name, volume.name)
        self.array.volume_cache.add_or_delete.assert_called_once_with(self.volume_response.name,
                                                                      self.volume_response.id)

    def test_get_volume_with_empty_cache(self, ):
        self._test_get_volume()
        self.client_mock.get_volumes_by_pool.assert_called_once_with(self.volume_response.pool)

    def test_get_volume_with_volume_in_cache(self):
        self._test_get_volume(with_cache=True)
        self.client_mock.get_volume.assert_called_once_with(self.volume_response.id)
        self.client_mock.get_volumes_by_pool.assert_not_called()

    def test_get_volume_with_pool_context(self):
        self.client_mock.get_volumes_by_pool.return_value = [
            self.volume_response,
        ]
        volume = self.array.get_volume(self.volume_response.name, pool=self.volume_response.pool,
                                       is_virt_snap_func=False)
        self.assertEqual(self.volume_response.name, volume.name)
        self.client_mock.get_volumes_by_pool.assert_called_once_with(self.volume_response.pool)

    def test_get_volume_with_pool_context_not_found(self):
        self.client_mock.get_volumes_by_pool.return_value = [
            self.volume_response,
        ]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.get_volume(ds8k_settings.VOLUME_FAKE_NAME, pool=self.volume_response.pool,
                                  is_virt_snap_func=False)

    def test_create_volume_with_default_space_efficiency_success(self):
        self._test_create_volume_success(space_efficiency=SPACE_EFFICIENCY_NONE)

    def test_create_volume_with_thin_space_efficiency_success(self):
        self._test_create_volume_success(space_efficiency=SPACE_EFFICIENCY_THIN)

    def test_create_volume_with_empty_space_efficiency_success(self):
        self._test_create_volume_success(space_efficiency="")

    def test_create_volume_with_empty_cache(self):
        self._test_create_volume_success()
        self.array.volume_cache.add.assert_called_once_with(self.volume_response.name, self.volume_response.id)

    def _test_create_volume_success(self, space_efficiency=""):
        self.client_mock.create_volume.return_value = self.volume_response
        self.client_mock.get_volume.return_value = self.volume_response
        name = self.volume_response.name
        size_in_bytes = self.volume_response.cap
        pool_id = self.volume_response.pool
        volume = self.array.create_volume(
            name, size_in_bytes, space_efficiency, pool_id, None, None, None, None, False)
        if space_efficiency == SPACE_EFFICIENCY_THIN:
            space_efficiency = ds8k_settings.DS8K_SPACE_EFFICIENCY_THIN
        else:
            space_efficiency = SPACE_EFFICIENCY_NONE
        self.client_mock.create_volume.assert_called_once_with(
            pool_id=pool_id,
            capacity_in_bytes=self.volume_response.cap,
            thin_provisioning=space_efficiency,
            name=common_settings.VOLUME_NAME,
        )
        self.assertEqual(self.volume_response.name, volume.name)

    def test_create_volume_fail_with_client_exception(self):
        self.client_mock.create_volume.side_effect = ClientException("500")
        with self.assertRaises(array_errors.VolumeCreationError):
            self.array.create_volume(common_settings.VOLUME_NAME, 1, SPACE_EFFICIENCY_THIN, common_settings.DUMMY_POOL1,
                                     None, None, None, None, False)

    def test_create_volume_fail_with_pool_not_found(self):
        self.client_mock.create_volume.side_effect = NotFound("404", message="BE7A0001")
        with self.assertRaises(array_errors.PoolDoesNotExist):
            self.array.create_volume(common_settings.VOLUME_NAME, 1, SPACE_EFFICIENCY_THIN, common_settings.DUMMY_POOL1,
                                     None, None, None, None, False)

    def test_create_volume_fail_with_incorrect_id(self):
        self.client_mock.create_volume.side_effect = InternalServerError("500", message="BE7A0005")
        with self.assertRaises(array_errors.PoolDoesNotExist):
            self.array.create_volume(common_settings.VOLUME_NAME, 1, SPACE_EFFICIENCY_THIN, common_settings.DUMMY_POOL1,
                                     None, None, None, None, False)

    def test_create_volume_fail_with_no_space_in_pool(self):
        self.client_mock.create_volume.side_effect = InternalServerError("500", message="BE534459")
        with self.assertRaises(array_errors.NotEnoughSpaceInPool):
            self.array.create_volume(common_settings.VOLUME_NAME, 1, SPACE_EFFICIENCY_THIN, common_settings.DUMMY_POOL1,
                                     None, None, None, None, False)

    def test_delete_volume(self):
        scsi_id = ds8k_settings.DUMMY_ABSTRACT_VOLUME_UID.format(self.volume_response.id)
        self.array.delete_volume(scsi_id)
        self.client_mock.delete_volume.assert_called_once_with(volume_id=self.volume_response.id)

    def test_delete_volume_with_remove_from_cache(self):
        self.client_mock.get_volume.return_value = self.volume_response
        scsi_id = ds8k_settings.DUMMY_ABSTRACT_VOLUME_UID.format(self.volume_response.id)
        self.array.delete_volume(scsi_id)
        self.array.volume_cache.remove.assert_called_once_with(self.volume_response.name)

    def test_delete_volume_fail_with_client_exception(self):
        self.client_mock.delete_volume.side_effect = ClientException("500")
        with self.assertRaises(array_errors.VolumeDeletionError):
            self.array.delete_volume(common_settings.VOLUME_UID)

    def test_delete_volume_fail_with_not_found(self):
        self.client_mock.delete_volume.side_effect = NotFound("404")
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.delete_volume(common_settings.VOLUME_UID)

    def test_delete_volume_failed_with_illegal_object_id(self):
        self.client_mock.get_volume.side_effect = InternalServerError("500", message="BE7A0005")
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.array.delete_volume(common_settings.VOLUME_UID)

    def test_delete_volume_with_flashcopies_as_source_and_target_fail(self):
        self.client_mock.get_volume.return_value = self.volume_response
        self.client_mock.get_flashcopies_by_volume.return_value = [
            self._get_flashcopy_response(ds8k_settings.DUMMY_VOLUME_ID1, ds8k_settings.DUMMY_VOLUME_ID2,
                                         ds8k_settings.DISABLED_BACKGROUND_COPY),
            self._get_flashcopy_response(ds8k_settings.DUMMY_VOLUME_ID3, ds8k_settings.DUMMY_VOLUME_ID1,
                                         ds8k_settings.ENABLED_BACKGROUND_COPY)
        ]
        self.client_mock.get_flashcopies.return_value = Munch(
            {ds8k_settings.FLASHCOPY_OUT_OF_SYNC_TRACKS_ATTR_KEY: ds8k_settings.DUMMY_ZERO_OUT_OF_SYNC_TRACKS})
        self.client_mock.delete_volume.side_effect = ClientException("500")
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.array.delete_volume(ds8k_settings.DUMMY_VOLUME_ID1)

    def _prepare_mocks_for_volume(self):
        self.client_mock.get_flashcopies_by_volume.return_value = [self.flashcopy_response]
        self.client_mock.get_flashcopies.return_value = Munch(
            {ds8k_settings.FLASHCOPY_OUT_OF_SYNC_TRACKS_ATTR_KEY: ds8k_settings.DUMMY_ZERO_OUT_OF_SYNC_TRACKS,
             ds8k_settings.FLASHCOPY_TARGET_VOLUME_ATTR_KEY: ds8k_settings.DUMMY_VOLUME_ID2,
             ds8k_settings.PYDS8K_REPRESENTATION_KEY: {}})
        volume = self.volume_response
        self.client_mock.get_volume.return_value = volume
        return volume

    def test_delete_volume_with_snapshot_as_source(self):
        self._prepare_mocks_for_volume()
        flashcopy = self.flashcopy_response
        flashcopy.backgroundcopy = ds8k_settings.DISABLED_BACKGROUND_COPY
        self.client_mock.get_flashcopies_by_volume.return_value = [flashcopy]
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.array.delete_volume(ds8k_settings.DUMMY_VOLUME_ID1)

    def test_delete_volume_with_flashcopy_still_copying(self):
        self._prepare_mocks_for_volume()
        self.client_mock.get_flashcopies.return_value.out_of_sync_tracks = ds8k_settings.DUMMY_OUT_OF_SYNC_TRACKS
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.array.delete_volume(ds8k_settings.DUMMY_VOLUME_ID1)
        self.client_mock.delete_flashcopy.assert_not_called()

    def test_delete_volume_with_flashcopy_as_source_deleted(self):
        self._prepare_mocks_for_volume()
        self.array.delete_volume(ds8k_settings.DUMMY_VOLUME_ID1)
        self.client_mock.delete_flashcopy.assert_called_once()

    def test_delete_volume_with_flashcopy_as_target_success(self):
        self._prepare_mocks_for_volume()
        self.array.delete_volume(ds8k_settings.DUMMY_VOLUME_ID1)
        self.client_mock.delete_flashcopy.assert_called_once_with(self._get_flashcopy_id(ds8k_settings.DUMMY_VOLUME_ID1,
                                                                                         ds8k_settings.DUMMY_VOLUME_ID2)
                                                                  )
        self.client_mock.delete_volume.assert_called_once_with(volume_id=ds8k_settings.DUMMY_VOLUME_ID1)

    def test_get_volume_mappings_fail_with_client_exception(self):
        self.client_mock.get_hosts.side_effect = ClientException("500")
        with self.assertRaises(ClientException):
            self.array.get_volume_mappings(common_settings.VOLUME_NAME)

    def _mock_get_hosts_response(self, volume_id=ds8k_settings.DUMMY_VOLUME_ID3, lun_id=array_settings.DUMMY_LUN_ID,
                                 wwpn1=array_settings.DUMMY_FC_WWN1,
                                 wwpn2=array_settings.DUMMY_FC_WWN2, host_name=common_settings.HOST_NAME):
        return [
            Munch({
                ds8k_settings.GET_HOSTS_MAPPINGS_BRIEFS_ATTR_KEY: [{
                    ds8k_settings.GET_HOSTS_VOLUME_ID_ATTR_KEY: volume_id,
                    ds8k_settings.GET_HOSTS_LUN_ID_ATTR_KEY: lun_id,
                }],
                ds8k_settings.GET_HOST_LOGIN_PORTS_ATTR_KEY: [
                    {
                        LOGIN_PORT_WWPN: wwpn1,
                        LOGIN_PORT_STATE: LOGIN_PORT_STATE_ONLINE,
                    },
                    {
                        LOGIN_PORT_WWPN: wwpn2,
                        LOGIN_PORT_STATE: ds8k_settings.LOGIN_PORT_STATE_OFFLINE,
                    }
                ],
                ds8k_settings.GET_HOSTS_NAME_ATTR_KEY: host_name,
                ds8k_settings.GET_HOSTS_HOST_PORTS_BRIEFS_ATTR_KEY: [{ds8k_settings.GET_HOSTS_WWPN_ATTR_KEY: wwpn1},
                                                                     {ds8k_settings.GET_HOSTS_WWPN_ATTR_KEY: wwpn2}]
            })
        ]

    def test_get_volume_mappings_found_nothing(self):
        volume_id = ds8k_settings.DUMMY_VOLUME_ID1
        scsi_id = ds8k_settings.DUMMY_ABSTRACT_VOLUME_UID.format(volume_id)
        self.client_mock.get_hosts.return_value = self._mock_get_hosts_response()
        self.assertDictEqual(self.array.get_volume_mappings(scsi_id), {})

    def test_get_volume_mappings(self):
        volume_id = ds8k_settings.DUMMY_VOLUME_ID1
        lunid = array_settings.DUMMY_LUN_ID
        scsi_id = ds8k_settings.DUMMY_ABSTRACT_VOLUME_UID.format(volume_id)
        self.client_mock.get_hosts.return_value = self._mock_get_hosts_response(volume_id=volume_id, lun_id=lunid)
        self.assertDictEqual(self.array.get_volume_mappings(scsi_id), {common_settings.HOST_NAME: int(lunid)})

    def test_map_volume_host_not_found(self):
        self.client_mock.map_volume_to_host.side_effect = NotFound("404")
        with self.assertRaises(array_errors.HostNotFoundError):
            self.array.map_volume(common_settings.VOLUME_NAME, common_settings.HOST_NAME,
                                  array_settings.DUMMY_CONNECTIVITY_TYPE)

    def test_map_volume_volume_not_found(self):
        self.client_mock.map_volume_to_host.side_effect = ClientException("500", "[BE586015]")
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.map_volume(common_settings.VOLUME_NAME, common_settings.HOST_NAME,
                                  array_settings.DUMMY_CONNECTIVITY_TYPE)

    def test_map_volume_no_available_lun(self):
        self.client_mock.map_volume_to_host.side_effect = InternalServerError("500", "[BE74121B]")
        with self.assertRaises(array_errors.NoAvailableLunError):
            self.array.map_volume(common_settings.VOLUME_NAME, common_settings.HOST_NAME,
                                  array_settings.DUMMY_CONNECTIVITY_TYPE)

    def test_map_volume_fail_with_client_exception(self):
        self.client_mock.map_volume_to_host.side_effect = ClientException("500")
        with self.assertRaises(array_errors.MappingError):
            self.array.map_volume(common_settings.VOLUME_NAME, common_settings.HOST_NAME,
                                  array_settings.DUMMY_CONNECTIVITY_TYPE)

    def test_map_volume(self):
        scsi_id = ds8k_settings.DUMMY_VOLUME_UID
        host_name = common_settings.VOLUME_NAME
        connectivity_type = array_settings.DUMMY_CONNECTIVITY_TYPE
        self.client_mock.map_volume_to_host.return_value = Munch({
            ds8k_settings.GET_HOSTS_LUN_ID_ATTR_KEY: array_settings.DUMMY_LUN_ID})
        lun = self.array.map_volume(scsi_id, host_name, connectivity_type)
        self.assertEqual(5, lun)
        self.client_mock.map_volume_to_host.assert_called_once_with(host_name, scsi_id[-4:])

    def test_unmap_volume_host_not_found(self):
        self.client_mock.get_host_mappings.side_effect = NotFound("404", message="BE7A0016")
        with self.assertRaises(array_errors.HostNotFoundError):
            self.array.unmap_volume(common_settings.VOLUME_NAME, common_settings.HOST_NAME)

    def test_unmap_volume_already_unmapped(self):
        self.client_mock.get_host_mappings.side_effect = NotFound("404", message="BE7A001F")
        with self.assertRaises(array_errors.VolumeAlreadyUnmappedError):
            self.array.unmap_volume(common_settings.VOLUME_NAME, common_settings.HOST_NAME)

    def test_unmap_volume_volume_not_found(self):
        self.client_mock.get_host_mappings.return_value = []
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.unmap_volume(common_settings.VOLUME_NAME, common_settings.HOST_NAME)

    def test_unmap_volume_fail_with_client_exception(self):
        volume_id = ds8k_settings.DUMMY_VOLUME_ID1
        lunid = array_settings.DUMMY_LUN_ID
        host_name = common_settings.HOST_NAME
        scsi_id = ds8k_settings.DUMMY_ABSTRACT_VOLUME_UID.format(volume_id)
        self.client_mock.get_host_mappings.return_value = [
            Munch({
                ds8k_settings.GET_HOST_MAPPINGS_VOLUME_ATTR_KEY: volume_id,
                ds8k_settings.GET_HOST_MAPPINGS_ID_ATTR_KEY: lunid
            })
        ]
        self.client_mock.unmap_volume_from_host.side_effect = ClientException("500")
        with self.assertRaises(array_errors.UnmappingError):
            self.array.unmap_volume(scsi_id, host_name)

    def test_unmap_volume(self):
        volume_id = ds8k_settings.DUMMY_VOLUME_ID1
        lunid = array_settings.DUMMY_LUN_ID
        host_name = common_settings.HOST_NAME
        scsi_id = ds8k_settings.DUMMY_ABSTRACT_VOLUME_UID.format(volume_id)
        self.client_mock.get_host_mappings.return_value = [
            Munch({
                ds8k_settings.GET_HOST_MAPPINGS_VOLUME_ATTR_KEY: volume_id,
                ds8k_settings.GET_HOST_MAPPINGS_ID_ATTR_KEY: lunid
            })
        ]
        self.array.unmap_volume(scsi_id, host_name)
        self.client_mock.unmap_volume_from_host.assert_called_once_with(host_name=host_name,
                                                                        lunid=lunid)

    def test_get_array_fc_wwns_fail_with_client_exception(self):
        self.client_mock.get_host.side_effect = ClientException("500")
        with self.assertRaises(ClientException):
            self.array.get_array_fc_wwns(None)

    def test_get_array_fc_wwns_skip_offline_port(self):
        wwpn1 = array_settings.DUMMY_FC_WWN1
        self.client_mock.get_host.return_value = self._mock_get_hosts_response()[0]
        self.assertListEqual(self.array.get_array_fc_wwns(None), [wwpn1])

    def test_get_array_fc_wwns(self):
        wwpn = array_settings.DUMMY_FC_WWN1
        self.client_mock.get_host.return_value = self._mock_get_hosts_response()[0]
        self.assertListEqual(self.array.get_array_fc_wwns(None), [wwpn])

    def test_get_host_by_name_success(self):
        self.client_mock.get_host.return_value = \
            self._mock_get_hosts_response(host_name=array_settings.DUMMY_HOST_NAME1)[0]
        host = self.array.get_host_by_name(array_settings.DUMMY_HOST_NAME1)
        self.assertEqual(array_settings.DUMMY_HOST_NAME1, host.name)
        self.assertEqual([array_settings.FC_CONNECTIVITY_TYPE], host.connectivity_types)
        self.assertEqual([], host.initiators.nvme_nqns)
        self.assertEqual([array_settings.DUMMY_FC_WWN1, array_settings.DUMMY_FC_WWN2], host.initiators.fc_wwns)
        self.assertEqual([], host.initiators.iscsi_iqns)

    def test_get_host_by_name_raise_host_not_found(self):
        self.client_mock.get_host.side_effect = NotFound("404", message="BE7A0001")
        with self.assertRaises(array_errors.HostNotFoundError):
            self.array.get_host_by_name(array_settings.DUMMY_HOST_NAME1)

    def test_get_host_by_identifiers(self):
        wwpn1 = array_settings.DUMMY_FC_WWN1
        wwpn2 = array_settings.DUMMY_FC_WWN2
        self.client_mock.get_hosts.return_value = self._mock_get_hosts_response()
        host, connectivity_type = self.array.get_host_by_host_identifiers(
            Initiators([], [wwpn1, wwpn2], [])
        )
        self.assertEqual(common_settings.HOST_NAME, host)
        self.assertEqual([array_settings.FC_CONNECTIVITY_TYPE], connectivity_type)

    def test_get_host_by_identifiers_partial_match(self):
        wwpn1 = array_settings.DUMMY_FC_WWN1
        self.client_mock.get_hosts.return_value = self._mock_get_hosts_response()
        host, connectivity_type = self.array.get_host_by_host_identifiers(
            Initiators([], [wwpn1, array_settings.DUMMY_FC_WWN3], [])
        )
        self.assertEqual(host, common_settings.HOST_NAME)
        self.assertEqual([array_settings.FC_CONNECTIVITY_TYPE], connectivity_type)

    def test_get_host_by_identifiers_not_found(self):
        self.client_mock.get_hosts.return_value = self._mock_get_hosts_response()
        with self.assertRaises(array_errors.HostNotFoundError):
            self.array.get_host_by_host_identifiers(
                Initiators([], [array_settings.DUMMY_FC_WWN3, array_settings.DUMMY_FC_WWN4], [])
            )

    def test_get_snapshot_not_exist_return_none(self):
        self.client_mock.get_snapshot.side_effect = [ClientError("400", "BE7A002D")]
        snapshot = self.array.get_snapshot(common_settings.VOLUME_UID, common_settings.VOLUME_NAME,
                                           pool=self.volume_response.pool,
                                           is_virt_snap_func=False)
        self.assertIsNone(snapshot)

    def test_get_snapshot_get_flashcopy_not_exist_raise_error(self):
        self._prepare_mocks_for_snapshot()
        self.client_mock.get_flashcopies_by_volume.return_value = []

        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.array.get_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME,
                                    pool=self.volume_response.pool, is_virt_snap_func=False)

    def test_get_snapshot_failed_with_incorrect_id(self):
        self.client_mock.get_volume.side_effect = InternalServerError("500", message="BE7A0005")
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.array.get_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME, pool=None,
                                    is_virt_snap_func=False)

    def _test_get_snapshot_success(self, with_cache=False):
        if with_cache:
            self.array.volume_cache.get.return_value = self.snapshot_response.id
        else:
            self.array.volume_cache.get.return_value = None
        target_volume = self._prepare_mocks_for_snapshot()
        volume = self.array.get_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME,
                                         pool=self.volume_response.pool,
                                         is_virt_snap_func=False)
        self.assertEqual(volume.name, target_volume.name)
        self.array.volume_cache.add_or_delete.assert_called_once_with(target_volume.name,
                                                                      target_volume.id)

    def test_get_snapshot_with_empty_cache(self):
        self._test_get_snapshot_success()
        self.client_mock.get_volumes_by_pool.assert_called_once_with(self.snapshot_response.pool)

    def test_get_snapshot_with_volume_in_cache(self):
        self._test_get_snapshot_success(with_cache=True)
        self.client_mock.get_volume.assert_called_once_with(self.snapshot_response.id)
        self.client_mock.get_volumes_by_pool.assert_not_called()

    def test_get_snapshot_no_pool_success(self):
        target_volume = self._prepare_mocks_for_snapshot()
        volume = self.array.get_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME, pool=None,
                                         is_virt_snap_func=False)
        self.assertEqual(volume.name, target_volume.name)

    def _prepare_mocks_for_create_snapshot(self, thin_provisioning=SPACE_EFFICIENCY_NONE):
        self.client_mock.create_volume = Mock()
        self.client_mock.get_volume.side_effect = [
            self._get_volume_response(ds8k_settings.DUMMY_VOLUME_ID1, common_settings.SOURCE_VOLUME_NAME,
                                      space_efficiency=thin_provisioning),
            Mock(),
            self.snapshot_response
        ]
        self.client_mock.get_flashcopies.return_value = self.flashcopy_response

    def test_create_snapshot_create_volume_error(self):
        self.client_mock.create_volume.side_effect = ClientException("500")

        with self.assertRaises(array_errors.VolumeCreationError):
            self.array.create_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME, space_efficiency=None,
                                       pool=self.volume_response.pool, is_virt_snap_func=False)

    def test_create_snapshot_create_fcrel_error(self):
        self.client_mock.create_volume = Mock()
        self.client_mock.get_volume = Mock()
        self.client_mock.create_flashcopy.side_effect = ClientException("500")

        with self.assertRaises(Exception):
            self.array.create_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME, space_efficiency=None,
                                       pool=self.volume_response.pool, is_virt_snap_func=False)

    def test_create_snapshot_get_volume_not_found(self):
        self.client_mock.create_volume = Mock()
        self.client_mock.get_volume.side_effect = NotFound("404")

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.create_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME, space_efficiency=None,
                                       pool=self.volume_response.pool, is_virt_snap_func=False)

    def test_create_snapshot_create_flashcopy_volume_not_found(self):
        self._prepare_mocks_for_create_snapshot()
        self.client_mock.create_flashcopy.side_effect = ClientException("500", message="00000013")

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.create_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME, space_efficiency=None,
                                       pool=self.volume_response.pool, is_virt_snap_func=False)

    def test_create_snapshot_already_exist(self):
        self._prepare_mocks_for_create_snapshot()
        self.client_mock.create_flashcopy.side_effect = ClientException("500", message="000000AE")

        with self.assertRaises(array_errors.SnapshotAlreadyExists):
            self.array.create_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME, space_efficiency=None,
                                       pool=self.volume_response.pool, is_virt_snap_func=False)

    def test_create_snapshot_failed_with_incorrect_id(self):
        self.client_mock.get_volume.side_effect = InternalServerError("500", message="BE7A0005")
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.array.create_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME, space_efficiency=None,
                                       pool=None,
                                       is_virt_snap_func=False)

    def test_create_snapshot_success(self):
        self._prepare_mocks_for_create_snapshot()
        snapshot_response = self.snapshot_response
        snapshot = self.array.create_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME,
                                              space_efficiency=None, pool=None,
                                              is_virt_snap_func=False)

        self.assertEqual(snapshot.name, snapshot_response.name)
        self.assertEqual(snapshot.id, self.array._generate_volume_scsi_identifier(snapshot_response.id))
        self.client_mock.create_volume.assert_called_once_with(name=common_settings.SNAPSHOT_NAME,
                                                               capacity_in_bytes=1073741824,
                                                               pool_id=common_settings.DUMMY_POOL1,
                                                               thin_provisioning=SPACE_EFFICIENCY_NONE)

    def test_create_snapshot_with_empty_cache(self):
        self._prepare_mocks_for_create_snapshot()

        self.array.create_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME, space_efficiency=None,
                                   pool=None,
                                   is_virt_snap_func=False)

        self.array.volume_cache.add.assert_called_once_with(self.snapshot_response.name, self.snapshot_response.id)

    def test_create_snapshot_with_different_pool_success(self):
        self._prepare_mocks_for_create_snapshot()

        self.array.create_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME, space_efficiency=None,
                                   pool=common_settings.DUMMY_POOL2,
                                   is_virt_snap_func=False)

        self.client_mock.create_volume.assert_called_once_with(name=common_settings.SNAPSHOT_NAME,
                                                               capacity_in_bytes=1073741824,
                                                               pool_id=common_settings.DUMMY_POOL2,
                                                               thin_provisioning=SPACE_EFFICIENCY_NONE)

    def _test_create_snapshot_with_space_efficiency_success(self, source_volume_space_efficiency,
                                                            space_efficiency_called, space_efficiency_parameter=None):
        self._prepare_mocks_for_create_snapshot(thin_provisioning=source_volume_space_efficiency)

        if space_efficiency_parameter is None:
            self.array.create_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME, space_efficiency=None,
                                       pool=None,
                                       is_virt_snap_func=False)
        else:
            self.array.create_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME,
                                       space_efficiency=space_efficiency_parameter,
                                       pool=None, is_virt_snap_func=False)

        self.client_mock.create_volume.assert_called_with(name=common_settings.SNAPSHOT_NAME,
                                                          capacity_in_bytes=1073741824,
                                                          pool_id=common_settings.DUMMY_POOL1,
                                                          thin_provisioning=space_efficiency_called)

    def test_create_snapshot_with_specified_source_volume_space_efficiency_success(self):
        self._test_create_snapshot_with_space_efficiency_success(source_volume_space_efficiency=SPACE_EFFICIENCY_NONE,
                                                                 space_efficiency_called=SPACE_EFFICIENCY_NONE)

    def test_create_snapshot_with_different_request_parameter_space_efficiency_success(self):
        self._test_create_snapshot_with_space_efficiency_success(SPACE_EFFICIENCY_NONE,
                                                                 ds8k_settings.DS8K_SPACE_EFFICIENCY_THIN,
                                                                 SPACE_EFFICIENCY_THIN)

    def test_create_snapshot_with_different_request_parameter_empty_space_efficiency_success(self):
        self._test_create_snapshot_with_space_efficiency_success(
            source_volume_space_efficiency=ds8k_settings.DS8K_SPACE_EFFICIENCY_THIN,
            space_efficiency_called=ds8k_settings.DS8K_SPACE_EFFICIENCY_THIN,
            space_efficiency_parameter="")

    def test_create_snapshot_not_valid(self):
        self._prepare_mocks_for_create_snapshot()
        flashcopy_response = self._get_flashcopy_response(
            ds8k_settings.DUMMY_VOLUME_ID1,
            ds8k_settings.DUMMY_VOLUME_ID2,
            state=ds8k_settings.INVALID_STATE
        )
        self.client_mock.get_flashcopies.return_value = flashcopy_response
        with self.assertRaises(ValueError) as ar_context:
            self.array.create_snapshot(common_settings.VOLUME_UID, common_settings.SNAPSHOT_NAME, space_efficiency=None,
                                       pool=self.volume_response.pool, is_virt_snap_func=False)
        self.assertIn(ds8k_settings.INVALID_STATE, str(ar_context.exception))

    def _prepare_mocks_for_snapshot(self):
        flashcopy_as_target = self.flashcopy_response
        snapshot = self.snapshot_response
        flashcopy_as_target.backgroundcopy = ds8k_settings.DISABLED_BACKGROUND_COPY
        self.client_mock.get_volume.return_value = snapshot
        self.client_mock.get_volumes_by_pool.return_value = [snapshot]
        self.client_mock.get_flashcopies_by_volume.return_value = [flashcopy_as_target]
        return snapshot

    def test_delete_snapshot(self):
        self._prepare_mocks_for_snapshot()
        self.array.delete_snapshot(common_settings.SNAPSHOT_VOLUME_UID, common_settings.INTERNAL_SNAPSHOT_ID)
        self.client_mock.delete_volume.assert_called_once()
        self.client_mock.delete_flashcopy.assert_called_once_with(self.flashcopy_response.id)

    def test_delete_snapshot_with_remove_from_cache(self):
        self._prepare_mocks_for_snapshot()
        self.array.delete_snapshot(self.snapshot_response.id, common_settings.INTERNAL_SNAPSHOT_ID)

        self.array.volume_cache.remove.assert_called_once_with(self.snapshot_response.name)

    def test_delete_snapshot_flashcopy_fail_with_client_exception(self):
        self._prepare_mocks_for_snapshot()
        self.client_mock.delete_flashcopy.side_effect = ClientException("500")
        self.client_mock.get_volume.return_value = self.snapshot_response
        with self.assertRaises(ClientException):
            self.array.delete_snapshot(common_settings.SNAPSHOT_VOLUME_UID, common_settings.INTERNAL_SNAPSHOT_ID)

    def test_delete_snapshot_fail_with_client_exception(self):
        self._prepare_mocks_for_snapshot()
        self.client_mock.delete_volume.side_effect = ClientException("500")
        with self.assertRaises(array_errors.VolumeDeletionError):
            self.array.delete_snapshot(common_settings.SNAPSHOT_VOLUME_UID, common_settings.INTERNAL_SNAPSHOT_ID)

    def test_delete_snapshot_fail_with_not_found(self):
        self.client_mock.get_volume.side_effect = NotFound("404")
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.delete_snapshot(common_settings.SNAPSHOT_VOLUME_UID, common_settings.INTERNAL_SNAPSHOT_ID)
        self.client_mock.get_volume.side_effect = [self.snapshot_response]
        self.client_mock.get_flashcopies_by_volume.return_value = [self.flashcopy_response]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.delete_snapshot(common_settings.SNAPSHOT_VOLUME_UID, common_settings.INTERNAL_SNAPSHOT_ID)

    def test_delete_snapshot_failed_with_illegal_object_id(self):
        self.client_mock.get_volume.side_effect = InternalServerError("500", message="BE7A0005")
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.array.delete_snapshot(common_settings.SNAPSHOT_VOLUME_UID, common_settings.INTERNAL_SNAPSHOT_ID)

    def _prepare_mocks_for_copy_to_existing_volume(self):
        volume = self.volume_response
        self.client_mock.get_volume.return_value = volume
        self.client_mock.get_flashcopies_by_volume.side_effect = \
            [[], [self.flashcopy_response], [self.flashcopy_response]]
        self.client_mock.get_flashcopies.return_value = self.flashcopy_response
        self.client_mock.create_flashcopy.return_value = self.flashcopy_response
        return volume

    def test_copy_to_existing_volume_success(self):
        volume = self._prepare_mocks_for_copy_to_existing_volume()
        self.array.copy_to_existing_volume(volume.id, ds8k_settings.DUMMY_VOLUME_ID2, 3, 2)
        self.client_mock.extend_volume.assert_called_once_with(volume_id=volume.id,
                                                               new_size_in_bytes=3)
        self.client_mock.create_flashcopy.assert_called_once_with(
            source_volume_id=ds8k_settings.DUMMY_VOLUME_ID2,
            target_volume_id=volume.id,
            options=[FLASHCOPY_PERSISTENT_OPTION,
                     FLASHCOPY_PERMIT_SPACE_EFFICIENT_TARGET_OPTION
                     ])

    def _test_copy_to_existing_volume_raise_errors(self, client_method, client_error, expected_error):
        self._prepare_mocks_for_copy_to_existing_volume()
        client_method.side_effect = client_error
        with self.assertRaises(expected_error):
            self.array.copy_to_existing_volume(common_settings.VOLUME_UID, common_settings.SOURCE_VOLUME_ID, 3, 2)

    def test_copy_to_existing_volume_raise_not_found(self):
        self._test_copy_to_existing_volume_raise_errors(client_method=self.client_mock.extend_volume,
                                                        client_error=NotFound("404"),
                                                        expected_error=array_errors.ObjectNotFoundError)

    def test_copy_to_existing_volume_raise_illegal_object_id(self):
        self._test_copy_to_existing_volume_raise_errors(client_method=self.client_mock.get_volume,
                                                        client_error=InternalServerError("500", "BE7A0005"),
                                                        expected_error=array_errors.InvalidArgumentError)

    def test_get_object_by_id_snapshot(self):
        snapshot = self._prepare_mocks_for_snapshot()
        return_value = self.array.get_object_by_id(snapshot.id, common_settings.SNAPSHOT_OBJECT_TYPE)
        self.assertEqual(type(return_value), Snapshot)
        self.assertEqual(return_value.id, self.array._generate_volume_scsi_identifier(snapshot.id))

    def test_get_object_by_id_volume_with_source(self):
        self.flashcopy_response.targetvolume = ds8k_settings.DUMMY_VOLUME_ID1
        volume = self._prepare_mocks_for_volume()
        return_value = self.array.get_object_by_id(volume.id, common_settings.VOLUME_OBJECT_TYPE)
        self.assertEqual(type(return_value), Volume)
        self.assertEqual(return_value.id, self.array._generate_volume_scsi_identifier(volume.id))
        self.assertEqual(return_value.source_id, self.array._generate_volume_scsi_identifier(volume.id))

    def test_get_object_by_id_return_none(self):
        self.client_mock.get_volume.side_effect = NotFound("404")
        return_value = self.array.get_object_by_id("", common_settings.VOLUME_OBJECT_TYPE)
        self.assertEqual(return_value, None)

    def test_get_object_by_id_errors(self):
        self._prepare_mocks_for_snapshot()
        self.client_mock.get_flashcopies_by_volume.return_value = []
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.array.get_object_by_id("", common_settings.SNAPSHOT_OBJECT_TYPE)
        self.flashcopy_response.backgroundcopy = ds8k_settings.ENABLED_BACKGROUND_COPY
        self.client_mock.get_flashcopies_by_volume.return_value = [self.flashcopy_response]
        with self.assertRaises(array_errors.ExpectedSnapshotButFoundVolumeError):
            self.array.get_object_by_id("", common_settings.SNAPSHOT_OBJECT_TYPE)

    def test_get_object_by_id_get_volume_raise_error(self):
        self.client_mock.get_volume.side_effect = ClientException("500", array_settings.DUMMY_ERROR_MESSAGE)
        with self.assertRaises(ClientException):
            self.array.get_object_by_id("", common_settings.VOLUME_OBJECT_TYPE)

    def test_expand_volume_success(self):
        volume = self._prepare_mocks_for_volume()
        self.array.expand_volume(volume_id=volume.id, required_bytes=10)
        self.client_mock.extend_volume.assert_called_once_with(volume_id=volume.id, new_size_in_bytes=10)

    def test_expand_volume_raise_in_use(self):
        volume = self._prepare_mocks_for_volume()
        self.client_mock.get_flashcopies.return_value.out_of_sync_tracks = ds8k_settings.DUMMY_OUT_OF_SYNC_TRACKS
        with self.assertRaises(array_errors.ObjectIsStillInUseError):
            self.array.expand_volume(volume_id=volume.id, required_bytes=10)

    def test_expand_volume_raise_illegal(self):
        volume = self._prepare_mocks_for_volume()
        self.client_mock.get_volume.side_effect = [InternalServerError("500", "BE7A0005")]
        with self.assertRaises(array_errors.InvalidArgumentError):
            self.array.expand_volume(volume_id=volume.id, required_bytes=10)

    def test_expand_volume_get_volume_not_found_error(self):
        self.client_mock.get_volume.side_effect = [NotFound("404", message="BE7A0001")]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.expand_volume(volume_id=common_settings.VOLUME_UID, required_bytes=10)

    def test_expand_volume_extend_volume_not_found_error(self):
        self.client_mock.get_volume.side_effect = [self.volume_response, self.volume_response]
        self.client_mock.extend_volume.side_effect = [NotFound("404", message="BE7A0001")]
        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.array.expand_volume(volume_id=common_settings.VOLUME_UID, required_bytes=10)

    def test_expand_volume_extend_not_enough_space_error(self):
        self.client_mock.extend_volume.side_effect = [ClientException("500", message="BE531465")]
        with self.assertRaises(array_errors.NotEnoughSpaceInPool):
            self.array.expand_volume(volume_id=common_settings.VOLUME_UID, required_bytes=10)

    def test_expand_volume_extend_raise_error(self):
        self.client_mock.extend_volume.side_effect = [
            ClientException("500", message=array_settings.DUMMY_ERROR_MESSAGE)]
        with self.assertRaises(ClientException):
            self.array.expand_volume(volume_id=common_settings.VOLUME_UID, required_bytes=10)
