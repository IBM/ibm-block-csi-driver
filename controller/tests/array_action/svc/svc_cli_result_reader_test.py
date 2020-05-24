import unittest

from controller.array_action import errors
from controller.array_action.svc_cli_result_reader import SVCListResultsReader

host_1 = "\n".join(("id 1", "name host_1", "WWPN wwpn1", "protocol fc", "WWPN wwpn2"))
host_2 = "\n".join(("id 2", "name host_2", "", "   ", "iscsi", "status   not active"))
host_3 = "\n".join(("id 3", "name host_3", "iscsi iscsi1"))


class TestSVCListReader(unittest.TestCase):

    def test_single_host_success(self):
        hosts_reader = SVCListResultsReader(host_1)
        hosts_list = list(hosts_reader)
        self.assertEqual(len(hosts_list), 1)
        self._assert_host_1(hosts_list[0])

    def test_multiple_hosts_success(self):
        hosts_count = 3
        hosts_raw_input = "\n".join((host_1, "\n  \n", host_2, host_3, "  "))
        assert_methods = [self._assert_host_1, self._assert_host_2, self._assert_host_3]
        hosts_reader = SVCListResultsReader(hosts_raw_input)
        hosts_list = list(hosts_reader)
        self.assertEqual(len(hosts_list), hosts_count)
        for i in range(hosts_count):
            assert_methods[i](hosts_list[i])

    def test_no_hosts_empty_input(self):
        hosts_reader = SVCListResultsReader("")
        hosts_list = list(hosts_reader)
        self.assertFalse(hosts_list)

    def test_no_hosts_whitespace_input(self):
        hosts_reader = SVCListResultsReader("\n\n\n")
        hosts_list = list(hosts_reader)
        self.assertFalse(hosts_list)

    def test_illegal_input(self):
        illegal_input = "\n".join(("name host_3", "id 3"))
        with self.assertRaises(errors.InvalidCliResponseError):
            SVCListResultsReader(illegal_input)

    def _assert_host_1(self, host):
        self.assertEqual(host.get("id"), "1")
        self.assertEqual(host.get("name"), "host_1")
        self.assertEqual(host.get_as_list("WWPN"), ["wwpn1", "wwpn2"])
        self.assertEqual(host.get("protocol"), "fc")
        self.assertEqual(host.get_as_list("protocol"), ["fc"])
        self.assertEqual(host.get("non_existing_value", "no-value"), "no-value")
        self.assertEqual(host.get_as_list("non_existing_value"), [])

    def _assert_host_2(self, host):
        self.assertEqual(host.get("id"), "2")
        self.assertEqual(host.get("name"), "host_2")
        self.assertEqual(host.get("iscsi"), "")
        self.assertEqual(host.get("status"), "not active")

    def _assert_host_3(self, host):
        self.assertEqual(host.get("id"), "3")
        self.assertEqual(host.get("name"), "host_3")
        self.assertEqual(host.get("iscsi"), "iscsi1")
