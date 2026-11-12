package reconciler

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
)

type replacementScheduler struct {
	gocron.Scheduler
	remove func(uuid.UUID) error
}

func (s *replacementScheduler) RemoveJob(id uuid.UUID) error { return s.remove(id) }

func TestConfigWatcherJobHandover(t *testing.T) {
	for _, test := range []struct {
		name         string
		failRemoval  bool
		failRollback bool
	}{
		{name: "commit"},
		{name: "rollback", failRemoval: true},
		{name: "rollback removal error", failRemoval: true, failRollback: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			started, finished := make(chan uuid.UUID, 8), make(chan uuid.UUID, 8)
			base, err := gocron.NewScheduler(gocron.WithGlobalJobOptions(gocron.WithEventListeners(
				gocron.BeforeJobRuns(func(id uuid.UUID, _ string) { started <- id }),
				gocron.AfterJobRuns(func(id uuid.UUID, _ string) { finished <- id }),
			)))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = base.Shutdown() })
			scheduler := &replacementScheduler{Scheduler: base}
			path := filepath.Join(t.TempDir(), "config")
			if err := os.WriteFile(path, []byte("@every 1h"), 0o600); err != nil {
				t.Fatal(err)
			}
			watcher, err := fsnotify.NewWatcher()
			if err != nil {
				t.Fatal(err)
			}
			calls := make(chan struct{}, 8)
			config, err := NewConfigWatcher(path, scheduler, watcher, func() { calls <- struct{}{} })
			if err != nil {
				_ = watcher.Close()
				t.Fatal(err)
			}
			originalID := config.job.ID()
			entered, release := make(chan gocron.Job, 1), make(chan struct{})
			failRemoval := test.failRemoval
			scheduler.remove = func(id uuid.UUID) error {
				if id == originalID {
					for _, job := range base.Jobs() {
						if job.ID() != originalID {
							entered <- job
							break
						}
					}
					<-release
					if failRemoval {
						return errors.New("test: cannot remove original job")
					}
				} else if test.failRollback {
					return errors.New("test: cannot remove candidate job")
				}
				return base.RemoveJob(id)
			}
			base.Start()
			syncReload := startReloadLoop(t, config, watcher)
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			if err := os.WriteFile(path, []byte("@every 2h"), 0o600); err != nil {
				t.Fatal(err)
			}
			watcher.Events <- fsnotify.Event{Name: path, Op: fsnotify.Create}
			var candidate gocron.Job
			select {
			case candidate = <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("original job removal was not attempted")
			}
			if err := candidate.RunNow(); err != nil {
				t.Fatal(err)
			}
			select {
			case id := <-started:
				if id != candidate.ID() {
					t.Fatal("unexpected job started")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("candidate did not enter execution")
			}
			select {
			case <-finished:
				t.Fatal("candidate finished before job handover")
			case <-time.After(50 * time.Millisecond):
			}
			unblock()
			syncReload()
			select {
			case id := <-finished:
				if id != candidate.ID() {
					t.Fatal("unexpected job finished")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("candidate remained blocked after handover")
			}
			if test.failRemoval {
				select {
				case <-calls:
					t.Fatal("rolled-back candidate called the handler")
				default:
				}
				if config.job.ID() != originalID || config.currentSchedule != "@every 1h" {
					t.Fatal("rollback changed the active job")
				}
				if test.failRollback {
					if err := base.RemoveJob(candidate.ID()); err != nil {
						t.Fatal(err)
					}
				}
				if len(base.Jobs()) != 1 {
					t.Fatal("rollback did not retain exactly the original job")
				}
				failRemoval = false
				watcher.Events <- fsnotify.Event{Name: path, Op: fsnotify.Create}
				syncReload()
			} else {
				select {
				case <-calls:
				default:
					t.Fatal("committed candidate did not call the handler")
				}
			}
			if jobs := base.Jobs(); len(jobs) != 1 || jobs[0].ID() != config.job.ID() || config.currentSchedule != "@every 2h" {
				t.Fatal("valid reload did not commit exactly the replacement job")
			}
			if test.failRemoval {
				if err := config.job.RunNow(); err != nil {
					t.Fatal(err)
				}
				select {
				case <-calls:
				case <-time.After(5 * time.Second):
					t.Fatal("replacement remained blocked after retry")
				}
			}
		})
	}
}
