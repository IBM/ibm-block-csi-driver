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
    return 'dummy_{}'.format(ArrayMediatorAbstract.__name__)


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
    return dummy_mediator_abstract_class(  # pylint: disable=abstract-class-instantiated
        common_settings.SECRET_USERNAME_VALUE,
        common_settings.SECRET_PASSWORD_VALUE,
        [common_settings.SECRET_MANAGEMENT_ADDRESS_VALUE])


class BaseMediatorAbstractSetUp(unittest.TestCase):

    def setUp(self):
        self.mediator = _get_array_mediator_abstract_class()

        self.mediator.get_volume_mappings.return_value = {}

        self.fc_ports = [array_settings.DUMMY_FC_WWN1, array_settings.DUMMY_FC_WWN2]
        self.lun_id = array_settings.DUMMY_LUN_ID
        self.connectivity_type = array_settings.FC_CONNECTIVITY_TYPE

        self.mediator.get_host_by_host_identifiers.return_value = (common_settings.HOST_NAME, self.connectivity_type)
        self.mediator.get_array_fc_wwns.return_value = self.fc_ports
        self.mediator.map_volume.return_value = self.lun_id
        self.hostname = common_settings.HOST_NAME
        self.iqn = array_settings.DUMMY_NODE1_IQN
        self.iscsi_targets_by_iqn = {
            array_settings.DUMMY_NODE1_IQN: [array_settings.DUMMY_IP_ADDRESS1, array_settings.DUMMY_IP_ADDRESS2],
            array_settings.DUMMY_NODE2_IQN: [array_settings.DUMMY_IP_ADDRESS3]
        }
        self.mediator.get_iscsi_targets_by_iqn.return_value = self.iscsi_targets_by_iqn
        self.mediator.max_lun_retries = 10
        self.initiators = Initiators()


class TestMapVolumeByInitiators(BaseMediatorAbstractSetUp):

    def setUp(self):
        super().setUp()
        self.mediator.get_host_by_name = Mock()

    def test_map_volume_by_initiators(self):
        response = self.mediator.map_volume_by_initiators('', self.initiators)
        self.assertTupleEqual((self.lun_id, self.connectivity_type, self.fc_ports), response)

    def test_map_volume_by_initiators_get_host_by_host_identifiers_exception(self):
        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.MultipleHostsFoundError('', '')]

        with self.assertRaises(array_errors.MultipleHostsFoundError):
            self.mediator.map_volume_by_initiators('', self.initiators)

        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.HostNotFoundError('')]

        with self.assertRaises(array_errors.HostNotFoundError):
            self.mediator.map_volume_by_initiators('', self.initiators)

    def test_map_volume_by_initiators_get_iscsi_targets_by_iqn_excpetions(self):
        self.mediator.get_host_by_host_identifiers.return_value = (common_settings.HOST_NAME,
                                                                   array_settings.ISCSI_CONNECTIVITY_TYPE)
        self.mediator.get_iscsi_targets_by_iqn.side_effect = [array_errors.NoIscsiTargetsFoundError('')]

        with self.assertRaises(array_errors.NoIscsiTargetsFoundError):
            self.mediator.map_volume_by_initiators('', self.initiators)

    def test_map_volume_by_initiators_get_volume_mappings_more_then_one_mapping(self):
        self.mediator.get_volume_mappings.return_value = {array_settings.DUMMY_HOST_ID1: array_settings.DUMMY_HOST_ID1,
                                                          array_settings.DUMMY_HOST_ID2: array_settings.DUMMY_HOST_ID2}

        with self.assertRaises(array_errors.VolumeAlreadyMappedToDifferentHostsError):
            self.mediator.map_volume_by_initiators('', self.initiators)

    def test_map_volume_by_initiators_get_volume_mappings_one_mapping(self):
        self.mediator.get_volume_mappings.return_value = {array_settings.DUMMY_HOST_ID1: array_settings.DUMMY_HOST_ID1}
        self.mediator.get_host_by_name.return_value = Host(name=self.hostname,
                                                           connectivity_types=[array_settings.ISCSI_CONNECTIVITY_TYPE],
                                                           iscsi_iqns=[self.iqn])

        with self.assertRaises(array_errors.VolumeAlreadyMappedToDifferentHostsError):
            self.mediator.map_volume_by_initiators('', self.initiators)

    def test_map_volume_by_initiators_get_volume_mappings_one_map_for_existing_host(self):
        self.mediator.get_volume_mappings = Mock()
        self.mediator.get_volume_mappings.return_value = {self.hostname: self.lun_id}
        self.mediator.get_host_by_name.return_value = Host(name=self.hostname,
                                                           connectivity_types=[array_settings.ISCSI_CONNECTIVITY_TYPE],
                                                           iscsi_iqns=[self.iqn])

        self.initiators.iscsi_iqns = [self.iqn]
        response = self.mediator.map_volume_by_initiators('', self.initiators)
        self.assertEqual((self.lun_id, array_settings.ISCSI_CONNECTIVITY_TYPE, self.iscsi_targets_by_iqn), response)

    def test_map_volume_by_initiators_map_volume_excpetions(self):
        self.mediator.map_volume.side_effect = [array_errors.PermissionDeniedError('')]

        with self.assertRaises(array_errors.PermissionDeniedError):
            self.mediator.map_volume_by_initiators('', self.initiators)

        self.mediator.map_volume.side_effect = [array_errors.ObjectNotFoundError('')]

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.mediator.map_volume_by_initiators('', self.initiators)

        self.mediator.map_volume.side_effect = [array_errors.HostNotFoundError('')]

        with self.assertRaises(array_errors.HostNotFoundError):
            self.mediator.map_volume_by_initiators('', self.initiators)

        self.mediator.map_volume.side_effect = [array_errors.MappingError('', '', '')]

        with self.assertRaises(array_errors.MappingError):
            self.mediator.map_volume_by_initiators('', self.initiators)

    def test_map_volume_by_initiators_map_volume_lun_already_in_use(self):
        self.mediator.map_volume.side_effect = [array_errors.LunAlreadyInUseError('', ''),
                                                array_settings.DUMMY_LUN_ID_INT]

        response = self.mediator.map_volume_by_initiators('', self.initiators)

        self.assertEqual(response, (array_settings.DUMMY_LUN_ID_INT, array_settings.FC_CONNECTIVITY_TYPE,
                                    [array_settings.DUMMY_FC_WWN1, array_settings.DUMMY_FC_WWN2]))

        self.mediator.map_volume.side_effect = [array_errors.LunAlreadyInUseError('', ''),
                                                array_settings.DUMMY_LUN_ID_INT]
        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, [array_settings.FC_CONNECTIVITY_TYPE]
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = [array_settings.DUMMY_FC_WWN3]

        response = self.mediator.map_volume_by_initiators('', self.initiators)

        self.assertEqual(response, (
            array_settings.DUMMY_LUN_ID_INT, array_settings.FC_CONNECTIVITY_TYPE, [array_settings.DUMMY_FC_WWN3]))

        self.mediator.map_volume.side_effect = [array_errors.LunAlreadyInUseError('', ''),
                                                array_errors.LunAlreadyInUseError('', ''),
                                                array_settings.DUMMY_LUN_ID_INT]

        self.mediator.map_volume_by_initiators('', self.initiators)

        self.assertEqual(response, (array_settings.DUMMY_LUN_ID_INT, array_settings.FC_CONNECTIVITY_TYPE,
                                    [array_settings.DUMMY_FC_WWN3]))

        max_lun_retries = self.mediator.max_lun_retries + 1
        error_all_retries = [array_errors.LunAlreadyInUseError('', '')] * max_lun_retries
        self.mediator.map_volume.side_effect = error_all_retries

        with self.assertRaises(array_errors.LunAlreadyInUseError):
            self.mediator.map_volume_by_initiators('', self.initiators)

    def test_map_volume_by_initiators_with_connectivity_type_fc(self):
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, [
            array_settings.ISCSI_CONNECTIVITY_TYPE, array_settings.FC_CONNECTIVITY_TYPE]
        self.mediator.get_array_fc_wwns = Mock()
        self.mediator.get_array_fc_wwns.return_value = [array_settings.DUMMY_FC_WWN3]

        response = self.mediator.map_volume_by_initiators('', self.initiators)

        self.assertEqual(response, (
            array_settings.DUMMY_LUN_ID, array_settings.FC_CONNECTIVITY_TYPE, [array_settings.DUMMY_FC_WWN3]))

    def test_map_volume_by_initiators_with_connectivity_type_nvme(self):
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, [
            array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE]

        response = self.mediator.map_volume_by_initiators('', self.initiators)

        self.assertEqual(response, (array_settings.DUMMY_LUN_ID, array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE, []))

    def test_map_volume_by_initiators_with_connectivity_unsupported_type(self):
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, [
            array_settings.DUMMY_CONNECTIVITY_TYPE]
        with self.assertRaises(array_errors.UnsupportedConnectivityTypeError):
            self.mediator.map_volume_by_initiators('', self.initiators)

    def test_map_volume_by_initiators_with_connectivity_type_iscsi(self):
        self.mediator.get_host_by_host_identifiers.return_value = self.hostname, [
            array_settings.ISCSI_CONNECTIVITY_TYPE]

        response = self.mediator.map_volume_by_initiators('', self.initiators)

        self.assertEqual(response, (array_settings.DUMMY_LUN_ID, array_settings.ISCSI_CONNECTIVITY_TYPE, {
            array_settings.DUMMY_NODE1_IQN: [array_settings.DUMMY_IP_ADDRESS1, array_settings.DUMMY_IP_ADDRESS2],
            array_settings.DUMMY_NODE2_IQN: [array_settings.DUMMY_IP_ADDRESS3]}))


class TestUnmapVolumeByInitiators(BaseMediatorAbstractSetUp):

    def setUp(self):
        super().setUp()
        self.mediator.unmap_volume = Mock()

    def test_unmap_volume_by_initiators_get_host_by_host_identifiers(self):
        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.return_value = (common_settings.HOST_NAME,
                                                                   array_settings.NVME_OVER_FC_CONNECTIVITY_TYPE)

        self.mediator.unmap_volume_by_initiators('', self.initiators)

    def test_unmap_volume_by_initiators_get_host_by_host_identifiers_multiple_hosts_found_error(self):
        self.mediator.get_host_by_host_identifiers = Mock()
        self.mediator.get_host_by_host_identifiers.side_effect = [array_errors.MultipleHostsFoundError('', '')]

        with self.assertRaises(array_errors.MultipleHostsFoundError):
            self.mediator.unmap_volume_by_initiators('', self.initiators)

    def _test_unpublish_volume_unmap_volume_with_error(self, array_error):
        self.mediator.unmap_volume.side_effect = [array_error]

        with self.assertRaises(array_error.__class__):
            self.mediator.unmap_volume_by_initiators('', self.initiators)

    def test_unmap_volume_by_initiators_unmap_volume_object_not_found_error(self):
        self._test_unpublish_volume_unmap_volume_with_error(array_errors.ObjectNotFoundError(''))

    def test_unmap_volume_by_initiators_unmap_volume_volume_already_unmapped_error(self):
        self._test_unpublish_volume_unmap_volume_with_error(array_errors.VolumeAlreadyUnmappedError(''))

    def test_unmap_volume_by_initiators_unmap_volume_permission_denied_error(self):
        self._test_unpublish_volume_unmap_volume_with_error(array_errors.PermissionDeniedError(''))

    def test_unmap_volume_by_initiators_unmap_volume_host_not_found_error(self):
        self._test_unpublish_volume_unmap_volume_with_error(array_errors.HostNotFoundError(''))

    def test_unmap_volume_by_initiators_unmap_volume_unmapping_error(self):
        self._test_unpublish_volume_unmap_volume_with_error(array_errors.UnmappingError('', '', ''))


class TestCopyToExistingVolumeFromSource(BaseMediatorAbstractSetUp):

    def setUp(self):
        super().setUp()
        self.source_capacity = 10
        self.required_bytes = 512

    def test_clone_volume_success(self):
        volume_id = common_settings.SOURCE_VOLUME_ID

        response_volume = utils.get_mock_mediator_response_volume()
        self.mediator.get_object_by_id.return_value = response_volume
        self.mediator.copy_to_existing_volume_from_source(response_volume, volume_id,
                                                          common_settings.VOLUME_OBJECT_TYPE, self.required_bytes)
        self.mediator.copy_to_existing_volume.assert_called_once_with(common_settings.VOLUME_UID, volume_id,
                                                                      self.source_capacity, self.required_bytes)

    def test_copy_to_existing_volume_success(self):
        snapshot_id = common_settings.SNAPSHOT_VOLUME_UID
        response_volume = utils.get_mock_mediator_response_volume()
        self.mediator.get_object_by_id.return_value = utils.get_mock_mediator_response_snapshot()
        self.mediator.copy_to_existing_volume_from_source(response_volume, snapshot_id,
                                                          common_settings.SNAPSHOT_OBJECT_TYPE, self.required_bytes)
        self.mediator.copy_to_existing_volume.assert_called_once_with(common_settings.VOLUME_UID, snapshot_id,
                                                                      self.source_capacity, self.required_bytes)

    def test_copy_to_existing_volume_error(self):
        snapshot_id = common_settings.SNAPSHOT_VOLUME_UID
        response_volume = utils.get_mock_mediator_response_volume()
        self.mediator.get_object_by_id.return_value = None

        with self.assertRaises(array_errors.ObjectNotFoundError):
            self.mediator.copy_to_existing_volume_from_source(response_volume, snapshot_id,
                                                              common_settings.SNAPSHOT_OBJECT_TYPE, self.required_bytes)

    def _test_copy_to_existing_volume_error(self, copy_exception, get_exception=None):
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
                                                              common_settings.VOLUME_OBJECT_TYPE, self.required_bytes)
        self.mediator.delete_volume.assert_called_with(target_volume_id)

    def test_copy_to_existing_volume_illegal_object_id(self):
        array_exception = array_errors.InvalidArgumentError('')
        self._test_copy_to_existing_volume_error(array_exception)

    def test_copy_to_existing_volume_permission_denied(self):
        array_exception = array_errors.PermissionDeniedError('')
        self._test_copy_to_existing_volume_error(array_exception)

    def test_copy_to_existing_volume_pool_missing(self):
        array_exception = array_errors.PoolParameterIsMissing('')
        self._test_copy_to_existing_volume_error(array_exception)

    def test_copy_to_existing_volume_general_error(self):
        array_exception = Exception('')
        self._test_copy_to_existing_volume_error(array_exception)

    def test_copy_to_existing_volume_get_object_general_error(self):
        array_exception = Exception('')
        self._test_copy_to_existing_volume_error(None, get_exception=array_exception)

    def test_copy_to_existing_volume_get_object_error(self):
        array_exception = array_errors.ExpectedSnapshotButFoundVolumeError('', '')
        self._test_copy_to_existing_volume_error(None, get_exception=array_exception)

    def test_copy_to_existing_volume_get_object_none(self):
        array_exception = array_errors.ObjectNotFoundError('')
        self._test_copy_to_existing_volume_error(None, get_exception=array_exception)
