import unittest
from threading import Thread
from queue import Queue
from time import sleep
from controller.array_action.storage_agent import StorageAgent, get_agent, clear_agents, get_agents, detect_array_type
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

    def tearDown(self):
        clear_agents()

    def test_get_agent_return_new(self):
        self.assertEqual(len(get_agents()), 0)
        agent = get_agent("", "", ["ds8k_host", ])
        self.assertIsInstance(agent, StorageAgent)
        self.assertEqual(len(get_agents()), 1)
        get_agent("test", "test", ["ds8k_host", ])
        self.assertEqual(len(get_agents()), 2)

    def test_get_agent_return_existing(self):
        name = "test_name"
        password = "test_password"
        endpoints = ["ds8k_host", ]
        self.assertEqual(len(get_agents()), 0)
        agent = get_agent(name, password, endpoints)
        self.assertEqual(len(get_agents()), 1)
        new_agent = get_agent(name, password, endpoints)
        self.assertEqual(len(get_agents()), 1)
        self.assertEqual(id(agent), id(new_agent))

    def test_get_agent_return_new_when_password_changed(self):
        name = "test_name"
        endpoints = ["ds8k_host", ]
        self.assertEqual(len(get_agents()), 0)
        agent = get_agent(name, "pa", endpoints)
        self.assertEqual(len(get_agents()), 1)
        new_agent = get_agent(name, "pb", endpoints)
        self.assertEqual(len(get_agents()), 1)
        self.assertNotEqual(id(agent), id(new_agent))

    @patch("controller.array_action.storage_agent.ConnectionPool")
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

    def test_get_multiple_mediators_parallelly_in_different_threads(self):

        def verify_mediator(current_size):
            agent = get_agent("test", "test", ["ds8k_host", ])
            with agent.get_mediator() as mediator:
                self.assertIsInstance(mediator, DS8KArrayMediator)
                self.assertEqual(agent.conn_pool.current_size, current_size)
                # get_system is called in setUp() too.
                self.assertEqual(self.client_mock.get_system.call_count, current_size + 1)
                sleep(0.2)

        t1 = Thread(target=verify_mediator, args=(1, ))
        t2 = Thread(target=verify_mediator, args=(2, ))
        t3 = Thread(target=verify_mediator, args=(3, ))

        t1.start()
        t2.start()
        t3.start()
        t1.join()
        t2.join()
        t3.join()

        self.assertEqual(get_agent("test", "test", ["ds8k_host", ]).conn_pool.current_size, 3)

    def test_get_mediator_timeout(self):
        self._test_get_mediator_timeout()

    def test_get_mediator_find_available_one_before_timeout(self):
        self._test_get_mediator_timeout(False)

    def _test_get_mediator_timeout(self, is_timeout=True):

        timeout = 0.3
        if is_timeout:
            timeout = 0.1

        def blocking_action():
            with get_agent("test", "test", ["ds8k_host", ]).get_mediator():
                sleep(0.2)

        def new_action(in_q):
            if is_timeout:
                with self.assertRaises(array_errors.NoConnectionAvailableException):
                    with get_agent("test", "test", ["ds8k_host", ]).get_mediator(timeout=timeout):
                        in_q.put(True)
            else:
                with get_agent("test", "test", ["ds8k_host", ]).get_mediator(timeout=timeout):
                    in_q.put(True)

        # max_size for ds8k is 10
        for _ in range(10):
            t = Thread(target=blocking_action)
            t.start()

        # all the clients are in use, the new action waits for an available one.
        q = Queue()
        new_thread = Thread(target=new_action, args=(q, ))
        new_thread.start()
        new_thread.join()

        if is_timeout:
            self.assertTrue(q.empty())
        else:
            self.assertFalse(q.empty())
            self.assertTrue(q.get() is True)
