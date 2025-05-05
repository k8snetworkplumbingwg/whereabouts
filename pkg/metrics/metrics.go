package metrics

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/pprof"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	utilwait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
)

// inspired by metrics server implementation @https://github.com/ovn-kubernetes/ovn-kubernetes/go-controller/pk/metrics/metrics.go

// stringFlagSetterFunc is a func used for setting string type flag.
type stringFlagSetterFunc func(string) (string, error)

// klogSetter is a setter to set klog level.
func klogSetter(val string) (string, error) {
	var level klog.Level
	if err := level.Set(val); err != nil {
		return "", fmt.Errorf("failed set klog.logging.verbosity %s: %v", val, err)
	}
	return fmt.Sprintf("successfully set klog.logging.verbosity to %s", val), nil
}

// stringFlagPutHandler wraps an http Handler to set string type flag.
func stringFlagPutHandler(setter stringFlagSetterFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == "PUT":
			body, err := io.ReadAll(req.Body)
			if err != nil {
				writePlainText(http.StatusBadRequest, "error reading request body: "+err.Error(), w)
				return
			}
			defer req.Body.Close()
			response, err := setter(string(body))
			if err != nil {
				writePlainText(http.StatusBadRequest, err.Error(), w)
				return
			}
			writePlainText(http.StatusOK, response, w)
			return
		default:
			writePlainText(http.StatusNotAcceptable, "unsupported http method", w)
			return
		}
	})
}

// writePlainText renders a simple string response.
func writePlainText(statusCode int, text string, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(statusCode)
	fmt.Fprintln(w, text)
}

// using the cyrpto/tls module's GetCertificate() callback function helps in picking up
// the latest certificate (due to cert rotation on cert expiry)
func getTLSServer(addr, certFile, privKeyFile string, handler http.Handler) *http.Server {
	tlsConfig := &tls.Config{
		GetCertificate: func(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
			cert, err := tls.LoadX509KeyPair(certFile, privKeyFile)
			if err != nil {
				return nil, fmt.Errorf("error generating x509 certs for metrics TLS endpoint: %v", err)
			}
			return &cert, nil
		},
	}
	server := &http.Server{
		Addr:      addr,
		Handler:   handler,
		TLSConfig: tlsConfig,
	}
	return server
}

func StartMetricsServer(bindAddress string, certFile string, keyfile string, stopChan <-chan struct{}, wg *sync.WaitGroup) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	// Allow changes to log level at runtime
	mux.HandleFunc("/debug/flags/v", stringFlagPutHandler(klogSetter))

	startMetricsServer(bindAddress, certFile, keyfile, mux, stopChan, wg)
}

func startMetricsServer(bindAddress, certFile, keyFile string, handler http.Handler, stopChan <-chan struct{}, wg *sync.WaitGroup) {
	var server *http.Server
	wg.Add(1)
	go func() {
		defer wg.Done()
		utilwait.Until(func() {
			klog.Infof("Starting metrics server at address %q", bindAddress)
			var listenAndServe func() error
			if certFile != "" && keyFile != "" {
				server = getTLSServer(bindAddress, certFile, keyFile, handler)
				listenAndServe = func() error { return server.ListenAndServeTLS("", "") }
			} else {
				server = &http.Server{Addr: bindAddress, Handler: handler}
				listenAndServe = func() error { return server.ListenAndServe() }
			}

			errCh := make(chan error)
			go func() {
				errCh <- listenAndServe()
			}()
			var err error
			select {
			case err = <-errCh:
				err = fmt.Errorf("failed while running metrics server at address %q: %w", bindAddress, err)
				utilruntime.HandleError(err)
			case <-stopChan:
				klog.Infof("Stopping metrics server at address %q", bindAddress)
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := server.Shutdown(shutdownCtx); err != nil {
					klog.Errorf("Error stopping metrics server at address %q: %v", bindAddress, err)
				}
			}
		}, 5*time.Second, stopChan)
	}()
}
