# Compatibility and Requirements

For iSCSI single path users (RHEL only) verify that multipathing is installed and running.

Define a virtual multipath. For example, remove `find_multipaths yes` from the multipath.conf file.

Be sure that there is at least one multipath defined. If not, define a virtual multipath (if single) - for example, for RHEL.