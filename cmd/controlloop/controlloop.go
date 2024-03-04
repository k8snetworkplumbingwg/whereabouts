package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/go-co-op/gocron/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"

	nadclient "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned"
	nadinformers "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/informers/externalversions"

	wbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned"
	wbinformers "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/informers/externalversions"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/controlloop"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/reconciler"
)

const (
	allNamespaces               = ""
	controllerName              = "pod-ip-controlloop"
	reconcilerCronConfiguration = "/cron-schedule/config"
)

const (
	_ int = iota
	couldNotCreateController
	cronSchedulerCreationError
	fileWatcherError
	couldNotCreateConfigWatcherError
	initialReconsileFailed
)

const (
	defaultLogLevel = "debug"
)

func main() {
	logLevel := flag.String("log-level", defaultLogLevel, "Specify the pod controller application logging level")
	if logLevel != nil && logging.GetLoggingLevel().String() != *logLevel {
		logging.SetLogLevel(*logLevel)
	}
	logging.SetLogStderr(true)

	stopChan := make(chan struct{})
	errorChan := make(chan error)
	defer close(stopChan)
	defer close(errorChan)
	handleSignals(stopChan, os.Interrupt)

	networkController, err := newPodController(stopChan)
	if err != nil {
		_ = logging.Errorf("could not create the pod networks controller: %v", err)
		os.Exit(couldNotCreateController)
	}

	networkController.Start(stopChan)
	defer networkController.Shutdown()

	s, err := gocron.NewScheduler(gocron.WithLocation(time.UTC))
	if err != nil {
		os.Exit(cronSchedulerCreationError)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		_ = logging.Errorf("error creating configuration watcher: %v", err)
		os.Exit(fileWatcherError)
	}
	defer watcher.Close()

	// trigger one immediate reconcile before cron job start
	go reconciler.ReconcileIPs(errorChan)
	err = <-errorChan
	if err == nil {
		logging.Verbosef("initial reconciler success")
	} else {
		logging.Verbosef("initial reconciler failure: %s", err)
		os.Exit(initialReconsileFailed)
	}

	reconcilerConfigWatcher, err := reconciler.NewConfigWatcher(
		reconcilerCronConfiguration,
		s,
		watcher,
		func() {
			reconciler.ReconcileIPs(errorChan)
		},
	)
	if err != nil {
		os.Exit(couldNotCreateConfigWatcherError)
	}
	s.Start()

	const reconcilerConfigMntFile = "/cron-schedule/..data"
	p := func(e fsnotify.Event) bool {
		return e.Name == reconcilerConfigMntFile && e.Op&fsnotify.Create == fsnotify.Create
	}
	reconcilerConfigWatcher.SyncConfiguration(p)

	for {
		select {
		case <-stopChan:
			logging.Verbosef("shutting down network controller")
			if err := s.Shutdown(); err != nil {
				_ = logging.Errorf("error shutting : %v", err)
			}
			return
		case err := <-errorChan:
			if err == nil {
				logging.Verbosef("reconciler success")
			} else {
				logging.Verbosef("reconciler failure: %s", err)
			}
		}
	}
}

func handleSignals(stopChannel chan struct{}, signals ...os.Signal) {
	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, signals...)
	go func() {
		<-signalChannel
		stopChannel <- struct{}{}
	}()
}

func newPodController(stopChannel chan struct{}) (*controlloop.PodController, error) {
	var cfg *rest.Config
	var err error
	cfg, err = rest.InClusterConfig()
	if err != nil {
		logging.Debugf("failed to generate the kubeconfig from service account: %v", err)
		kubeConfigFile := os.Getenv("KUBECONFIG")
		if kubeConfigFile == "" {
			return nil, fmt.Errorf("KUBECONFIG environment variable not set")
		}
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeConfigFile)
		if err != nil {
			return nil, fmt.Errorf("failed to generate kubeconfig from file %s: %v", kubeConfigFile, err)
		}
	}

	k8sClientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create the Kubernetes client: %w", err)
	}

	nadK8sClientSet, err := nadclient.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	eventBroadcaster := newEventBroadcaster(k8sClientSet)

	wbClientSet, err := wbclient.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	const noResyncPeriod = 0
	ipPoolInformerFactory := wbinformers.NewSharedInformerFactory(wbClientSet, noResyncPeriod)
	netAttachDefInformerFactory := nadinformers.NewSharedInformerFactory(nadK8sClientSet, noResyncPeriod)
	podInformerFactory, err := controlloop.PodInformerFactory(k8sClientSet)
	if err != nil {
		return nil, err
	}

	controller := controlloop.NewPodController(
		k8sClientSet,
		wbClientSet,
		podInformerFactory,
		ipPoolInformerFactory,
		netAttachDefInformerFactory,
		eventBroadcaster,
		newEventRecorder(eventBroadcaster))
	logging.Verbosef("pod controller created")

	logging.Verbosef("Starting informer factories ...")
	podInformerFactory.Start(stopChannel)
	netAttachDefInformerFactory.Start(stopChannel)
	ipPoolInformerFactory.Start(stopChannel)
	logging.Verbosef("Informer factories started")

	return controller, nil
}

func newEventBroadcaster(k8sClientset kubernetes.Interface) record.EventBroadcaster {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(logging.Verbosef)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: k8sClientset.CoreV1().Events(allNamespaces)})
	return eventBroadcaster
}

func newEventRecorder(broadcaster record.EventBroadcaster) record.EventRecorder {
	return broadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerName})
}
