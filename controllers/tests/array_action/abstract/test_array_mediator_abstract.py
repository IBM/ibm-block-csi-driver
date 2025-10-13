import unittest
from unittest.mock import Mock

import controllers.tests.array_action.test_settings as array_settings
import controllers.tests.common.test_settings as common_settings
from controllers.array_action.array_mediator_abstract import ArrayMediatorAbstract
from controllers.common.node_info import Initiators


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
