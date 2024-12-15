# 1.11.0 (January 2023)

IBM® block storage CSI driver 1.11.0 added new support and enhancements.
- Added dynamic host definition enhancements
- New IBM Storage Virtualize family system support for policy-based replication and dynamic volume groups
- Additional orchestration platform support for Red Hat OpenShift 4.12 and Kubernetes 1.25 and 1.26

Version 1.11.0 also resolved the following issue:

|Ticket ID|Severity|Description|
|---------|--------|-----------|
|**CSI-4554**|Low|During HostDefinition creation no success event is emitted when the status is in a _Ready_ state.|
