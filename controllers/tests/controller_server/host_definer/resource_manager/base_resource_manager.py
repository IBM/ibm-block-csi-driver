import unittest

import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils


class BaseResourceManager(unittest.TestCase):
    def setUp(self):
        test_utils.patch_k8s_api_init()

    def _test_get_k8s_resources_info_success(self, function_to_test, k8s_function,
                                             get_info_function, fake_resource_info, fake_k8s_items):
        k8s_function.return_value = fake_k8s_items
        get_info_function.return_value = fake_resource_info
        result = function_to_test()
        self.assertEqual(result, [fake_resource_info])
        get_info_function.assert_called_once_with(fake_k8s_items.items[0])

    def _test_get_k8s_resources_info_empty_list_success(self, function_to_test, k8s_function, info_function):
        k8s_function.return_value = test_utils.get_fake_empty_k8s_list()
        result = function_to_test()
        self.assertEqual(result, [])
        info_function.assert_not_called()
