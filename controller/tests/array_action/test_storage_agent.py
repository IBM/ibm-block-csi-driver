import unittest
import gevent
import time
from controller.array_action.storage_agent import StorageAgent
from munch import Munch
from mock import patch, NonCallableMagicMock, Mock
from controller.array_action.errors import FailedToFindStorageSystemType
from controller.array_action.array_mediator_xiv import XIVArrayMediator
from controller.array_action.array_mediator_svc import SVCArrayMediator
from controller.array_action.array_mediator_ds8k import DS8KArrayMediator
import controller.array_action.errors as array_errors


def _fake_socket_connect_test(host, port):
    # arrays is a [host, open_ports] dict, note that both port 22 and 8452 are opened in ds8k
    arrays = {
        "svc_host": [SVCArrayMediator.port, ],
        "ds8k_host": [DS8KArrayMediator.port, 22],
        "xiv_host": [XIVArrayMediator.port, ],

    }

    if host in arrays and port in arrays[host]:
        return 0
    return 1


class TestStorageAgent(unittest.TestCase):

    def setUp(self):
        self.endpoint = ["1.2.3.4"]
        self.client_mock = NonCallableMagicMock()
        patcher = patch('controller.array_action.array_mediator_ds8k.RESTClient')
        self.connect_mock = patcher.start()
        self.addCleanup(patcher.stop)
        self.connect_mock.return_value = self.client_mock

        self.client_mock.get_system.return_value = Munch(
            {"id": "dsk array id",
             "name": "mtc032h",
             "state": "online",
             "release": "7.4",
             "bundle": "87.51.47.0",
             "MTM": "2421-961",
             "sn": "75DHZ81",
             "wwnn": "5005076306FFD2F0",
             "cap": "440659",
             "capalloc": "304361",
             "capavail": "136810",
             "capraw": "73282879488"
             }
        )

        socket_patcher = patch('controller.array_action.storage_agent._socket_connect_test')
        self.socket_mock = socket_patcher.start()
        self.addCleanup(socket_patcher.stop)
        self.socket_mock.side_effect = _fake_socket_connect_test

        self.agent = StorageAgent(["ds8k_host", ], "", "")

    @patch("controller.array_action.storage_agent.ConnectionPool")
    def test_detect_array_type(self, _):
        self.assertEqual(
            StorageAgent(["svc_host", ], "", "").detect_array_type(),
            SVCArrayMediator.array_type
        )

        self.assertEqual(
            StorageAgent(["ds8k_host", ], "", "").detect_array_type(),
            DS8KArrayMediator.array_type
        )

        self.assertEqual(
            StorageAgent(["xiv_host", ], "", "").detect_array_type(),
            XIVArrayMediator.array_type
        )

        with self.assertRaises(FailedToFindStorageSystemType):
            StorageAgent(["unknown_host", ], "", "",)

    def test_init_StorageAgent_prepopulates_one_mediator(self):
        # one mediator client is already initialized.
        self.client_mock.get_system.assert_called_once_with()

    def test_get_mediator(self):
        with self.agent.get_mediator() as mediator:
            self.assertIsInstance(mediator, DS8KArrayMediator)
            self.assertEqual(self.client_mock.get_system.call_count, 1)

    def test_get_multiple_mediators_sequentially(self):
        for i in range(3):
            with self.agent.get_mediator() as mediator:
                self.assertIsInstance(mediator, DS8KArrayMediator)
                self.assertEqual(self.agent.conn_pool.current_size, 1)
                self.assertEqual(self.client_mock.get_system.call_count, 1)

        self.assertEqual(self.agent.conn_pool.current_size, 1)

    def test_get_multiple_mediators_parallelly(self):
        with self.agent.get_mediator() as mediator1:
            self.assertIsInstance(mediator1, DS8KArrayMediator)
            self.assertEqual(self.agent.conn_pool.current_size, 1)
            self.assertEqual(self.client_mock.get_system.call_count, 1)
            with self.agent.get_mediator() as mediator2:
                self.assertIsInstance(mediator2, DS8KArrayMediator)
                self.assertEqual(self.agent.conn_pool.current_size, 2)
                self.assertEqual(self.client_mock.get_system.call_count, 2)
                with self.agent.get_mediator() as mediator3:
                    self.assertIsInstance(mediator3, DS8KArrayMediator)
                    self.assertEqual(self.agent.conn_pool.current_size, 3)
                    self.assertEqual(self.client_mock.get_system.call_count, 3)

        self.assertEqual(self.agent.conn_pool.current_size, 3)

    def test_get_multiple_mediators_parallelly_in_different_threads(self):

        def verify_mediator(current_size):
            with self.agent.get_mediator() as mediator:
                self.assertIsInstance(mediator, DS8KArrayMediator)
                self.assertEqual(self.agent.conn_pool.current_size, current_size)
                self.assertEqual(self.client_mock.get_system.call_count, current_size)
                gevent.sleep(0.1)

        g1 = gevent.spawn(verify_mediator, 1)
        g2 = gevent.spawn(verify_mediator, 2)
        g3 = gevent.spawn(verify_mediator, 3)

        g1.join()
        g2.join()
        g3.join()

        self.assertEqual(self.agent.conn_pool.current_size, 3)

    def test_get_mediator_find_one_inactive(self):
        with self.agent.get_mediator() as mediator, self.agent.get_mediator():
            mediator.is_active = Mock(return_value=False)
        # two clients in the pool, but one of them are inactive after using.
        self.assertEqual(self.agent.conn_pool.current_size, 2)

        for i in range(10):
            with self.agent.get_mediator():
                pass
        # After some iteration, the inactive client is disconnected and removed.
        self.assertEqual(self.agent.conn_pool.current_size, 1)

    def test_get_mediator_timeout(self):
        self._test_get_mediator_with_timeout()

    def test_get_mediator_find_available_one_before_timeout(self):
        # self._test_get_mediator_with_timeout(False)
        pass

    def _test_get_mediator_with_timeout(self, is_timeout=True):
        timeout = 10
        if is_timeout:
            timeout = 0.1

        def blocking_action():
            with self.agent.get_mediator():
                gevent.sleep(0.2)

        def new_action():
            called = False
            if is_timeout:
                with self.assertRaises(array_errors.NoConnectionAvailableException):
                    with self.agent.get_mediator(timeout=timeout):
                        called = True
            else:
                with self.agent.get_mediator(timeout=timeout):
                    called = True

            # wait till all block actions finish.
            time.sleep(0.2)
            return called

        # max_size for ds8k is 10
        for i in range(10):
            gevent.spawn(blocking_action)

        # all the 10 clients are in use, the new action waits for an available client and timeout.
        new_greenlet = gevent.spawn(new_action)
        new_greenlet.join()

        called = new_greenlet.value
        if is_timeout:
            self.assertTrue(called is False)
        else:
            self.assertTrue(called)
