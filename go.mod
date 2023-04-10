module github.com/ibm/ibm-block-csi-driver

go 1.13

require (
	github.com/container-storage-interface/spec v1.8.0
	github.com/golang/mock v1.3.1
	github.com/kubernetes-csi/csi-lib-utils v0.9.1
	github.com/sirupsen/logrus v1.6.0
	golang.org/x/sync v0.0.0-20220722155255-886fb9371eb4
	golang.org/x/sys v0.5.0
	google.golang.org/grpc v1.29.0
	gopkg.in/yaml.v2 v2.2.8
	k8s.io/apimachinery v0.19.0
	k8s.io/client-go v0.19.0
	k8s.io/mount-utils v0.20.13
	k8s.io/utils v0.0.0-20201110183641-67b214c5f920
)
