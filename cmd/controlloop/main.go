package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	v1coreinformerfactory "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"

	nadclient "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned"
	nadinformers "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/informers/externalversions"

	"github.com/k8snetworkplumbingwg/whereabouts/cmd/reconciler"
	wbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/clientset/versioned"
	wbinformers "github.com/k8snetworkplumbingwg/whereabouts/pkg/client/informers/externalversions"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/reconciler/controlloop"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	allNamespaces  = ""
	controllerName = "pod-ip-reconciler"
)

const (
	couldNotCreateController = 1
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
	returnErr := make(chan error)
	defer close(stopChan)
	defer close(returnErr)
	handleSignals(stopChan, os.Interrupt)

	networkController, err := newPodController(stopChan)
	if err != nil {
		_ = logging.Errorf("could not create the pod networks controller: %v", err)
		os.Exit(couldNotCreateController)
	}

	networkController.Start(stopChan)

	totalReconcilerSuccess := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "reconciler_success_total",
		Help: "Increments upon successful run of IP reconciler",
	})

	prometheus.MustRegister(totalReconcilerSuccess)

	http.Handle("/metrics", promhttp.Handler())

	// TODO: i want to generalize this - use random ip address instead of a specific one
	go func() {
		log.Fatal(http.ListenAndServe(":1984", nil)) // ListenAndServe is a blocking call.
	}()

	// here's where my for { select {} } loop should go - and use tickers
	// https://gobyexample.com/tickers
	// general logic - loop indefinitely, with the following conditions:
	// a. stopChan sends a value: quit out of the loop and return function
	// b. ticker ticks: start a goroutine to run ip-reconciler
	// c. default: continue to spin
	ticker := time.NewTicker(10 * time.Second) // temp value, will eventually be days/weeks duration

	i := 0
	for /*i := 0; i < 5; i++*/ {
		i++
		logging.Verbosef("iteration #%d", i)
		select {
		case <-stopChan:
			return
		case t := <-ticker.C:
			fmt.Println("Running ip-reconciler, tick at ", t)
			go reconciler.InvokeIPReconciler(returnErr)
		case err := <-returnErr: // why is this case only reached one time?
			logging.Verbosef("reached counter decision")
			if err == nil {
				totalReconcilerSuccess.Inc()
				logging.Verbosef("ip reconciler success!")
			} else {
				logging.Verbosef("ip reconciler failure: %s", err)
			}
		}
	}

	logging.Verbosef("loop ended...you have one minute to curl for counter results.")
	time.Sleep(1 * time.Minute)
	return
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
	podInformerFactory := v1coreinformerfactory.NewSharedInformerFactoryWithOptions(
		k8sClientSet, noResyncPeriod, v1coreinformerfactory.WithTweakListOptions(
			func(options *v1.ListOptions) {
				const (
					filterKey           = "spec.nodeName"
					hostnameEnvVariable = "HOSTNAME"
				)
				options.FieldSelector = fields.OneTermEqualSelector(filterKey, os.Getenv(hostnameEnvVariable)).String()
			}))

	controller := controlloop.NewPodController(
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
