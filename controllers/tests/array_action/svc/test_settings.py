from controllers.tests.array_action.test_settings import ID_KEY, NAME_KEY

STATUS_KEY = "status"
NO_VALUE_ALIAS = "no"
YES_VALUE_ALIAS = "yes"
DUMMY_INTERNAL_ID1 = "1"
DUMMY_INTERNAL_ID2 = "2"
BYTE_UNIT_SYMBOL = "b"

NODE_ID_ATTR_KEY = ID_KEY
NODE_NAME_ATTR_KEY = NAME_KEY
NODE_ISCSI_NAME_ATTR_KEY = "iscsi_name"
NODE_STATUS_ATTR_KEY = STATUS_KEY

ONLINE_STATUS = "online"
OFFLINE_STATUS = "offline"
ACTIVE_STATE = "active"
INACTIVE_STATE = "inactive"

LSSYSTEM_LOCATION_ATTR_KEY = "location"
LSSYSTEM_ID_ALIAS_ATTR_KEY = "id_alias"
LSSYSTEM_CODE_LEVEL_ALIAS_ATTR_KEY = "code_level"

LOCAL_LOCATION = "local"
DUMMY_ID_ALIAS = "fake_identifier"

NODE_ID_KEY = "node_id"
IP_ADDRESS_KEY = "IP_address"
ADDRESS_6_ATTR_KEY = "IP_address_6"

LSPORTIP_NODE_ID_ATTR_KEY = NODE_ID_KEY
LSPORTIP_IP_ADDRESS_ATTR_KEY = IP_ADDRESS_KEY
LSPORTIP_IP_ADDRESS_6_ATTR_KEY = ADDRESS_6_ATTR_KEY

LSIP_NODE_ID_ATTR_KEY = NODE_ID_KEY
LSIP_IP_ADDRESS_ATTR_KEY = IP_ADDRESS_KEY
LSIP_PORTSET_ID_ATTR_KEY = "portset_id"

DUMMY_PORTSET_ID = "demo_id"
DUMMY_FCMAP_ID = "fc_id"

FCMAP_SOURCE_VDISK_NAME_ATTR_KEY = "source_vdisk_name"
FCMAP_TARGET_VDISK_NAME_ATTR_KEY = "target_vdisk_name"
FCMAP_ID_ATTR_KEY = ID_KEY
FCMAP_STATUS_ATTR_KEY = STATUS_KEY
FCMAP_COPY_RATE_ATTR_KEY = "copy_rate"
FCMAP_RC_CONTROLLED_ATTR_KEY = "rc_controlled"

DUMMY_FCMAP_BAD_STATUS = "not good"
DUMMY_COPY_RATE = "non_zero_value"
DUMMY_ZERO_COPY_RATE = "0"

VOLUME_VDISK_UID_ATTR_KEY = "vdisk_UID"
VOLUME_CAPACITY_ATTR_KEY = "capacity"
VOLUME_MDISK_GRP_NAME_ATTR_KEY = "mdisk_grp_name"
VOLUME_IO_GROUP_NAME_ATTR_KEY = "IO_group_name"
VOLUME_FC_ID_ATTR_KEY = "FC_id"
VOLUME_SE_COPY_ATTR_KEY = "se_copy"
VOLUME_DEDUPLICATED_COPY_ATTR_KEY = "deduplicated_copy"
VOLUME_COMPRESSED_COPY_ATTR_KEY = "compressed_copy"

MANY_VALUE = "many"
VOLUME_FC_ID_MANY = MANY_VALUE

SNAPSHOT_ID_ATTR_KEY = "snapshot_id"
SNAPSHOT_NAME_ATTR_KEY = "snapshot_name"
SNAPSHOT_VOLUME_ID_ATTR_KEY = "volume_id"
SNAPSHOT_VOLUME_NAME_ATTR_KEY = "volume_name"

DUMMY_IO_GROUP = "iogrp0"

POOL_MANY = MANY_VALUE

CREATE_VOLUME_NAME_ARGUMENT = NAME_KEY
CREATE_VOLUME_SIZE_UNIT_ARGUMENT = "unit"
CREATE_VOLUME_SIZE_ARGUMENT = "size"
CREATE_VOLUME_POOL_ARGUMENT = "pool"
CREATE_VOLUME_IO_GROUP_ARGUMENT = "iogrp"
CREATE_VOLUME_VOLUME_GROUP_ARGUMENT = "volumegroup"

SVC_RESPONSE_AS_LIST = "as_list"

HOST_MAP_ID_ATTR_KEY = ID_KEY
HOST_MAP_NAME_ATTR_KEY = NAME_KEY
HOST_MAP_LUN_ATTR_KEY = "SCSI_id"
HOST_MAP_HOST_ID_ATTR_KEY = "host_id"
HOST_MAP_HOST_NAME_ATTR_KEY = "host_name"

LSFABRIC_PORT_REMOTE_WWPN_ATTR_KEY = "remote_wwpn"
LSFABRIC_PORT_REMOTE_NPORTID_ATTR_KEY = "remote_nportid"
LSFABRIC_PORT_ID_ATTR_KEY = ID_KEY
LSFABRIC_PORT_NODE_NAME_ATTR_KEY = "node_name"
LSFABRIC_PORT_LOCAL_WWPN_ATTR_KEY = "local_wwpn"
LSFABRIC_PORT_LOCAL_PORT_ATTR_KEY = "local_port"
LSFABRIC_PORT_LOCAL_NPORTID_ATTR_KEY = "local_nportid"
LSFABRIC_PORT_STATE_ATTR_KEY = "state"
LSFABRIC_PORT_NAME_ATTR_KEY = NAME_KEY
LSFABRIC_PORT_CLUSTER_NAME_ATTR_KEY = "cluster_name"
LSFABRIC_PORT_TYPE_ATTR_KEY = "type"

DUMMY_REMOTE_WWPN1 = "21000024FF3A42E1"
DUMMY_LOCAL_WWPN1 = "5005076810282CD1"
DUMMY_REMOTE_NPORTID1 = "012F01"
DUMMY_LOCAL_NPORTID1 = "010601"
DUMMY_LOCAL_PORT1 = "1"
DUMMY_REMOTE_WWPN2 = "21000024FF3A42E2"
DUMMY_LOCAL_WWPN2 = "5005076810282CD2"
DUMMY_REMOTE_NPORTID2 = "012F02"
DUMMY_LOCAL_NPORTID2 = "010602"
DUMMY_LOCAL_PORT2 = "2"
DUMMY_PORT_NAME = "port_name"

SOURCE_IDS_UID = "uid"
SOURCE_IDS_INTERNAL_ID = "internal_id"
DUMMY_HOST_MAP_NAME = "host_map_name"

MKVOLUMEGROUP_CLONE_TYPE = "clone"
DUMMY_SPACE_EFFICIENCY = "space_efficiency"

LSMDISKGRP_SITE_NAME_ATTR_KEY = "site_name"
DUMMY_POOL_SITE = "pool_site"

LSRCRELATIONSHIP_AUX_VOLUME_ATTR_KEY = "aux_vdisk_name"
DUMMY_VOLUME_SITE1 = "volume_site_1"
DUMMY_VOLUME_SITE2 = "volume_site_2"

FILTERVALUE_DELIMITER = "="

INVALID_NAME_1 = "\xff"
INVALID_NAME_SYMBOLS = "!@#"
INVALID_NAME_START_WITH_NUMBER = "12345"
INVALID_NAME_TOO_LONG = "a" * 64
