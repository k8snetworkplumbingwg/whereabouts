package testenvironment

import (
	"os"
	"strconv"

	core "k8s.io/api/core/v1"
)

type Configuration struct {
	KubeconfigPath      string
	numComputeNodes     int
	fillPercentCapacity int
	NumberOfIterations  int
}

func NewConfig() (*Configuration, error) {
	var err error

	kubeconfigPath := kubeConfig()
	numComputeNodes, err := computeNodes()
	if err != nil {
		return nil, err
	}
	fillPercentCapacity, err := fillPercent()
	if err != nil {
		return nil, err
	}
	numThrashIter, err := thrashIter()
	if err != nil {
		return nil, err
	}

	return &Configuration{
		KubeconfigPath:      kubeconfigPath,
		numComputeNodes:     numComputeNodes,
		fillPercentCapacity: fillPercentCapacity,
		NumberOfIterations:  numThrashIter,
	}, nil
}

func kubeConfig() string {
	const kubeconfig = "KUBECONFIG"
	kubeconfigPath, found := os.LookupEnv(kubeconfig)
	if !found {
		kubeconfigPath = "${HOME}/.kube/config"
	}
	return kubeconfigPath
}

func computeNodes() (int, error) {
	const numCompute = "NUMBER_OF_COMPUTE_NODES"
	numComputeNodes, found := os.LookupEnv(numCompute)
	if !found {
		numComputeNodes = "2"
	}
	return strconv.Atoi(numComputeNodes)
}

func fillPercent() (int, error) {
	const fillCapcity = "FILL_PERCENT_CAPACITY"
	fillPercentCapacity, found := os.LookupEnv(fillCapcity)
	if !found {
		fillPercentCapacity = "50"
	}
	return strconv.Atoi(fillPercentCapacity)
}

func thrashIter() (int, error) {
	const numThrash = "NUMBER_OF_THRASH_ITER"
	numThrashIter, found := os.LookupEnv(numThrash)
	if !found {
		numThrashIter = "1"
	}
	return strconv.Atoi(numThrashIter)
}

func (v Configuration) MaxReplicas(allPods []core.Pod) int32 {
	const maxPodsPerNode = 110
	return int32(
		(v.numComputeNodes*maxPodsPerNode - (len(allPods))) * v.fillPercentCapacity / 100)
}
