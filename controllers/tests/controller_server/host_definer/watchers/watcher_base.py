import unittest

import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils


class WatcherBaseSetUp(unittest.TestCase):
    def setUp(self):
        test_utils.patch_k8s_api_init()
