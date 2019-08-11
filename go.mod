module github.com/ibm/ibm-block-csi-driver

go 1.12

require (
	github.com/container-storage-interface/spec v1.1.0
	github.com/golang/mock v1.2.0
	github.com/kubernetes-csi/csi-lib-iscsi v0.0.0-20190415173011-c545557492f4
	github.com/tebeka/go2xunit v1.4.10
	golang.org/x/tools v0.0.0-20190809145639-6d4652c779c4 // indirect
	google.golang.org/grpc v1.22.0
	gopkg.in/yaml.v2 v2.2.2
	k8s.io/apimachinery v0.0.0-20190727130956-f97a4e5b4abc // indirect
	k8s.io/klog v0.3.3
	k8s.io/kubernetes v1.13.1
	k8s.io/utils v0.0.0-20190712204705-3dccf664f023 // indirect
)
