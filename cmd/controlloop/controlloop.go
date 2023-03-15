package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	gocron "github.com/go-co-op/gocron"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"

	nadclient "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned"
	nadinformers "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/informers/externalversions"

	wbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned"
	wbinformers "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/informers/externalversions"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/config"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/controlloop"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/reconciler"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

const (
	allNamespaces  = ""
	controllerName = "pod-ip-controlloop"
)

const (
	couldNotCreateController = 1
	couldNotReadFlatfile     = 1
	couldNotGetFlatIPAM      = 1
	cronExpressionError      = 1
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

	s := gocron.NewScheduler(time.UTC)
	schedule := cronExpressionFromFlatFile()

	_, err = s.Cron(schedule).Do(func() { // user configurable cron expression in install-cni.sh
		reconciler.ReconcileIPs(errorChan)
	})
	if err != nil {
		_ = logging.Errorf("error with cron expression schedule: %v", err)
		os.Exit(cronExpressionError)
	}

	s.StartAsync()

	for {
		select {
		case <-stopChan:
			logging.Verbosef("shutting down network controller")
			s.Stop()
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
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to implicitly generate the kubeconfig: %w", err)
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

func cronExpressionFromFlatFile() string {
	flatipam, _, err := config.GetFlatIPAM(true, &types.IPAMConfig{}, "")
	if err != nil {
		_ = logging.Errorf("could not get flatipam: %v", err)
		os.Exit(couldNotGetFlatIPAM)
	}
	return flatipam.IPAM.ReconcilerCronExpression
}
