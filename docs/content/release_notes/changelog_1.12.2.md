
{{site.data.keyword.attribute-definition-list}}

# 1.12.2 (March 2025)

As of this document's publication date, the IBM Power® architecture is not supported for this release.{: restriction}

IBM® Block Storage CSI driver 1.12.2 added new support and enhancements.
- Utilizing FlashSystem plugin callhome extension to send IBM® Block Storage CSI driver callhome data
- Security fixes and 3rd party dependency updates

IBM® block storage CSI driver version 1.12.2 resolved the following issues:

|Ticket ID|Severity|Description|
|---------|--------|-----------|
|**CSI-5769**|Service|In case the NVMe cli package is installed on a Kubernetes cluster node, but the NVMe kernel modules are not loaded, PVC resizing/expansion will not work.|

