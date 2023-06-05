from controllers.common.config import config

NVME_OVER_FC_CONNECTIVITY_TYPE = config.connectivity_type.nvme_over_fc
FC_CONNECTIVITY_TYPE = config.connectivity_type.fc
ISCSI_CONNECTIVITY_TYPE = config.connectivity_type.iscsi
REPLICATION_COPY_TYPE_SYNC = "sync"
REPLICATION_COPY_TYPE_ASYNC = "async"
REPLICATION_TYPE_MIRROR = "mirror"
REPLICATION_TYPE_EAR = "ear"
REPLICATION_DEFAULT_COPY_TYPE = REPLICATION_COPY_TYPE_SYNC

ENDPOINT_TYPE_PRODUCTION = 'production'
ENDPOINT_TYPE_INDEPENDENT = 'independent'
ENDPOINT_TYPE_RECOVERY = 'recovery'

RCRELATIONSHIP_STATE_IDLE = 'idling'
RCRELATIONSHIP_STATE_READY = 'consistent_synchronized'

# volume context
CONTEXT_POOL = "pool"

WWN_OUI_END = 7
NGUID_OUI_END = 22
WWN_VENDOR_IDENTIFIER_END = 16
VENDOR_IDENTIFIER_LENGTH = 9

UNIQUE_KEY_KEY = 'unique_key'
METADATA_KEY = 'metadata'
VERSION_KEY = 'version'
REGISTRATION_PLUGIN = 'block.csi.ibm.com'
