
{{site.data.keyword.attribute-definition-list}}

# 1.13.1 (April 2026)

As of this document's publication date, the IBM Power® and zLinux® architectures are not supported for this release.{: restriction}

IBM® Block Storage CSI driver 1.13.1 added new support and enhancements.
- IBM Storage Virtualize® partitions PBHA for both FC and iSCSI hosts
- Support of NVMe over FC hosts
- Extended support to RedHat OpenShift® 4.21
- Extended support to Kubernetes 1.35
- More info in callhome

For more information regarding the IBM FlashSystem® Call Home feature, search IBM.com/docs for your product's documentation.{: tip}

IBM® block storage CSI driver version 1.13.1 resolved the following issues:

|Ticket ID|Severity|Description|
|---------|--------|-----------|
|**CSI-5997**|Medium|Storage port list is limited to 256 characters|
|**CSI-6032**|Medium|Add call home metadata|
|**CSI-6092**|Medium|HostDefiner upgrade may take default configuration in an intermediate step|