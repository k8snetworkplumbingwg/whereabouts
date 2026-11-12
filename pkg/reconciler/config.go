package reconciler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/go-co-op/gocron/v2"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/config"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

type ConfigWatcher struct {
	configDir       string
	configPath      string
	currentSchedule string
	job             gocron.Job
	scheduler       gocron.Scheduler
	handlerFunc     func()
	jobFactoryFunc  func(string) gocron.JobDefinition
	watcher         *fsnotify.Watcher
}

func NewConfigWatcher(configPath string, scheduler gocron.Scheduler, configWatcher *fsnotify.Watcher, handlerFunc func()) (*ConfigWatcher, error) {
	return newConfigWatcher(
		configPath,
		scheduler,
		configWatcher,
		func(schedule string) gocron.JobDefinition {
			return gocron.CronJob(schedule, false)
		},
		handlerFunc,
	)
}

func newConfigWatcher(
	configPath string,
	scheduler gocron.Scheduler,
	configWatcher *fsnotify.Watcher,
	cronJobFactoryFunc func(string) gocron.JobDefinition,
	handlerFunc func(),
) (*ConfigWatcher, error) {
	schedule, err := determineCronExpression(configPath)
	if err != nil {
		return nil, err
	}

	job, err := scheduler.NewJob(
		cronJobFactoryFunc(schedule),
		gocron.NewTask(handlerFunc),
	)
	if err != nil {
		return nil, fmt.Errorf("could not create reconciler job for cron expression %q from %q: %w", schedule, configPath, err)
	}

	return &ConfigWatcher{
		configDir:       filepath.Dir(configPath),
		configPath:      configPath,
		currentSchedule: schedule,
		job:             job,
		scheduler:       scheduler,
		watcher:         configWatcher,
		handlerFunc:     handlerFunc,
		jobFactoryFunc:  cronJobFactoryFunc,
	}, nil
}

// ReconcilerConfigEventPredicate matches fsnotify events that mean the
// reconciler cron config may have changed:
//   - CREATE on "..data": ConfigMap atomic writer swapped the projected payload
//     (repeat updates when the key was already present)
//   - CREATE on configPath: first-time projection of the key; kubelet creates
//     this symlink *after* swapping "..data", so watching "..data" alone can
//     race, read before the symlink exists, and miss the update
func ReconcilerConfigEventPredicate(configPath string) func(fsnotify.Event) bool {
	dataDirFile := filepath.Join(filepath.Dir(configPath), "..data")
	return func(event fsnotify.Event) bool {
		if event.Op&fsnotify.Create != fsnotify.Create {
			return false
		}
		return event.Name == dataDirFile || event.Name == configPath
	}
}

func determineCronExpression(configPath string) (string, error) {
	// We read the expression from a file if present, otherwise we use ReconcilerCronExpression
	fileContents, err := os.ReadFile(configPath)
	if err != nil {
		flatipam, _, flatErr := config.GetFlatIPAM(true, &types.IPAMConfig{}, "")
		if flatErr != nil {
			return "", logging.Errorf("could not get flatipam config: %v", flatErr)
		}

		logging.Verbosef("notice: could not read file: %v, defaulting to expression from configuration: %v", err, flatipam.IPAM.ReconcilerCronExpression)
		return flatipam.IPAM.ReconcilerCronExpression, nil
	}
	logging.Verbosef("using expression: %v", strings.TrimSpace(string(fileContents))) // do i need to trim spaces? idk i think the file would JUST be the expression?
	return strings.TrimSpace(string(fileContents)), nil
}

func (c *ConfigWatcher) SyncConfiguration(relevantEventPredicate func(event fsnotify.Event) bool) {
	go c.syncConfig(relevantEventPredicate)
	if err := c.watcher.Add(c.configDir); err != nil {
		_ = logging.Errorf("error adding watcher to config %q: %v", c.configPath, err)
	}
}

func (c *ConfigWatcher) syncConfig(relevantEventPredicate func(event fsnotify.Event) bool) {
	for {
		select {
		case event, ok := <-c.watcher.Events:
			if !ok {
				return
			}

			if !relevantEventPredicate(event) {
				logging.Debugf("event not relevant: %v", event)
				continue
			}
			updatedSchedule, err := determineCronExpression(c.configPath)
			if err != nil {
				_ = logging.Errorf("error determining cron expression from %q: %v", c.configPath, err)
				continue
			}
			logging.Verbosef(
				"configuration updated to file %q. New cron expression: %s",
				event.Name,
				updatedSchedule,
			)

			if updatedSchedule == c.currentSchedule {
				logging.Debugf("no changes in schedule, nothing to do.")
				continue
			}
			// Update removes the old job before validating its replacement.
			// Register the replacement first, but hold its task until the old
			// job removal is accepted. Abort waiting tasks if rollback is needed.
			ready := make(chan struct{})
			abort := make(chan struct{})
			updatedJob, err := c.scheduler.NewJob(
				c.jobFactoryFunc(updatedSchedule),
				gocron.NewTask(func(ctx context.Context) {
					select {
					case <-ready:
						if ctx.Err() == nil {
							c.handlerFunc()
						}
					case <-ctx.Done():
					case <-abort:
					}
				}),
				gocron.WithName(c.job.Name()),
			)
			if err != nil {
				_ = logging.Errorf(
					"failed to apply cron expression %q from %q; keeping previous schedule %q: %v",
					updatedSchedule, c.configPath, c.currentSchedule, err,
				)
				continue
			}
			if err := c.scheduler.RemoveJob(c.job.ID()); err != nil {
				close(abort)
				_ = logging.Errorf("error removing previous job %q: %v", c.job.ID().String(), err)
				if rollbackErr := c.scheduler.RemoveJob(updatedJob.ID()); rollbackErr != nil {
					_ = logging.Errorf("error removing replacement job %q: %v", updatedJob.ID().String(), rollbackErr)
				}
				continue
			}
			c.job = updatedJob
			c.currentSchedule = updatedSchedule
			close(ready)
			logging.Verbosef(
				"successfully updated CRON configuration id %q - new cron expression: %s",
				updatedJob.ID().String(),
				updatedSchedule,
			)
		case err, ok := <-c.watcher.Errors:
			_ = logging.Errorf("error when listening to config changes: %v", err)
			if !ok {
				return
			}
		}
	}
}
