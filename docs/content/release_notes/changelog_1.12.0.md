# 1.12.0 (December 2024)

IBM® block storage CSI driver 1.11.0 added new support and enhancements.
- Added support for PVC RWX access mode (common use case is OpenShift VM Live Migration)
- Added SVC-specific configuration option for host definer portSet to set the port set for new ports created by host definer
- Added new tool for automatic log collection
- Security fixes
- Upgraded running environment - now uses GO 1.22 and Python 3.9

IBM® block storage CSI driver version 1.12.0 resolved the following issues:

|Ticket ID|Severity|Description|
|---------|--------|-----------|
|**CSI-5712**|Low|Document procedure for detached install of CSI|
