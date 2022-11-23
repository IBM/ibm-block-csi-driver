from controllers.common.settings import HOST_DEFINITION_PLURAL, CSI_IBM_GROUP

STORAGE_API_VERSION = 'storage.k8s.io/v1'
CSI_PARAMETER_PREFIX = "csi.storage.k8s.io/"
CSINODE_KIND = 'CSINode'
CSI_IBM_API_VERSION = 'csi.ibm.com/v1'
HOST_DEFINITION_KIND = 'HostDefinition'
SECRET_NAME_SUFFIX = 'secret-name'
CSI_PROVISIONER_NAME = 'block.csi.ibm.com'
ADDED_EVENT = 'ADDED'
DELETED_EVENT = 'DELETED'
MODIFIED_EVENT = 'MODIFIED'
PENDING_PREFIX = 'Pending'
PENDING_CREATION_PHASE = 'PendingCreation'
PENDING_DELETION_PHASE = 'PendingDeletion'
ERROR_PHASE = 'Error'
READY_PHASE = 'Ready'
DRIVER_PRODUCT_LABEL = 'product=ibm-block-csi-driver'
DEFAULT_NAMESPACE = 'default'
HOST_DEFINER = 'hostDefiner'
MANAGE_NODE_LABEL = 'hostdefiner.block.csi.ibm.com/manage-node'
FORBID_DELETION_LABEL = 'hostdefiner.block.csi.ibm.com/do-not-delete-definition'
NODE_NAME_FIELD = 'nodeName'
SECRET_NAME_FIELD = 'secretName'
SECRET_NAMESPACE_FIELD = 'secretNamespace'
API_VERSION = 'apiVersion'
KIND = 'kind'
METADATA = 'metadata'
SPEC = 'spec'
HOST_DEFINITION_FIELD = 'hostDefinition'
PREFIX_ENV_VAR = 'PREFIX'
CONNECTIVITY_ENV_VAR = 'CONNECTIVITY_TYPE'
STATUS = 'status'
PHASE = 'phase'
LABELS = 'labels'
TRUE_STRING = 'true'
DYNAMIC_NODE_LABELING_ENV_VAR = 'DYNAMIC_NODE_LABELING'
ALLOW_DELETE_ENV_VAR = 'ALLOW_DELETE'
DEFINE_ACTION = 'Define'
UNDEFINE_ACTION = 'Undefine'
SUCCESS_MESSAGE = 'Host defined successfully on the array'
FAILED_MESSAGE_TYPE = 'Failed'
SUCCESSFUL_MESSAGE_TYPE = 'Successful'
NORMAL_EVENT_TYPE = 'Normal'
WARNING_EVENT_TYPE = 'Warning'
FINALIZERS = 'finalizers'
CSI_IBM_FINALIZER = HOST_DEFINITION_PLURAL + '.' + CSI_IBM_GROUP
HOST_DEFINITION_PENDING_RETRIES = 5
HOST_DEFINITION_PENDING_EXPONENTIAL_BACKOFF_IN_SECONDS = 3
HOST_DEFINITION_PENDING_DELAY_IN_SECONDS = 3
