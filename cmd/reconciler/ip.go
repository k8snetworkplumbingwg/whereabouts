package main

import (
	"flag"
	"os"

	"github.com/dougbtv/whereabouts/pkg/logging"
	"github.com/dougbtv/whereabouts/pkg/storage/kubernetes"
)

func main() {
	kubeConfigFile := flag.String("kubeconfig", "", "the path to the Kubernetes configuration file")
	flag.Parse()

	if *kubeConfigFile == "" {
		_ = logging.Errorf("must specify the kubernetes config file, via the '-kubeconfig' flag")
		os.Exit(kubeconfigNotFound)
	}
	logging.Debugf("Kubernetes config file located at: %s", *kubeConfigFile)

	_, err := kubernetes.NewClient(*kubeConfigFile)
	if err != nil {
		_ = logging.Errorf("failed to instantiate the Kubernetes client: %+v", err)
		os.Exit(couldNotConnectToKubernetes)
	}
	logging.Debugf("created kubernetes client via kubeconfig located at: %s", *kubeConfigFile)
}
