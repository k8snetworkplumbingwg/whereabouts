package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/go-co-op/gocron/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"

	nadclient "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned"
	nadinformers "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/informers/externalversions"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/controlloop"
	wbclient "github.com/k8snetworkplumbingwg/whereabouts/pkg/generated/clientset/versioned"
	wbinformers "github.com/k8snetworkplumbingwg/whereabouts/pkg/generated/informers/externalversions"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/reconciler"
)

const (
	allNamespaces               = ""
	controllerName              = "pod-ip-controlloop"
	reconcilerCronConfiguration = "/cron-schedule/config"
	reconcilerLeaderLeaseName   = "whereabouts-reconciler-lock"
	defaultWhereaboutsNamespace = "kube-system"
)

const (
	_ int = iota
	couldNotCreateController
	couldNotInitializeReconcilerLeaderElection
)

const (
	defaultLogLevel               = "debug"
	reconcilerLeaderLeaseDuration = 15 * time.Second
	reconcilerLeaderRenewDeadline = 10 * time.Second
	reconcilerLeaderRetryPeriod   = 2 * time.Second
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

	leaderElectionCtx, cancelLeaderElection := context.WithCancel(context.Background())
	defer cancelLeaderElection()
	go runReconcilerLeaderElectionLoop(leaderElectionCtx, errorChan)

	for {
		select {
		case <-stopChan:
			logging.Verbosef("shutting down reconciler")
			cancelLeaderElection()
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

func runReconcilerLeaderElectionLoop(ctx context.Context, errorChan chan error) {
	namespace := whereaboutsNamespace()
	identity := reconcilerLeaderIdentity()

	cfg, err := rest.InClusterConfig()
	if err != nil {
		_ = logging.Errorf("failed to generate in-cluster config for reconciler leader election: %v", err)
		os.Exit(couldNotInitializeReconcilerLeaderElection)
	}

	k8sClientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		_ = logging.Errorf("failed to create kubernetes client for reconciler leader election: %v", err)
		os.Exit(couldNotInitializeReconcilerLeaderElection)
	}

	electionCtx, cancelElection := context.WithCancel(ctx)
	defer cancelElection()

	err = runReconcilerLeaderElection(
		electionCtx,
		k8sClientSet,
		namespace,
		identity,
		errorChan,
		cancelElection,
	)
	if err != nil {
		errorChan <- err
	}
}

func runReconcilerLeaderElection(
	ctx context.Context,
	k8sClientSet kubernetes.Interface,
	namespace string,
	identity string,
	errorChan chan error,
	cancelElection context.CancelFunc,
) error {
	leaseLock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      reconcilerLeaderLeaseName,
			Namespace: namespace,
		},
		Client: k8sClientSet.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: identity,
		},
	}

	leaderElector, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:            leaseLock,
		LeaseDuration:   reconcilerLeaderLeaseDuration,
		RenewDeadline:   reconcilerLeaderRenewDeadline,
		RetryPeriod:     reconcilerLeaderRetryPeriod,
		ReleaseOnCancel: true,
		Name:            reconcilerLeaderLeaseName,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(leadingCtx context.Context) {
				logging.Verbosef("acquired reconciler leadership (%s/%s) as %q", namespace, reconcilerLeaderLeaseName, identity)
				if err := runScheduledReconciler(leadingCtx, errorChan); err != nil {
					errorChan <- err
					cancelElection()
				}
			},
			OnStoppedLeading: func() {
				logging.Verbosef("lost reconciler leadership (%s/%s)", namespace, reconcilerLeaderLeaseName)
			},
			OnNewLeader: func(currentLeader string) {
				if currentLeader == identity {
					logging.Verbosef("this pod is reconciler leader: %q", currentLeader)
					return
				}
				logging.Verbosef("reconciler leader is now: %q", currentLeader)
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create reconciler leader elector: %w", err)
	}

	logging.Verbosef("starting reconciler leader election (%s/%s) with identity %q", namespace, reconcilerLeaderLeaseName, identity)
	leaderElector.Run(ctx)
	return nil
}

func runScheduledReconciler(ctx context.Context, errorChan chan error) error {
	scheduler, err := gocron.NewScheduler(gocron.WithLocation(time.UTC))
	if err != nil {
		return fmt.Errorf("failed to create reconciler cron scheduler: %w", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		if shutdownErr := scheduler.Shutdown(); shutdownErr != nil {
			_ = logging.Errorf("failed to shutdown reconciler scheduler: %v", shutdownErr)
		}
		return fmt.Errorf("error creating reconciler configuration watcher: %w", err)
	}
	defer func() {
		if closeErr := watcher.Close(); closeErr != nil {
			_ = logging.Errorf("error closing reconciler configuration watcher: %v", closeErr)
		}
	}()

	reconcilerConfigWatcher, err := reconciler.NewConfigWatcher(
		reconcilerCronConfiguration,
		scheduler,
		watcher,
		func() {
			reconciler.ReconcileIPs(errorChan)
		},
	)
	if err != nil {
		if shutdownErr := scheduler.Shutdown(); shutdownErr != nil {
			_ = logging.Errorf("failed to shutdown reconciler scheduler: %v", shutdownErr)
		}
		return fmt.Errorf("could not create reconciler config watcher: %w", err)
	}

	scheduler.Start()
	const reconcilerConfigMntFile = "/cron-schedule/..data"
	reconcilerConfigWatcher.SyncConfiguration(func(event fsnotify.Event) bool {
		return event.Name == reconcilerConfigMntFile && event.Op&fsnotify.Create == fsnotify.Create
	})

	logging.Verbosef("scheduled reconciler started")
	<-ctx.Done()
	logging.Verbosef("scheduled reconciler stopping")

	if err := scheduler.Shutdown(); err != nil {
		_ = logging.Errorf("error shutting reconciler scheduler: %v", err)
	}

	return nil
}

func whereaboutsNamespace() string {
	if namespace, found := os.LookupEnv("WHEREABOUTS_NAMESPACE"); found && namespace != "" {
		return namespace
	}
	return defaultWhereaboutsNamespace
}

func reconcilerLeaderIdentity() string {
	if podName, found := os.LookupEnv("POD_NAME"); found && podName != "" {
		return podName
	}
	hostname, err := os.Hostname()
	if err == nil && hostname != "" {
		return hostname
	}
	if nodeName, found := os.LookupEnv("NODENAME"); found && nodeName != "" {
		return fmt.Sprintf("%s-%d", nodeName, os.Getpid())
	}
	return fmt.Sprintf("%s-%d", reconcilerLeaderLeaseName, os.Getpid())
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
