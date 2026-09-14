
{{site.data.keyword.attribute-definition-list}}

# 1.14.0 (September 2026)

IBM® Block Storage CSI driver 1.14.0 added new support and enhancements.

For more information regarding the IBM FlashSystem® Call Home feature, search IBM.com/docs for your product's documentation.{: tip}

IBM® block storage CSI driver version 1.14.0 resolved the following issues:

|Ticket ID|Severity|Description|
|---------|--------|-----------|
|**CSI-6323**|Medium|Under large quantity of devices, CSI driver might detect more than one volumeID for single multipath name|
|**CSI-6266**|Medium|Kubelet PVC metrics do not appear when using user_friendly_names no|
|**CSI-6193**|Medium|Creation of volume from snapshot when using stretch cluster configuration may fail|
|**CSI-6060**|Medium|Expansion is blocked when fc_map exists|
