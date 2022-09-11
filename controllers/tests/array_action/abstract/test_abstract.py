import unittest
from unittest.mock import Mock

import controllers.array_action.errors as array_errors
import controllers.tests.array_action.test_settings as array_settings
import controllers.tests.common.test_settings as common_settings
from controllers.array_action.array_action_types import Host
from controllers.array_action.array_mediator_abstract import ArrayMediatorAbstract
from controllers.common.node_info import Initiators
from controllers.tests import utils


def _get_dummy_class_name():
    return 'dummy_%s' % ArrayMediatorAbstract.__name__


def _get_dummy_mediator_abstract_class(class_dict):
    return type(_get_dummy_class_name(), (ArrayMediatorAbstract,), class_dict)


def _get_implemented_class_dict():
    mediator_abstract_class = ArrayMediatorAbstract
    dummy_abstract_class_dict = mediator_abstract_class.__dict__.copy()
    for abstract_method in mediator_abstract_class.__abstractmethods__:
        dummy_abstract_class_dict[abstract_method] = Mock()
    return dummy_abstract_class_dict


def _get_array_mediator_abstract_class():
    dummy_abstract_class_dict = _get_implemented_class_dict()
    dummy_mediator_abstract_class = _get_dummy_mediator_abstract_class(dummy_abstract_class_dict)
    return dummy_mediator_abstract_class(common_settings.SECRET_USERNAME_VALUE,
                                         common_settings.SECRET_PASSWORD_VALUE,
                                         [common_settings.SECRET_MANAGEMENT_ADDRESS_VALUE])


class BaseMediatorAbstractSetUp(unittest.TestCase):

    def setUp(self):
        self.mediator = _get_array_mediator_abstract_class()

        self.mediator.get_volume_mappings.return_value = {}

        self.fc_ports = [array_settings.DUMMY_FC_WWN1, array_settings.DUMMY_FC_WWN2]
        self.lun_id = array_settings.DUMMY_LUN_ID
        self.connectivity_type = array_settings.FC_CONNECTIVITY_TYPE

        self.mediator.get_host_by_host_identifiers.return_value = (common_settings.HOST_NAME,
                                                                   self.connectivity_type)
        self.mediator.get_array_fc_wwns.return_value = self.fc_ports
        self.mediator.map_volume.return_value = self.lun_id
        self.hostname = common_settings.HOST_NAME
        self.iqn = array_settings.DUMMY_NODE1_IQN
        self.iscsi_targets_by_iqn = {'iqn1': ['1.1.1.1', '2.2.2.2'], 'iqn2': ['[::1]']}
        self.mediator.get_iscsi_targets_by_iqn.return_value = self.iscsi_targets_by_iqn
        self.mediator.max_lun_retries = 10


class TestMapVolumeByInitiators(BaseMediatorAbstractSetUp):

    def setUp(self):
        super().setUp()
        self.mediator.get_host_by_name = Mock()

    def test_map_volume_by_initiators(self):
        response = self.mediator.map_volume_by_initiators(vol_id='', initiators=Initiators())
        self.assertTupleEqual((self.lun_id, self.connectivity_type, self.fc_ports), response)

    def test_publish_volume_get_host_by_host_identifiers_exception(self):
        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.MultipleHostsFoundError('', '')]

        with self.assertRaises(array_errors.MultipleHostsFoundError):
            self.mediator.map_volume_by_initiators(vol_id='', initiators=Initiators())

        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.HostNotFoundError('')]

        with self.assertRaises(array_errors.HostNotFoundError):
            self.mediator.map_volume_by_initiators(vol_id='', initiators=Initiators())

    def test_publish_volume_get_iscsi_targets_by_iqn_excpetions(self):
        self.mediator.get_host_by_host_identifiers.return_value = (common_settings.HOST_NAME,
                                                                   array_settings.ISCSI_CONNECTIVITY_TYPE)
        self.mediator.get_iscsi_targets_by_iqn.side_effect = [array_errors.NoIscsiTargetsFoundError('')]

        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.mediator.map_volume_by_initiators(vol_id='', initiators=Initiators())

    def test_publish_volume_get_volume_mappings_more_then_one_mapping(self):
        self.mediator.get_volume_mappings.return_value = {array_settings.DUMMY_HOST_ID1: array_settings.DUMMY_HOST_ID1,
                                                          array_settings.DUMMY_HOST_ID2: array_settings.DUMMY_HOST_ID2}

        with self.assertRaises(array_errors.VolumeAlreadyMappedToDifferentHostsError):
            self.mediator.map_volume_by_initiators(vol_id='', initiators=Initiators())

    def test_publish_volume_get_volume_mappings_one_mapping(self):
        self.mediator.get_volume_mappings.return_value = {array_settings.DUMMY_HOST_ID1: array_settings.DUMMY_HOST_ID1}
        self.mediator.get_host_by_name.return_value = Host(name=self.hostname, connectivity_types=['iscsi'],
                                                           iscsi_iqns=[self.iqn])

        with self.assertRaises(array_errors.VolumeAlreadyMappedToDifferentHostsError):
            self.mediator.map_volume_by_initiators(vol_id='', initiators=Initiators())

    def test_publish_volume_get_volume_mappings_one_map_for_existing_host(self):
        self.mediator.get_volume_mappings = Mock()
        self.mediator.get_volume_mappings.return_value = {self.hostname: self.lun_id}
        self.mediator.get_host_by_name.return_value = Host(name=self.hostname, connectivity_types=['iscsi'],
                                                           iscsi_iqns=[self.iqn])

        response = self.mediator.map_volume_by_initiators(vol_id='', initiators=Initiators(iscsi_iqns=[self.iqn]))
        self.assertEqual((self.lun_id, 'iscsi', self.iscsi_targets_by_iqn), response)

    def test_publish_volume_map_volume_excpetions(self):
        self.mediator.map_volume.side_effect = [array_errors.PermissionDeniedError('msg')]

        with self.assertRaises(array_errors.PermissionDeniedError):
            self.mediator.map_volume_by_initiators(vol_id='', initiators=Initiators())

        self.mediator.map_volume.side_effect = [array_errors.ObjectNotFoundError('volume')]

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.mediator.map_volume_by_initiators(vol_id='', initiators=Initiators())

        self.mediator.map_volume.side_effect = [array_errors.HostNotFoundError('host')]

        with self.assertRaises(array_errors.HostNotFoundError):
            self.mediator.map_volume_by_initiators(vol_id='', initiators=Initiators())

        self.mediator.map_volume.side_effect = [array_errors.MappingError('', '', '')]

        with self.assertRaises(array_errors.MappingError):
            self.mediator.map_volume_by_initiators(vol_id='', initiators=Initiators())

    def test_publish_volume_map_volume_lun_already_in_use(self):
        self.mediator.map_volume.side_effect = [array_errors.LunAlreadyInUseError('', ''), 2]

        response = self.mediator.map_volume_by_initiators(vol_id='', initiators=Initiators())

        self.assertEqual(response, (2, 'fc', ['wwn1', 'wwn2']))

        self.mediator.map_volume.side_effect = [
            array_errors.LunAlreadyInUseError('', ''), 2]
        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ['fc']
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = ['500143802426baf4']

        response = self.mediator.map_volume_by_initiators(vol_id='', initiators=Initiators())

        self.assertEqual(response, (2, 'fc', ['500143802426baf4']))

        self.mediator.map_volume.side_effect = [array_errors.LunAlreadyInUseError('', ''),
                                                array_errors.LunAlreadyInUseError('', ''), 2]

        self.mediator.map_volume_by_initiators(vol_id='', initiators=Initiators())

        self.assertEqual(response, (2, 'fc', ['500143802426baf4']))

        max_lun_retries = self.mediator.max_lun_retries + 1
        error_all_retries = [array_errors.LunAlreadyInUseError('', '')] * max_lun_retries
        self.mediator.map_volume.side_effect = error_all_retries

        with self.assertRaises(array_errors.LunAlreadyInUseError):
            self.mediator.map_volume_by_initiators(vol_id='', initiators=Initiators())

    def test_publish_volume_with_connectivity_type_fc(self):
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ['iscsi', 'fc']
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = ['500143802426baf4']

        response = self.mediator.map_volume_by_initiators(vol_id='', initiators=Initiators())

        self.assertEqual(response, ('5', 'fc', ['500143802426baf4']))

    def test_publish_volume_with_connectivity_type_nvme(self):
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ['nvmeofc']

        response = self.mediator.map_volume_by_initiators(vol_id='', initiators=Initiators())

        self.assertEqual(response, ('5', 'nvmeofc', []))

    def test_publish_volume_with_connectivity_unsupported_type(self):
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ['fake']
        with self.assertRaises(array_errors.UnsupportedConnectivityTypeError):
            self.mediator.map_volume_by_initiators(vol_id='', initiators=Initiators())

    def test_publish_volume_with_connectivity_type_iscsi(self):
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, ['iscsi']

        response = self.mediator.map_volume_by_initiators(vol_id='', initiators=Initiators())

        self.assertEqual(response, ('5', 'iscsi', {'iqn1': ['1.1.1.1', '2.2.2.2'], 'iqn2': ['[::1]']}))


class TestUnmapVolumeByInitiators(BaseMediatorAbstractSetUp):

    def setUp(self):
        super().setUp()
        self.mediator.unmap_volume = Mock()

    def test_unpublish_volume_get_host_by_host_identifiers(self):
        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.return_value = (common_settings.HOST_NAME,
                                                                   array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE)

        self.mediator.unmap_volume_by_initiators(vol_id='', initiators=Initiators())

    def test_unpublish_volume_get_host_by_host_identifiers_multiple_hosts_found_error(self):
        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.MultipleHostsFoundError('', '')]

        with self.assertRaises(array_errors.MultipleHostsFoundError):
            self.mediator.unmap_volume_by_initiators(vol_id='', initiators=Initiators())

    def _test_unpublish_volume_unmap_volume_with_error(self, array_error):
        self.mediator.unmap_volume.side_effect = [array_error]

        with self.assertRaises(array_error.__class__):
            self.mediator.unmap_volume_by_initiators(vol_id='', initiators=Initiators())

    def test_unpublish_volume_unmap_volume_object_not_found_error(self):
        self._test_unpublish_volume_unmap_volume_with_error(array_errors.ObjectNotFoundError('volume'))

    def test_unpublish_volume_unmap_volume_volume_already_unmapped_error(self):
        self._test_unpublish_volume_unmap_volume_with_error(array_errors.VolumeAlreadyUnmappedError(''))

    def test_unpublish_volume_unmap_volume_permission_denied_error(self):
        self._test_unpublish_volume_unmap_volume_with_error(array_errors.PermissionDeniedError('msg'))

    def test_unpublish_volume_unmap_volume_host_not_found_error(self):
        self._test_unpublish_volume_unmap_volume_with_error(array_errors.HostNotFoundError('host'))

    def test_unpublish_volume_unmap_volume_unmapping_error(self):
        self._test_unpublish_volume_unmap_volume_with_error(array_errors.UnmappingError('', '', ''))


class TestCopyToExistingVolumeFromSource(BaseMediatorAbstractSetUp):

    def test_clone_volume_success(self):
        volume_id = common_settings.SOURCE_VOLUME_ID
        volume_capacity_bytes = 10
        response_volume = utils.get_mock_mediator_response_volume()
        self.mediator.get_object_by_id.return_value = response_volume
        self.mediator.copy_to_existing_volume_from_source(response_volume, volume_id,
                                                          common_settings.VOLUME_OBJECT_TYPE, 2)
        self.mediator.copy_to_existing_volume.assert_called_once_with(common_settings.VOLUME_UID,
                                                                      volume_id,
                                                                      volume_capacity_bytes,
                                                                      2)

    def test_create_volume_from_snapshot_success(self):
        snapshot_id = common_settings.SNAPSHOT_VOLUME_UID
        snapshot_capacity_bytes = 10
        response_volume = utils.get_mock_mediator_response_volume()
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_snapshot()
        self.mediator.copy_to_existing_volume_from_source(response_volume, snapshot_id,
                                                          common_settings.SNAPSHOT_OBJECT_TYPE, 2)
        self.mediator.copy_to_existing_volume.assert_called_once_with(common_settings.VOLUME_UID, snapshot_id,
                                                                      snapshot_capacity_bytes,
                                                                      2)

    def test_create_volume_from_snapshot_error(self):
        snapshot_id = common_settings.SNAPSHOT_VOLUME_UID
        response_volume = utils.get_mock_mediator_response_volume()
        self.mediator.get_object_by_id.return_value = None

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.mediator.copy_to_existing_volume_from_source(response_volume, snapshot_id,
                                                              common_settings.SNAPSHOT_OBJECT_TYPE, 2)

    def _test_create_volume_from_snapshot_error(self, copy_exception, get_exception=None):
        target_volume_id = common_settings.VOLUME_UID
        exception = copy_exception
        if not copy_exception:
            exception = get_exception
            self.mediator.get_object_by_id.side_effect = [get_exception]
        else:
            self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_snapshot()
            self.mediator.copy_to_existing_volume.side_effect = [copy_exception]

        self.mediator.delete_volume = Mock()

        response_volume = utils.get_mock_mediator_response_volume()
        with self.assertRaises(exception.__class__):
            self.mediator.copy_to_existing_volume_from_source(response_volume, target_volume_id,
                                                              common_settings.VOLUME_OBJECT_TYPE, 2)
        self.mediator.delete_volume.assert_called_with(target_volume_id)

    def test_create_volume_from_source_illegal_object_id(self):
        array_exception = array_errors.InvalidArgumentError('')
        self._test_create_volume_from_snapshot_error(array_exception)

    def test_create_volume_from_source_permission_denied(self):
        array_exception = array_errors.PermissionDeniedError('')
        self._test_create_volume_from_snapshot_error(array_exception)

    def test_create_volume_from_source_pool_missing(self):
        array_exception = array_errors.PoolParameterIsMissing('')
        self._test_create_volume_from_snapshot_error(array_exception)

    def test_create_volume_from_source_general_error(self):
        array_exception = Exception('')
        self._test_create_volume_from_snapshot_error(array_exception)

    def test_create_volume_from_source_get_object_general_error(self):
        array_exception = Exception('')
        self._test_create_volume_from_snapshot_error(None, get_exception=array_exception)

    def test_create_volume_from_source_get_object_error(self):
        array_exception = array_errors.ExpectedSnapshotButFoundVolumeError('', '')
        self._test_create_volume_from_snapshot_error(None, get_exception=array_exception)

    def test_create_volume_from_source_get_object_none(self):
        array_exception = array_errors.ObjectNotFoundError('')
        self._test_create_volume_from_snapshot_error(None, get_exception=array_exception)
