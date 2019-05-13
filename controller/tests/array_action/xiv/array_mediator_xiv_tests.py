import unittest
from pyxcli import errors as xcli_errors
from controller.array_action.array_mediator_xiv import XIVArrayMediator
from mock import patch, Mock
import controller.array_action.errors as array_errors
from controller.tests.array_action.xiv import utils


class TestArrayMediatorXIV(unittest.TestCase):

    @patch("controller.array_action.array_mediator_xiv.XIVArrayMediator._connect")
    def setUp(self, connect):
        self.fqdn = "fqdn"
        self.mediator = XIVArrayMediator("user", "password", self.fqdn)
        self.mediator.client = Mock()

    def test_get_volume_return_correct_errors(self):
        error_msg = "ex"
        self.mediator.client.cmd.vol_list.side_effect = [Exception("ex")]
        with self.assertRaises(Exception) as ex:
            self.mediator.get_volume("some name")

        self.assertTrue(error_msg in ex.exception)

    def test_get_volume_return_correct_value(self):
        vol = utils.get_mock_xiv_volume(10, "vol_name", "wwn")
        ret = Mock()
        ret.as_single_element = vol
        self.mediator.client.cmd.vol_list.return_value = ret
        res = self.mediator.get_volume("some name")

        self.assertTrue(res.capacity_bytes == vol.capacity * 512)
        self.assertTrue(res.capacity_bytes == vol.capacity * 512)

    def test_get_volume_returns_illegal_object_name(self):
        self.mediator.client.cmd.vol_list.side_effect = [xcli_errors.IllegalNameForObjectError("", "vol", "")]
        with self.assertRaises(array_errors.IllegalObjectName):
            res = self.mediator.get_volume("vol")

    def test_get_volume_returns_nothing(self):
        ret = Mock()
        ret.as_single_element = None
        self.mediator.client.cmd.vol_list.return_value = ret
        with self.assertRaises(array_errors.VolumeNotFoundError):
            res = self.mediator.get_volume("vol")

    @patch("controller.array_action.array_mediator_xiv.XCLIClient")
    def test_connect_errors(self, client):
        client.connect_multiendpoint_ssl.return_value = Mock()
        client.connect_multiendpoint_ssl.side_effect = [xcli_errors.CredentialsError("a", "b", "c")]
        with self.assertRaises(array_errors.CredentialsError):
            self.mediator._connect()

        client.connect_multiendpoint_ssl.side_effect = [xcli_errors.XCLIError()]
        with self.assertRaises(array_errors.CredentialsError) as ex:
            self.mediator._connect()

    @patch("controller.array_action.array_mediator_xiv.XCLIClient")
    def test_close(self, client):
        self.mediator.client.is_connected = lambda: True
        self.mediator.disconnect()
        self.mediator.client.close.assert_called_once_with()

        self.mediator.client.is_connected = lambda: False
        self.mediator.disconnect()
        self.mediator.client.close.assert_called_once_with()

    def test_create_volume_return_illegal_name_for_object(self):
        self.mediator.client.cmd.vol_create.side_effect = [xcli_errors.IllegalNameForObjectError("", "vol", "")]
        with self.assertRaises(array_errors.IllegalObjectName):
            self.mediator.create_volume("vol", 10, [], "pool1")

    def test_create_volume_return_volume_exists_error(self):
        self.mediator.client.cmd.vol_create.side_effect = [xcli_errors.VolumeExistsError("", "vol", "")]
        with self.assertRaises(array_errors.VolumeAlreadyExists):
            self.mediator.create_volume("vol", 10, [], "pool1")

    def test_create_volume_return_pool_does_not_exists_error(self):
        self.mediator.client.cmd.vol_create.side_effect = [xcli_errors.PoolDoesNotExistError("", "pool", "")]
        with self.assertRaises(array_errors.PoolDoesNotExist):
            self.mediator.create_volume("vol", 10, [], "pool1")

    @patch.object(XIVArrayMediator, "_generate_volume_response")
    def test_create_volume__generate_volume_response_return_exception(self, response):
        response.side_effect = Exception("err")
        with self.assertRaises(Exception):
            self.mediator.create_volume("vol", 10, [], "pool1")

    def test_delete_volume_return_volume_not_found(self):
        ret = Mock()
        ret.as_single_element = None
        self.mediator.client.cmd.vol_list.return_value = ret
        with self.assertRaises(array_errors.VolumeNotFoundError):
            self.mediator.delete_volume("vol-wwn")

    def test_delete_volume_return_bad_name_error(self):
        self.mediator.client.cmd.vol_delete.side_effect = [xcli_errors.VolumeBadNameError("", "vol", "")]
        with self.assertRaises(array_errors.VolumeNotFoundError):
            self.mediator.delete_volume("vol-wwn")
