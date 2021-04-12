module github.com/coryschwartz/lotus-kubeapi

go 1.13

require (
	github.com/filecoin-project/go-address v0.0.5
	github.com/filecoin-project/lotus v1.3.0
	github.com/go-logr/logr v0.1.0
	github.com/onsi/ginkgo v1.14.0
	github.com/onsi/gomega v1.10.1
	k8s.io/api v0.17.2
	k8s.io/apimachinery v0.17.2
	k8s.io/client-go v0.17.2
	sigs.k8s.io/controller-runtime v0.5.0
)

replace github.com/filecoin-project/filecoin-ffi => ../../filecoin-project/lotus/extern/filecoin-ffi

replace github.com/filecoin-project/lotus => ../../filecoin-project/lotus
