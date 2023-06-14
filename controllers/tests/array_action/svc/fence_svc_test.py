import unittest

from mock import Mock
from munch import Munch

from controllers.tests.array_action.svc.array_mediator_svc_test import TestArrayMediatorSVC
from controllers.tests.common.test_settings import HOST_NAME, VOLUME_NAME, POOL_ID, POOL_NAME, FENCE_OWNERSHIP_GROUP, \
    UNFENCE_OWNERSHIP_GROUP


class MyTestCase(TestArrayMediatorSVC):
    def test_is_fenced_true(self):
        self.svc.client.svcinfo.lsmdiskgrp.return_value = Mock(as_list=[])
        is_fenced = self.svc.is_fenced(FENCE_OWNERSHIP_GROUP)
        self.assertTrue(is_fenced)
        self.svc.client.svcinfo.lsmdiskgrp.assert_called_once_with(
            filtervalue='owner_name={}'.format(FENCE_OWNERSHIP_GROUP))

    def test_is_fenced_false(self):
        self.svc.client.svcinfo.lsmdiskgrp.return_value = Mock(as_list=[Munch({"name": POOL_NAME})])
        is_fenced = self.svc.is_fenced(FENCE_OWNERSHIP_GROUP)
        self.assertFalse(is_fenced)
        self.svc.client.svcinfo.lsmdiskgrp.assert_called_once_with(
            filtervalue='owner_name={}'.format(FENCE_OWNERSHIP_GROUP))

    def test_fence_rmvdiskhostmap_called(self):
        self.svc.client.svcinfo.lsmdiskgrp.return_value = Mock(as_list=[Munch({"name": POOL_NAME, "id": POOL_ID})])
        self.svc.client.svcinfo.lshost.return_value = Mock(as_list=[Munch({"name": HOST_NAME})])
        self.svc.client.svcinfo.lsvdisk.return_value = Mock(as_list=[Munch({"name": VOLUME_NAME})])
        self.svc.client.svcinfo.lshostvdiskmap.return_value = Mock(
            as_list=[Munch({"name": HOST_NAME, "vdisk_name": VOLUME_NAME})])
        self.svc.fence(FENCE_OWNERSHIP_GROUP, UNFENCE_OWNERSHIP_GROUP)
        self.svc.client.svctask.rmvdiskhostmap.assert_called_once_with(vdisk_name=VOLUME_NAME, host=HOST_NAME)

    def test_fence_rmvdiskhostmap_not_called(self):
        self.svc.client.svcinfo.lsmdiskgrp.return_value = Mock(as_list=[Munch({"name": POOL_NAME, "id": POOL_ID})])
        self.svc.client.svcinfo.lshost.return_value = Mock(as_list=[Munch({"name": HOST_NAME})])
        self.svc.client.svcinfo.lsvdisk.return_value = Mock(as_list=[Munch({"name": VOLUME_NAME})])
        self.svc.client.svcinfo.lshostvdiskmap.return_value = Mock(as_list=[])
        self.svc.fence(FENCE_OWNERSHIP_GROUP, UNFENCE_OWNERSHIP_GROUP)
        self.svc.client.svctask.rmvdiskhostmap.assert_not_called()


if __name__ == '__main__':
    unittest.main()
