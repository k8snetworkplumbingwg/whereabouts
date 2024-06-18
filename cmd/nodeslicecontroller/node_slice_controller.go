package main

import (
	"errors"
	"flag"
	"os"
	"time"

	nadclient "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned"
	nadinformers "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/informers/externalversions"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	clientset "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned"
	informers "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/informers/externalversions"
	node_controller "github.com/k8snetworkplumbingwg/whereabouts/pkg/node-controller"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/node-controller/signals"
)

var (
	masterURL  string
	kubeconfig string
)

// TODO: leader election
func main() {
	klog.InitFlags(nil)
	flag.Parse()

	// set up signals so we handle the shutdown signal gracefully
	ctx := signals.SetupSignalHandler()
	logger := klog.FromContext(ctx)

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
	if err != nil {
		logger.Error(err, "Error building kubeconfig")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		logger.Error(err, "Error building kubernetes clientset")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}

	whereaboutsClient, err := clientset.NewForConfig(cfg)
	if err != nil {
		logger.Error(err, "Error building kubernetes clientset")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}

	nadClient, err := nadclient.NewForConfig(cfg)
	if err != nil {
		logger.Error(err, "Error building kubernetes clientset")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}

	whereaboutsNamespace := os.Getenv("WHEREABOUTS_NAMESPACE")
	if whereaboutsNamespace == "" {
		logger.Error(errors.New("env var for WHEREABOUTS_NAMESPACE not set"), "unable to discover namespace")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}

	kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClient, time.Second*30)
	whereaboutsInformerFactory := informers.NewSharedInformerFactory(whereaboutsClient, time.Second*30)
	nadInformerFactory := nadinformers.NewSharedInformerFactory(nadClient, time.Second*30)

	controller := node_controller.NewController(
		ctx,
		kubeClient,
		whereaboutsClient,
		nadClient,
		kubeInformerFactory.Core().V1().Nodes(),
		whereaboutsInformerFactory.Whereabouts().V1alpha1().NodeSlicePools(),
		nadInformerFactory.K8sCniCncfIo().V1().NetworkAttachmentDefinitions(),
		false,
		whereaboutsNamespace,
	)

	// notice that there is no need to run Start methods in a separate goroutine. (i.e. go kubeInformerFactory.Start(ctx.done())
	// Start method is non-blocking and runs all registered informers in a dedicated goroutine.
	kubeInformerFactory.Start(ctx.Done())
	whereaboutsInformerFactory.Start(ctx.Done())
	nadInformerFactory.Start(ctx.Done())

	if err = controller.Run(ctx, 1); err != nil {
		logger.Error(err, "Error running controller")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
