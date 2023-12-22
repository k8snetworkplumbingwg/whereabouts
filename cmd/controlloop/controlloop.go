package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/go-co-op/gocron/v2"
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
	allNamespaces               = ""
	controllerName              = "pod-ip-controlloop"
	reconcilerCronConfiguration = "/cron-schedule/config"
)

const (
	_ int = iota
	couldNotCreateController
	couldNotGetFlatIPAM
	cronExpressionError
	cronSchedulerCreationError
	fileWatcherError
	fileWatcherAddWatcherError
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
	schedule := determineCronExpression()

	job, err := s.NewJob(
		gocron.CronJob(schedule, false),
		gocron.NewTask(func() {
			reconciler.ReconcileIPs(errorChan)
		}),
	)
	if err != nil {
		_ = logging.Errorf("error with cron expression schedule: %v", err)
		os.Exit(cronExpressionError)
	}

	logging.Verbosef("started cron with job ID: %q", job.ID().String())
	s.Start()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		_ = logging.Errorf("error creating configuration watcher: %v", err)
		os.Exit(fileWatcherError)
	}
	defer watcher.Close()

	go syncConfiguration(watcher, s, job, errorChan)
	if err := watcher.Add(reconcilerCronConfiguration); err != nil {
		_ = logging.Errorf("error adding watcher to config %q: %v", reconcilerCronConfiguration, err)
		os.Exit(fileWatcherAddWatcherError)
	}

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

func determineCronExpression() string {
	// We read the expression from a file if present, otherwise we use ReconcilerCronExpression
	fileContents, err := os.ReadFile(reconcilerCronConfiguration)
	if err != nil {
		flatipam, _, err := config.GetFlatIPAM(true, &types.IPAMConfig{}, "")
		if err != nil {
			_ = logging.Errorf("could not get flatipam config: %v", err)
			os.Exit(couldNotGetFlatIPAM)
		}
		_ = logging.Errorf("could not read file: %v, using expression from flatfile: %v", err, flatipam.IPAM.ReconcilerCronExpression)
		return flatipam.IPAM.ReconcilerCronExpression
	}
	logging.Verbosef("using expression: %v", strings.TrimSpace(string(fileContents))) // do i need to trim spaces? idk i think the file would JUST be the expression?
	return strings.TrimSpace(string(fileContents))
}

func syncConfiguration(
	watcher *fsnotify.Watcher,
	scheduler gocron.Scheduler,
	job gocron.Job,
	errorChannel chan error,
) {
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			updatedSchedule := determineCronExpression()
			logging.Verbosef(
				"configuration updated to file %q. New cron expression: %s",
				event.Name,
				updatedSchedule,
			)
			updatedJob, err := scheduler.Update(
				job.ID(),
				gocron.CronJob(updatedSchedule, false),
				gocron.NewTask(func() {
					reconciler.ReconcileIPs(errorChannel)
				}),
			)
			if err != nil {
				_ = logging.Errorf("error updating job %q configuration: %v", job.ID().String(), err)
			}

			logging.Verbosef(
				"successfully updated CRON configuration id %q - new cron expression: %s",
				updatedJob.ID().String(),
				updatedSchedule,
			)
		case err, ok := <-watcher.Errors:
			_ = logging.Errorf("error when listening to config changes: %v", err)
			if !ok {
				return
			}
		}
	}
}
