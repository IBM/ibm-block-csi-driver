# Compatibility and Requirements

- For iSCSI single path users (RHEL only) verify that multipathing is installed and running.

  Define a virtual multipath. For example, remove `find_multipaths yes` from the multipath.conf file.

  For example, to configure a Linux multipath device, verify that the `find_multipaths` parameter in the multipath.conf file is disabled by removing the `find_multipaths yes` string from the file.

  Be sure that there is at least one multipath defined. If not, define a virtual multipath (if single) - for example, for RHEL.

- Ensure iSCSI connectivity for RHEL users:

    -   `iscsi-initiator-utils` (if iSCSI connection is required)
    -   `xfsprogs` (if XFS file system is required)
    
    <pre>
    yum -y install iscsi-initiator-utils
    yum -y install xfsprogs
