// +build !s390x

package device_connectivity

// For non Z-systems, the FC subsystem is pci
var FcSubsystem = "pci"
