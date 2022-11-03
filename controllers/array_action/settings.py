from controllers.common.config import config

NVME_OVER_FC_CONNECTIVITY_TYPE = config.connectivity_type.nvme_over_fc
FC_CONNECTIVITY_TYPE = config.connectivity_type.fc
ISCSI_CONNECTIVITY_TYPE = config.connectivity_type.iscsi
REPLICATION_COPY_TYPE_SYNC = "sync"
REPLICATION_COPY_TYPE_ASYNC = "async"
REPLICATION_TYPE_MIRROR = "mirror"
REPLICATION_TYPE_EAR = "ear"
REPLICATION_DEFAULT_COPY_TYPE = REPLICATION_COPY_TYPE_SYNC
FC_NVME_HOST_PROTOCOL = 'fcnvme'
SCSI_HOST_PROTOCOL = 'scsi'

# volume context
CONTEXT_POOL = "pool"

WWN_OUI_END = 7
WWN_VENDOR_IDENTIFIER_END = 16
