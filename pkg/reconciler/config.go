package reconciler

import (
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
		return nil, fmt.Errorf("error creating job: %v", err)
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

func determineCronExpression(configPath string) (string, error) {
	// We read the expression from a file if present, otherwise we use ReconcilerCronExpression
	fileContents, err := os.ReadFile(configPath)
	if err != nil {
		flatipam, _, err := config.GetFlatIPAM(true, &types.IPAMConfig{}, "")
		if err != nil {
			return "", logging.Errorf("could not get flatipam config: %v", err)
		}

		_ = logging.Errorf("could not read file: %v, using expression from flatfile: %v", err, flatipam.IPAM.ReconcilerCronExpression)
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
			updatedJob, err := c.scheduler.Update(
				c.job.ID(),
				c.jobFactoryFunc(updatedSchedule),
				gocron.NewTask(c.handlerFunc),
			)
			if err != nil {
				_ = logging.Errorf("error updating job %q configuration: %v", c.job.ID().String(), err)
			}
			c.currentSchedule = updatedSchedule
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
