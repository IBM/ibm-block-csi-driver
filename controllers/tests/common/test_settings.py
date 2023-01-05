from controllers.common import settings as common_settings
SECRET_USERNAME_KEY = "username"
SECRET_USERNAME_VALUE = "dummy_username"
SECRET_PASSWORD_KEY = "password"
SECRET_PASSWORD_VALUE = "dummy_password"
SECRET_MANAGEMENT_ADDRESS_KEY = "management_address"
SECRET_MANAGEMENT_ADDRESS_VALUE = "dummy_management_address"
SECRET = {SECRET_USERNAME_KEY: SECRET_USERNAME_VALUE, SECRET_PASSWORD_KEY: SECRET_PASSWORD_VALUE,
          SECRET_MANAGEMENT_ADDRESS_KEY: SECRET_MANAGEMENT_ADDRESS_VALUE}

ARRAY = "arr"
DUMMY_POOL1 = "pool1"
DUMMY_POOL2 = "pool2"
STRETCHED_POOL = "pool1:pool2"
SPACE_EFFICIENCY = "thin"
VIRT_SNAP_FUNC_TRUE = "true"
DUMMY_IO_GROUP = "iogrp1"
DUMMY_FULL_IO_GROUP = common_settings.FULL_IO_GROUP
DUMMY_VOLUME_GROUP = "volgrp1"

ID_FORMAT = "a9k:{};{}"

VOLUME_OBJECT_TYPE = "volume"
VOLUME_NAME = "volume_name"
VOLUME_UID = "volume_wwn"
SOURCE_VOLUME_NAME = "source_volume"
SOURCE_ID = "source_id"
SOURCE_VOLUME_ID = "source_volume_id"
TARGET_VOLUME_ID = "target_volume_id"
TARGET_VOLUME_NAME = "target_volume_name"
INTERNAL_VOLUME_ID = "internal_volume_id"
REQUEST_VOLUME_ID = ID_FORMAT.format(INTERNAL_VOLUME_ID, VOLUME_UID)

NAME_PREFIX = "prefix"

HOST_NAME = "host_name"

OBJECT_INTERNAL_ID = "object_internal_id"
OTHER_OBJECT_INTERNAL_ID = "other_object_internal_id"

SNAPSHOT_OBJECT_TYPE = "snapshot"
SNAPSHOT_NAME = "snapshot_name"
SNAPSHOT_VOLUME_UID = "12345678"
SNAPSHOT_VOLUME_NAME = "snapshot_volume"
INTERNAL_SNAPSHOT_ID = "internal_snapshot_id"

CLONE_VOLUME_NAME = "clone_volume"
REPLICATION_NAME = "replication_name"
SYSTEM_ID = "system_id"
COPY_TYPE = "async"
FCS_DELIMITER = ":"

VOLUME_GROUP_OBJECT_TYPE = "volume group"
VOLUME_GROUP_NAME = "volume_group_name"
VOLUME_GROUP_UID = "volume_group_wwn"
INTERNAL_VOLUME_GROUP_ID = "internal_volume_group_id"
REQUEST_VOLUME_GROUP_ID = ID_FORMAT.format(INTERNAL_VOLUME_GROUP_ID, VOLUME_GROUP_UID)

HOST_OBJECT_TYPE = "host"
