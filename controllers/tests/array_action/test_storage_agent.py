import unittest
from threading import Lock, Thread
from time import sleep

from mock import patch, NonCallableMagicMock, Mock
from munch import Munch

import controllers.array_action.errors as array_errors
from controllers.array_action.array_mediator_ds8k import DS8KArrayMediator
from controllers.array_action.array_mediator_svc import SVCArrayMediator
from controllers.array_action.array_mediator_xiv import XIVArrayMediator
from controllers.array_action.errors import FailedToFindStorageSystemType
from controllers.array_action.storage_agent import (StorageAgent, get_agent, clear_agents,
                                                    get_agents, detect_array_type)
from controllers.servers.csi.controller_types import ArrayConnectionInfo


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


class Counter:
    def __init__(self):
        self._value = 0
        self._lock = Lock()

    def increment(self):
        with self._lock:
            self._value += 1

    def get_value(self):
        with self._lock:
            return self._value


class TestStorageAgent(unittest.TestCase):

    def setUp(self):
        self.endpoint = ["1.2.3.4"]
        self.client_mock = NonCallableMagicMock()
        patcher = patch('controllers.array_action.array_mediator_ds8k.RESTClient')
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

        socket_patcher = patch('controllers.array_action.storage_agent._socket_connect_test')
        self.socket_mock = socket_patcher.start()
        self.addCleanup(socket_patcher.stop)
        self.socket_mock.side_effect = _fake_socket_connect_test

        self.agent = StorageAgent(["ds8k_host", ], "", "")

    def tearDown(self):
        clear_agents()

    def test_get_agent_return_new(self):
        self.assertEqual(0, len(get_agents()))
        agent = get_agent(ArrayConnectionInfo(array_addresses=["ds8k_host", ], user="", password=""))
        self.assertIsInstance(agent, StorageAgent)
        self.assertEqual(1, len(get_agents()))
        get_agent(ArrayConnectionInfo(array_addresses=["ds8k_host", ], user="test", password="test"))
        self.assertEqual(2, len(get_agents()))

    def test_get_agent_return_existing(self):
        name = "test_name"
        password = "test_password"
        endpoints = ["ds8k_host", ]
        self.assertEqual(0, len(get_agents()))
        agent = get_agent(ArrayConnectionInfo(array_addresses=endpoints, user=name, password=password))
        self.assertEqual(1, len(get_agents()))
        new_agent = get_agent(ArrayConnectionInfo(array_addresses=endpoints, user=name, password=password))
        self.assertEqual(1, len(get_agents()))
        self.assertEqual(id(agent), id(new_agent))

    def test_get_agent_return_new_when_password_changed(self):
        name = "test_name"
        endpoints = ["ds8k_host", ]
        self.assertEqual(0, len(get_agents()))
        agent = get_agent(ArrayConnectionInfo(array_addresses=endpoints, user=name, password="pa"))
        self.assertEqual(1, len(get_agents()))
        new_agent = get_agent(ArrayConnectionInfo(array_addresses=endpoints, user=name, password="pb"))
        self.assertEqual(1, len(get_agents()))
        self.assertNotEqual(id(agent), id(new_agent))

    @patch("controllers.array_action.storage_agent.ConnectionPool")
    def test_detect_array_type(self, _):
        self.assertEqual(
            detect_array_type(["svc_host", ]),
            SVCArrayMediator.array_type
        )

        self.assertEqual(
            detect_array_type(["ds8k_host", ]),
            DS8KArrayMediator.array_type
        )

        self.assertEqual(
            detect_array_type(["xiv_host", ]),
            XIVArrayMediator.array_type
        )

        with self.assertRaises(FailedToFindStorageSystemType):
            detect_array_type(["unknown_host", ])

    def test_init_storage_agent_prepopulates_one_mediator(self):
        # one mediator client is already initialized.
        self.client_mock.get_system.assert_called_once_with()

    def test_get_mediator(self):
        with self.agent.get_mediator() as mediator:
            self.assertIsInstance(mediator, DS8KArrayMediator)
            self.assertEqual(1, self.client_mock.get_system.call_count)

    def test_get_multiple_mediators_sequentially(self):
        for _ in range(3):
            with self.agent.get_mediator() as mediator:
                self.assertIsInstance(mediator, DS8KArrayMediator)
                self.assertEqual(1, self.agent.conn_pool.current_size)
                self.assertEqual(1, self.client_mock.get_system.call_count)

        self.assertEqual(1, self.agent.conn_pool.current_size)

    def test_get_multiple_mediators_parallelly(self):
        with self.agent.get_mediator() as mediator1:
            self.assertIsInstance(mediator1, DS8KArrayMediator)
            self.assertEqual(1, self.agent.conn_pool.current_size)
            self.assertEqual(1, self.client_mock.get_system.call_count)
            with self.agent.get_mediator() as mediator2:
                self.assertIsInstance(mediator2, DS8KArrayMediator)
                self.assertEqual(2, self.agent.conn_pool.current_size)
                self.assertEqual(2, self.client_mock.get_system.call_count)
                with self.agent.get_mediator() as mediator3:
                    self.assertIsInstance(mediator3, DS8KArrayMediator)
                    self.assertEqual(3, self.agent.conn_pool.current_size)
                    self.assertEqual(3, self.client_mock.get_system.call_count)

        self.assertEqual(3, self.agent.conn_pool.current_size)

    def test_get_mediator_find_one_inactive(self):
        with self.agent.get_mediator() as mediator, self.agent.get_mediator():
            mediator.is_active = Mock(return_value=False)
        # two clients in the pool, but one of them are inactive after using.
        self.assertEqual(2, self.agent.conn_pool.current_size)

        for _ in range(10):
            with self.agent.get_mediator():
                pass
        # After some iteration, the inactive client is disconnected and removed.
        self.assertEqual(1, self.agent.conn_pool.current_size)

    @staticmethod
    def _wait_for_count(count, target_count):
        while count.get_value() != target_count:
            sleep(0.1)

    def test_get_multiple_mediators_parallely_in_different_threads(self):
        keep_alive_lock = Lock()
        count = Counter()

        def verify_mediator(current_size):
            agent = get_agent(ArrayConnectionInfo(array_addresses=["ds8k_host", ], user="test", password="test"))
            with agent.get_mediator() as mediator:
                self.assertIsInstance(mediator, DS8KArrayMediator)
                self.assertEqual(current_size, agent.conn_pool.current_size)
                # get_system is called in setUp() too.
                self.assertEqual(self.client_mock.get_system.call_count, current_size + 1)

                count.increment()
                with keep_alive_lock:
                    pass

        t1 = Thread(target=verify_mediator, args=(1,))
        t2 = Thread(target=verify_mediator, args=(2,))
        t3 = Thread(target=verify_mediator, args=(3,))

        with keep_alive_lock:
            t1.start()
            self._wait_for_count(count, 1)
            t2.start()
            self._wait_for_count(count, 2)
            t3.start()
            self._wait_for_count(count, 3)

        t1.join()
        t2.join()
        t3.join()

        self.assertEqual(get_agent(
            ArrayConnectionInfo(array_addresses=["ds8k_host", ], user="test", password="test")).conn_pool.current_size,
            3)

    def test_get_mediator_timeout(self):
        self._test_get_mediator_timeout()

    def test_get_mediator_find_available_one_before_timeout(self):
        self._test_get_mediator_timeout(False)

    def _test_get_mediator_timeout(self, is_timeout=True):
        keep_alive_lock = Lock()
        timeout = 0.3

        def blocking_action():
            with get_agent(
                    ArrayConnectionInfo(array_addresses=["ds8k_host", ], user="test", password="test")).get_mediator():
                with keep_alive_lock:
                    pass

        with keep_alive_lock:

            # max_size for ds8k is 10
            for _ in range(10):
                thread = Thread(target=blocking_action)
                thread.start()

            # all the clients are in use, the next section waits for an available one.
            if is_timeout:
                with self.assertRaises(array_errors.NoConnectionAvailableException):
                    with get_agent(ArrayConnectionInfo(array_addresses=["ds8k_host", ], user="test",
                                                       password="test")).get_mediator(timeout=timeout):
                        pass

        if not is_timeout:
            with get_agent(ArrayConnectionInfo(array_addresses=["ds8k_host", ], user="test",
                                               password="test")).get_mediator():
                pass
