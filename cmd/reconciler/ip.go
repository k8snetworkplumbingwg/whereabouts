package main

import (
	"flag"
	"os"

	"github.com/dougbtv/whereabouts/pkg/logging"
)

func main() {
	kubeConfigFile := flag.String("kubeconfig", "", "the path to the Kubernetes configuration file")
	flag.Parse()

	if *kubeConfigFile == "" {
		_ = logging.Errorf("must specify the kubernetes config file, via the '-kubeconfig' flag")
		os.Exit(kubeconfigNotFound)
	}

	logging.Debugf("Kubernetes config file located at: %s", kubeConfigFile)
}
