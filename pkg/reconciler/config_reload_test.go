package reconciler

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/go-co-op/gocron/v2"
)

func TestConfigWatcherPreservesJobOnInvalidSchedule(t *testing.T) {
	for _, test := range []struct {
		name     string
		schedule string
		seconds  bool
		custom   bool
	}{
		{name: "malformed", schedule: "not-a-cron"},
		{name: "empty", schedule: ""},
		{name: "unexpected seconds field", schedule: "* * * * * *"},
		{name: "no possible run", schedule: "0 0 31 2 *"},
		{name: "seconds enabled", schedule: "not-a-cron", seconds: true},
		{name: "scheduler cron implementation", schedule: "not-a-cron", custom: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			initialSchedule, updatedSchedule := "@every 1s", "@every 2s"
			options := []gocron.SchedulerOption{gocron.WithLocation(time.UTC)}
			if test.custom {
				initialSchedule, updatedSchedule = "custom:first", "custom:next"
				options = append(options, gocron.WithGlobalJobOptions(gocron.WithCronImplementation(reloadTestCron{})))
			}
			configPath := filepath.Join(t.TempDir(), "config")
			if err := os.WriteFile(configPath, []byte(initialSchedule), 0o600); err != nil {
				t.Fatal(err)
			}
			scheduler, err := gocron.NewScheduler(options...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = scheduler.Shutdown() })
			watcher, err := fsnotify.NewWatcher()
			if err != nil {
				t.Fatal(err)
			}
			runs := make(chan struct{}, 16)
			newWatcher := NewConfigWatcher
			if test.seconds {
				newWatcher = newConfigWatcherForTests
			}
			config, err := newWatcher(configPath, scheduler, watcher, func() {
				select {
				case runs <- struct{}{}:
				default:
				}
			})
			if err != nil {
				_ = watcher.Close()
				t.Fatal(err)
			}
			jobID := config.job.ID()
			jobName := config.job.Name()
			scheduler.Start()

			// Feed events directly so assertions wait for processing rather than
			// relying on filesystem delivery timing or reading watcher state concurrently.
			syncReload := startReloadLoop(t, config, watcher)

			reload := func(schedule string) {
				t.Helper()
				if err := os.WriteFile(configPath, []byte(schedule), 0o600); err != nil {
					t.Fatal(err)
				}
				watcher.Events <- fsnotify.Event{Name: configPath, Op: fsnotify.Create}
				syncReload()
			}
			assertJobRuns := func() {
				t.Helper()
				jobs := scheduler.Jobs()
				if len(jobs) != 1 || jobs[0].ID() != config.job.ID() {
					t.Fatalf("expected exactly the active job to remain registered, got %d jobs", len(jobs))
				}
				if jobs[0].Name() != jobName {
					t.Fatal("reload changed the configured job name")
				}
				if err := jobs[0].RunNow(); err != nil {
					t.Fatal(err)
				}
				select {
				case <-runs:
				case <-time.After(5 * time.Second):
					t.Fatal("registered job did not execute")
				}
			}

			reload(test.schedule)
			assertJobRuns()
			if config.currentSchedule != initialSchedule || config.job.ID() != jobID {
				t.Fatalf("invalid update changed the active schedule to %q", config.currentSchedule)
			}
			reload(initialSchedule)
			assertJobRuns()
			if config.job.ID() != jobID {
				t.Fatal("restoring the previous expression replaced the active job")
			}
			reload(updatedSchedule)
			assertJobRuns()
			if config.currentSchedule != updatedSchedule || config.job.ID() == jobID {
				t.Fatalf("valid update was not applied: %q", config.currentSchedule)
			}
			if !test.custom {
				previousID := config.job.ID()
				const zonedSchedule = "CRON_TZ=Europe/Berlin 0 9 * * *"
				reload(zonedSchedule)
				assertJobRuns()
				if config.currentSchedule != zonedSchedule || config.job.ID() == previousID {
					t.Fatal("five-field expression with timezone was not applied")
				}
			}
		})
	}
}

// This stateless implementation accepts expressions the default parser rejects.
type reloadTestCron struct{}

func (reloadTestCron) IsValid(expression string, _ *time.Location, _ time.Time) error {
	if expression != "custom:first" && expression != "custom:next" {
		return fmt.Errorf("invalid test expression: %s", expression)
	}
	return nil
}

func (reloadTestCron) Next(previous time.Time) time.Time { return previous.Add(time.Second) }

func startReloadLoop(t *testing.T, config *ConfigWatcher, watcher *fsnotify.Watcher) func() {
	t.Helper()
	processed := make(chan struct{}, 1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		config.syncConfig(func(event fsnotify.Event) bool {
			if event.Name == "barrier" {
				processed <- struct{}{}
				return false
			}
			return ReconcilerConfigEventPredicate(config.configPath)(event)
		})
	}()
	t.Cleanup(func() {
		_ = watcher.Close()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Error("configuration watcher did not stop")
		}
	})
	return func() {
		t.Helper()
		watcher.Events <- fsnotify.Event{Name: "barrier"}
		select {
		case <-processed:
		case <-time.After(5 * time.Second):
			t.Fatal("configuration reload did not finish")
		}
	}
}
