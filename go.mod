module github.com/ibm/ibm-block-csi-driver

go 1.13

require (
	github.com/container-storage-interface/spec v1.5.0
	github.com/golang/mock v1.3.1
	github.com/kubernetes-csi/csi-lib-utils v0.9.1
	github.com/sirupsen/logrus v1.6.0
	golang.org/x/sync v0.0.0-20190911185100-cd5d95a43a6e
	golang.org/x/sys v0.0.0-20200622214017-ed371f2e16b4
	google.golang.org/grpc v1.29.0
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/apimachinery v0.19.0
	k8s.io/client-go v0.19.0
	k8s.io/mount-utils v0.20.13
	k8s.io/utils v0.0.0-20201110183641-67b214c5f920
)
