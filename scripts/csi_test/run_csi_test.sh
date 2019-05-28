./cmd/csi-sanity/csi-sanity  --csi.endpoint $1 --csi.secrets $2 --csi.testvolumeparameters $3 --ginkgo.focus $4
#./cmd/csi-sanity/csi-sanity  --csi.endpoint /tmp/b --csi.secrets olga_secrets --csi.testvolumeparameters olga_params --ginkgo.focus "ValidateVolumeCapabilities|ControllerPublishVolume"


