import unittest

from controllers.servers.host_definer.utils import manifest_utils
import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.utils.k8s_manifests_utils as test_manifest_utils
import controllers.tests.controller_server.host_definer.settings as test_settings


class TestManifestUtils(unittest.TestCase):
    def setUp(self):
        pass

    def test_get_host_definition_manifest_success(self):
        fake_k8s_host_definition_manifest = test_manifest_utils.get_fake_k8s_host_definition_manifest()
        result = manifest_utils.get_host_definition_manifest(
            test_utils.get_fake_host_definition_info(), test_utils.get_fake_define_host_response(),
            test_settings.FAKE_NODE_ID)
        self.assertEqual(result[test_settings.SPEC_FIELD], fake_k8s_host_definition_manifest[test_settings.SPEC_FIELD])
        self.assertEqual(result[test_settings.API_VERSION_FIELD],
                         fake_k8s_host_definition_manifest[test_settings.API_VERSION_FIELD])
        self.assertEqual(result[test_settings.KIND_FIELD], fake_k8s_host_definition_manifest[test_settings.KIND_FIELD])

    def test_get_host_definition_status_manifest_success(self):
        fake_status_phase_manifest = test_manifest_utils.get_status_phase_manifest(test_settings.READY_PHASE)
        result = manifest_utils.get_host_definition_status_manifest(test_settings.READY_PHASE)
        self.assertEqual(result, fake_status_phase_manifest)

    def test_get_body_manifest_for_labels_success(self):
        fake_labels_body = test_manifest_utils.get_metadata_with_manage_node_labels_manifest(
            test_settings.TRUE_STRING)
        result = manifest_utils.get_body_manifest_for_labels(test_settings.TRUE_STRING)
        self.assertEqual(result, fake_labels_body)

    def test_get_finalizer_manifest_success(self):
        fake_finalizers_manifest = test_manifest_utils.get_finalizers_manifest([test_settings.CSI_IBM_FINALIZER, ])
        result = manifest_utils.get_finalizer_manifest(
            test_settings.FAKE_NODE_NAME, [test_settings.CSI_IBM_FINALIZER, ])
        self.assertEqual(result, fake_finalizers_manifest)
