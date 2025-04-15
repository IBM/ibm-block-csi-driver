
{{site.data.keyword.attribute-definition-list}}

# 1.12.0 (December 2024)

As of this document's publication date, IBM Power® and zLinux® architectures are not supported for this release. {: restriction}

IBM® Block Storage CSI driver 1.12.0 added new support and enhancements.
- Added support for PVC ReadWriteMany (RWX) access mode
- Added FlashSystem-specific configuration option for host definer portSet to set the port set for new ports created by host definer
- Added installation instructions on a detached environment
- Updated the log collection commands
- Updated information on how to enable multipath on all nodes
- Security fixes
- Upgraded running environment - now uses GO 1.22 and Python 3.9

IBM® block storage CSI driver version 1.12.0 resolved the following issues:

|Ticket ID|Severity|Description|
|---------|--------|-----------|
|**CSI-5727**|Medium|HostDefiner fails to match hosts with NQN identifiers when FlashSystem lsnvmefabric is disabled|

