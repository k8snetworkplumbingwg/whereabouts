package reconciler

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"

	"github.com/fsnotify/fsnotify"
	"github.com/go-co-op/gocron/v2"
)

var _ = Describe("Reconciler configuration watcher", func() {
	var (
		config      *ConfigWatcher
		configDir   string
		dummyConfig *os.File
		mailbox     chan struct{}
		watcher     *fsnotify.Watcher
	)

	BeforeEach(func() {
		var err error

		mailbox = make(chan struct{})

		configDir, err = os.MkdirTemp("", "config")
		Expect(err).NotTo(HaveOccurred())
		const (
			initialCronWithSeconds = "0/1 2 3 * * *"
			dummyFileName          = "DUMMY"
		)
		dummyConfig, err = os.Create(filepath.Join(configDir, filepath.Base(dummyFileName)))
		Expect(err).NotTo(HaveOccurred())

		Expect(dummyConfig.Write([]byte(initialCronWithSeconds))).To(Equal(len(initialCronWithSeconds)))
		scheduler, err := gocron.NewScheduler()
		Expect(err).NotTo(HaveOccurred())
		watcher, err = fsnotify.NewWatcher()
		Expect(err).NotTo(HaveOccurred())
		config, err = newConfigWatcherForTests(
			dummyConfig.Name(),
			scheduler,
			watcher,
			func() { mailbox <- struct{}{} },
		)
		scheduler.Start()
		Expect(err).NotTo(HaveOccurred())
		config.SyncConfiguration(func(event fsnotify.Event) bool {
			return event.Name == dummyConfig.Name() && event.Op&fsnotify.Write == fsnotify.Write
		})
	})

	AfterEach(func() {
		watcher.Close()
		dummyConfig.Close()
	})

	When("the cron job expression is updated in the file-system", func() {
		const updatedCronWithSeconds = "0/1 * * * * *"

		BeforeEach(func() {
			Expect(dummyConfig.WriteAt([]byte(updatedCronWithSeconds), 0)).To(Equal(len(updatedCronWithSeconds)))
		})

		It("the current schedule is updated, and the handler function executed", func() {
			Eventually(func() string { return config.currentSchedule }).Should(Equal(updatedCronWithSeconds))
			Eventually(mailbox).WithTimeout(time.Minute).Should(Receive())
		})
	})
})

var _ = Describe("ReconcilerConfigEventPredicate", func() {
	const configPath = "/cron-schedule/config"
	var predicate func(fsnotify.Event) bool

	BeforeEach(func() {
		predicate = ReconcilerConfigEventPredicate(configPath)
	})

	table.DescribeTable("event relevance",
		func(event fsnotify.Event, expected bool) {
			Expect(predicate(event)).To(Equal(expected))
		},
		table.Entry("CREATE on ..data (ConfigMap atomic swap)", fsnotify.Event{
			Name: "/cron-schedule/..data",
			Op:   fsnotify.Create,
		}, true),
		table.Entry("CREATE on config file (first-time projection)", fsnotify.Event{
			Name: configPath,
			Op:   fsnotify.Create,
		}, true),
		table.Entry("CREATE on unrelated path", fsnotify.Event{
			Name: "/cron-schedule/..data_tmp",
			Op:   fsnotify.Create,
		}, false),
		table.Entry("WRITE on ..data", fsnotify.Event{
			Name: "/cron-schedule/..data",
			Op:   fsnotify.Write,
		}, false),
		table.Entry("WRITE on config file", fsnotify.Event{
			Name: configPath,
			Op:   fsnotify.Write,
		}, false),
		table.Entry("REMOVE on config file", fsnotify.Event{
			Name: configPath,
			Op:   fsnotify.Remove,
		}, false),
	)
})

var _ = Describe("ConfigMap first-projection race", func() {
	// Reproduces kubelet's optional ConfigMap mount sequence:
	// CREATE ..data (payload ready) before the user-visible config symlink exists.
	// Watching only ..data races: the read of config fails and the later CREATE
	// on config was previously ignored, leaving the default schedule stuck.
	const (
		initialCron = "30 4 * * *"
		updatedCron = "* * * * *"
	)

	var (
		config     *ConfigWatcher
		mountDir   string
		configPath string
		scheduler  gocron.Scheduler
		watcher    *fsnotify.Watcher
	)

	BeforeEach(func() {
		var err error
		mountDir, err = os.MkdirTemp("", "cron-schedule")
		Expect(err).NotTo(HaveOccurred())
		configPath = filepath.Join(mountDir, "config")

		// Start with the flatfile/default schedule already in place so
		// NewConfigWatcher succeeds (same expression the race falls back to).
		Expect(os.WriteFile(configPath, []byte(initialCron), 0o644)).To(Succeed())

		scheduler, err = gocron.NewScheduler()
		Expect(err).NotTo(HaveOccurred())
		watcher, err = fsnotify.NewWatcher()
		Expect(err).NotTo(HaveOccurred())

		config, err = newConfigWatcher(
			configPath,
			scheduler,
			watcher,
			func(schedule string) gocron.JobDefinition {
				return gocron.CronJob(schedule, false)
			},
			func() {},
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(config.currentSchedule).To(Equal(initialCron))
		scheduler.Start()
	})

	AfterEach(func() {
		_ = scheduler.Shutdown()
		_ = watcher.Close()
		_ = os.RemoveAll(mountDir)
	})

	simulateKubeletFirstProjection := func() {
		timestampedDir := filepath.Join(mountDir, "..2026_01_01_00_00_00.1")
		Expect(os.Mkdir(timestampedDir, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(timestampedDir, "config"), []byte(updatedCron), 0o644)).To(Succeed())

		// Drop the previous regular file so configPath is missing when ..data appears.
		Expect(os.Remove(configPath)).To(Succeed())

		// 1) atomic writer swaps ..data while config symlink does not exist yet
		Expect(os.Symlink(filepath.Base(timestampedDir), filepath.Join(mountDir, "..data"))).To(Succeed())

		// Wait until the watcher has observed ..data *before* the config symlink
		// exists. Without a flatfile the read fails and the schedule is unchanged;
		// that is the race window the fix must recover from.
		Consistently(func() string { return config.currentSchedule }).
			WithTimeout(300 * time.Millisecond).
			WithPolling(50 * time.Millisecond).
			Should(Equal(initialCron))
		Expect(configPath).NotTo(BeAnExistingFile())

		// 2) kubelet then creates the user-visible config symlink
		Expect(os.Symlink(filepath.Join("..data", "config"), configPath)).To(Succeed())
	}

	It("updates the schedule when ..data is created before the config symlink", func() {
		config.SyncConfiguration(ReconcilerConfigEventPredicate(configPath))
		// Allow the watcher goroutine to subscribe before emitting filesystem events.
		time.Sleep(100 * time.Millisecond)

		simulateKubeletFirstProjection()

		Eventually(func() string { return config.currentSchedule }).
			WithTimeout(5 * time.Second).
			WithPolling(50 * time.Millisecond).
			Should(Equal(updatedCron))
	})

	It("never adopts the new schedule when only ..data CREATE is watched (pre-fix)", func() {
		dataDirFile := filepath.Join(mountDir, "..data")
		config.SyncConfiguration(func(event fsnotify.Event) bool {
			return event.Name == dataDirFile && event.Op&fsnotify.Create == fsnotify.Create
		})
		time.Sleep(100 * time.Millisecond)

		simulateKubeletFirstProjection()

		// ..data CREATE races ahead of the config symlink; the later CREATE on
		// config is ignored, so the updated expression is never applied.
		Consistently(func() string { return config.currentSchedule }).
			WithTimeout(2 * time.Second).
			WithPolling(50 * time.Millisecond).
			Should(Equal(initialCron))
	})
})

func newConfigWatcherForTests(configPath string, scheduler gocron.Scheduler, configWatcher *fsnotify.Watcher, handlerFunc func()) (*ConfigWatcher, error) {
	return newConfigWatcher(
		configPath,
		scheduler,
		configWatcher,
		func(schedule string) gocron.JobDefinition {
			return gocron.CronJob(schedule, true)
		},
		handlerFunc,
	)
}
