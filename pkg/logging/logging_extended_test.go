package logging

import (
	"os"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These tests extend the existing logging_test.go to cover output verification,
// level filtering, Errorf return value, Panicf behavior, and thread safety.

var _ = Describe("logging output verification", func() {
	var (
		tmpFile *os.File
	)

	BeforeEach(func() {
		var err error
		tmpFile, err = os.CreateTemp("", "whereabouts-log-test-*.log")
		Expect(err).NotTo(HaveOccurred())

		// Reset logging state
		mu.Lock()
		loggingStderr = false
		loggingFp = nil
		loggingLevel = DebugLevel
		mu.Unlock()

		SetLogFile(tmpFile.Name())
	})

	AfterEach(func() {
		mu.Lock()
		if loggingFp != nil {
			loggingFp.Close()
			loggingFp = nil
		}
		mu.Unlock()
		os.Remove(tmpFile.Name())
	})

	readLog := func() string {
		// Close the current fp so writes are flushed
		mu.Lock()
		if loggingFp != nil {
			loggingFp.Close()
			loggingFp = nil
		}
		mu.Unlock()

		data, err := os.ReadFile(tmpFile.Name())
		Expect(err).NotTo(HaveOccurred())
		return string(data)
	}

	Describe("Printf", func() {
		It("writes output at the correct level", func() {
			Printf(DebugLevel, "hello %s", "world")
			content := readLog()
			Expect(content).To(ContainSubstring("[debug]"))
			Expect(content).To(ContainSubstring("hello world"))
		})

		It("includes a timestamp", func() {
			Printf(ErrorLevel, "test message")
			content := readLog()
			// RFC3339 starts with year
			Expect(content).To(MatchRegexp(`\d{4}-\d{2}-\d{2}T`))
		})

		It("suppresses output when level is below threshold", func() {
			mu.Lock()
			loggingLevel = ErrorLevel
			mu.Unlock()

			Printf(DebugLevel, "should not appear")
			content := readLog()
			Expect(content).To(BeEmpty())
		})

		It("writes output when level equals threshold", func() {
			mu.Lock()
			loggingLevel = ErrorLevel
			mu.Unlock()

			Printf(ErrorLevel, "should appear")
			content := readLog()
			Expect(content).To(ContainSubstring("should appear"))
		})
	})

	Describe("Debugf", func() {
		It("writes at debug level", func() {
			Debugf("debug msg %d", 42)
			content := readLog()
			Expect(content).To(ContainSubstring("[debug]"))
			Expect(content).To(ContainSubstring("debug msg 42"))
		})

		It("is suppressed when level is error", func() {
			mu.Lock()
			loggingLevel = ErrorLevel
			mu.Unlock()

			Debugf("should be hidden")
			content := readLog()
			Expect(content).To(BeEmpty())
		})
	})

	Describe("Verbosef", func() {
		It("writes at verbose level", func() {
			Verbosef("verbose msg")
			content := readLog()
			Expect(content).To(ContainSubstring("[verbose]"))
			Expect(content).To(ContainSubstring("verbose msg"))
		})
	})

	Describe("Errorf", func() {
		It("writes at error level and returns an error", func() {
			err := Errorf("something failed: %s", "reason")
			content := readLog()
			Expect(content).To(ContainSubstring("[error]"))
			Expect(content).To(ContainSubstring("something failed: reason"))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("something failed: reason"))
		})

		It("returns the formatted error", func() {
			err := Errorf("val=%d", 99)
			Expect(err.Error()).To(Equal("val=99"))
		})
	})

	Describe("Panicf", func() {
		It("panics with the formatted message", func() {
			Expect(func() {
				Panicf("fatal error: %s", "crash")
			}).To(PanicWith("fatal error: crash"))
		})

		It("writes stack trace to log", func() {
			func() {
				defer func() { recover() }()
				Panicf("panic test")
			}()
			content := readLog()
			Expect(content).To(ContainSubstring("Stack trace output"))
			Expect(content).To(ContainSubstring("panic test"))
		})
	})
})

var _ = Describe("Level.String", func() {
	It("returns correct strings for all levels", func() {
		Expect(PanicLevel.String()).To(Equal("panic"))
		Expect(ErrorLevel.String()).To(Equal("error"))
		Expect(VerboseLevel.String()).To(Equal("verbose"))
		Expect(DebugLevel.String()).To(Equal("debug"))
	})

	It("returns unknown for MaxLevel", func() {
		Expect(MaxLevel.String()).To(Equal("unknown"))
	})

	It("returns unknown for UnknownLevel", func() {
		Expect(UnknownLevel.String()).To(Equal("unknown"))
	})
})

var _ = Describe("GetLoggingLevel", func() {
	BeforeEach(func() {
		mu.Lock()
		loggingLevel = VerboseLevel
		mu.Unlock()
	})

	It("returns the current level", func() {
		Expect(GetLoggingLevel()).To(Equal(VerboseLevel))
	})
})

var _ = Describe("SetLogFile", func() {
	It("closes previously opened file when called again", func() {
		tmpFile1, err := os.CreateTemp("", "wb-log-1-*.log")
		Expect(err).NotTo(HaveOccurred())
		defer os.Remove(tmpFile1.Name())

		tmpFile2, err := os.CreateTemp("", "wb-log-2-*.log")
		Expect(err).NotTo(HaveOccurred())
		defer os.Remove(tmpFile2.Name())

		mu.Lock()
		loggingFp = nil
		mu.Unlock()

		SetLogFile(tmpFile1.Name())
		mu.Lock()
		Expect(loggingFp).NotTo(BeNil())
		fp1 := loggingFp
		mu.Unlock()

		SetLogFile(tmpFile2.Name())
		mu.Lock()
		Expect(loggingFp).NotTo(BeNil())
		Expect(loggingFp).NotTo(Equal(fp1))
		mu.Unlock()

		// Clean up
		mu.Lock()
		if loggingFp != nil {
			loggingFp.Close()
			loggingFp = nil
		}
		mu.Unlock()
	})
})

var _ = Describe("Thread safety", func() {
	It("handles concurrent writes without races", func() {
		tmpFile, err := os.CreateTemp("", "wb-log-concurrent-*.log")
		Expect(err).NotTo(HaveOccurred())
		defer os.Remove(tmpFile.Name())

		mu.Lock()
		loggingStderr = false
		loggingFp = nil
		loggingLevel = DebugLevel
		mu.Unlock()

		SetLogFile(tmpFile.Name())

		var wg sync.WaitGroup
		for i := range 100 {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				Debugf("concurrent write %d", n)
			}(i)
		}
		wg.Wait()

		// Close and read
		mu.Lock()
		if loggingFp != nil {
			loggingFp.Close()
			loggingFp = nil
		}
		mu.Unlock()

		data, err := os.ReadFile(tmpFile.Name())
		Expect(err).NotTo(HaveOccurred())
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		Expect(lines).To(HaveLen(100))
	})
})
