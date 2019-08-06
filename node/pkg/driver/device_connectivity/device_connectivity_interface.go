package device_connectivity

//go:generate mockgen -destination=../../../mocks/mock_OsDeviceConnectivityInterface.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity OsDeviceConnectivityInterface

type OsDeviceConnectivityInterface interface {
	RescanDevices(lunId int, arrayIdentifier string) error // For NVME lunID will be namespace ID.
	GetMpathDevice(volumeId string, lunId int, arrayIdentifier string) (string, error)
	FlushMultipathDevice(mpathDevice string) error
	RemovePhysicalDevice(sysDevices []string) error
}
