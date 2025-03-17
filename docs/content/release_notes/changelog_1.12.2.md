
{{site.data.keyword.attribute-definition-list}}

# 1.12.2 (March 2025)

As of this document's publication date, the IBM Power® architecture is not supported for this release.{: restriction}

IBM® Block Storage CSI driver 1.12.2 added new support and enhancements.
- Security fixes and 3rd party dependency updates
- Utilizing IBM FlashSystem® Call Home extension for plugins to send IBM® Block Storage CSI driver callhome data

For more information regarding the IBM FlashSystem® Call Home feature, search IBM.com/docs for your product's documentation.{: tip}

IBM® block storage CSI driver version 1.12.2 resolved the following issues:

|Ticket ID|Severity|Description|
|---------|--------|-----------|
|**CSI-5769**|Service|In case the NVMe cli package is installed on a Kubernetes cluster node, but the NVMe kernel modules are not loaded, PVC resizing/expansion will not work.|
|**CSI-5783**|Service|CSI does not reuse existing mappings between host and volumes.|
