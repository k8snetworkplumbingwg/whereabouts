// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
//
// SPDX-License-Identifier: Apache-2.0

// install-cni is a standalone binary that replaces the shell-based CNI
// installer (install-cni.sh, token-watcher.sh, lib.sh).  It is executed
// as the DaemonSet entry-point and performs three tasks:
//
//  1. Copy the whereabouts CNI binary to the host CNI bin directory.
//  2. Generate a kubeconfig and whereabouts.conf on the host CNI conf
//     directory so that the CNI plugin can talk to the API server.
//  3. Watch the ServiceAccount token (and optionally the CA bundle) for
//     changes and regenerate the kubeconfig when they rotate.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Environment variable names with their defaults.
const (
	defaultCNIBinDir          = "/host/opt/cni/bin/"
	defaultCNIConfDir         = "/host/etc/cni/net.d"
	defaultReconcilerCron     = "30 4 * * *"
	defaultKubeConfigMode     = 0o600
	defaultServiceAccountPath = "/var/run/secrets/kubernetes.io/serviceaccount"
	defaultSkipTLSVerify      = "false"
	defaultKubeProtocol       = "https"

	// How often the fallback polling loop checks for token/CA changes,
	// in case fsnotify misses an event (e.g. atomic symlink swap).
	defaultPollInterval = 5 * time.Minute
)

// pollInterval can be shortened in tests for faster assertions.
var pollInterval = defaultPollInterval

// config holds all runtime configuration, populated from environment
// variables with sensible defaults.
type config struct {
	CNIBinDir          string
	CNIBinSrc          string
	CNIConfDir         string
	ReconcilerCron     string
	ServiceAccountPath string
	KubeCAFile         string
	SkipTLSVerify      bool
	KubeProtocol       string
	KubeHost           string
	KubePort           string
	Namespace          string
}

func loadConfig() (*config, error) {
	saPath := envOr("SERVICE_ACCOUNT_PATH", defaultServiceAccountPath)

	c := &config{
		CNIBinDir:          envOr("CNI_BIN_DIR", defaultCNIBinDir),
		CNIBinSrc:          envOr("CNI_BIN_SRC", "/whereabouts"),
		CNIConfDir:         envOr("CNI_CONF_DIR", defaultCNIConfDir),
		ReconcilerCron:     envOr("WHEREABOUTS_RECONCILER_CRON", defaultReconcilerCron),
		ServiceAccountPath: saPath,
		KubeCAFile:         envOr("KUBE_CA_FILE", filepath.Join(saPath, "ca.crt")),
		SkipTLSVerify:      envOr("SKIP_TLS_VERIFY", defaultSkipTLSVerify) == "true",
		KubeProtocol:       envOr("KUBERNETES_SERVICE_PROTOCOL", defaultKubeProtocol),
		KubeHost:           os.Getenv("KUBERNETES_SERVICE_HOST"),
		KubePort:           os.Getenv("KUBERNETES_SERVICE_PORT"),
		Namespace:          os.Getenv("WHEREABOUTS_NAMESPACE"),
	}
	if c.KubeHost == "" {
		return nil, fmt.Errorf("KUBERNETES_SERVICE_HOST not set")
	}
	if c.KubePort == "" {
		return nil, fmt.Errorf("KUBERNETES_SERVICE_PORT not set")
	}
	return c, nil
}

// tokenPath returns the path to the projected ServiceAccount token.
func (c *config) tokenPath() string {
	return filepath.Join(c.ServiceAccountPath, "token")
}

// confDir returns the whereabouts.d directory on the host.
func (c *config) confDir() string {
	return filepath.Join(c.CNIConfDir, "whereabouts.d")
}

// kubeconfigPath returns the full path for the generated kubeconfig.
func (c *config) kubeconfigPath() string {
	return filepath.Join(c.confDir(), "whereabouts.kubeconfig")
}

// kubeconfigLiteral returns the kubeconfig path as seen from the host
// (stripping the /host mount prefix).
func (c *config) kubeconfigLiteral() string {
	return strings.Replace(c.kubeconfigPath(), "/host", "", 1)
}

// whereaboutsConfPath returns the full path for the whereabouts.conf file.
func (c *config) whereaboutsConfPath() string {
	return filepath.Join(c.confDir(), "whereabouts.conf")
}

// wrappedHost returns the Kubernetes service host, wrapped in brackets
// for IPv6 addresses.
func (c *config) wrappedHost() string {
	h := c.KubeHost
	// Detect IPv6: contains a colon followed by a hex digit.
	if strings.ContainsAny(h, ":") {
		h = "[" + h + "]"
	}
	return h
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	err := run(ctx)
	cancel()

	if err != nil {
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

// run performs the full CNI installer lifecycle: config loading,
// kubeconfig + conf generation, binary copy, and credential watching.
// It returns an error on any setup failure so the container restarts.
func run(ctx context.Context) error {
	slog.Info("starting CNI installer")

	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}

	// 1. Create the whereabouts.d config directory on the host.
	if err := os.MkdirAll(cfg.confDir(), 0o755); err != nil {
		return fmt.Errorf("creating config directory %s: %w", cfg.confDir(), err)
	}

	// 2. Generate kubeconfig & whereabouts.conf.
	if err := writeKubeConfig(cfg); err != nil {
		return fmt.Errorf("writing kubeconfig: %w", err)
	}
	if err := writeWhereaboutsConf(cfg); err != nil {
		return fmt.Errorf("writing whereabouts.conf: %w", err)
	}

	// 3. Copy the whereabouts binary to the host CNI bin dir.
	if err := copyFile(cfg.CNIBinSrc, filepath.Join(cfg.CNIBinDir, "whereabouts")); err != nil {
		return fmt.Errorf("copying whereabouts binary: %w", err)
	}
	slog.Info("CNI installer setup complete")

	// 4. Watch for token/CA changes and regenerate kubeconfig.
	watchAndRegenerate(ctx, cfg)
	slog.Info("shutting down")
	return nil
}

// writeKubeConfig generates a kubeconfig file for the whereabouts CNI plugin.
func writeKubeConfig(cfg *config) error {
	tokenBytes, err := os.ReadFile(cfg.tokenPath())
	if err != nil {
		return fmt.Errorf("reading service account token: %w", err)
	}
	token := strings.TrimSpace(string(tokenBytes))

	cluster := clientcmdapi.Cluster{
		Server: fmt.Sprintf("%s://%s:%s", cfg.KubeProtocol, cfg.wrappedHost(), cfg.KubePort),
	}
	if cfg.SkipTLSVerify {
		cluster.InsecureSkipTLSVerify = true
	} else {
		caBytes, err := os.ReadFile(cfg.KubeCAFile)
		if err != nil {
			return fmt.Errorf("reading CA file %s: %w", cfg.KubeCAFile, err)
		}
		cluster.CertificateAuthorityData = caBytes
	}

	kubeconfig := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"local": &cluster,
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"whereabouts": {Token: token},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"whereabouts-context": {
				Cluster:   "local",
				AuthInfo:  "whereabouts",
				Namespace: cfg.Namespace,
			},
		},
		CurrentContext: "whereabouts-context",
	}

	data, err := clientcmd.Write(kubeconfig)
	if err != nil {
		return fmt.Errorf("serializing kubeconfig: %w", err)
	}

	path := cfg.kubeconfigPath()
	if err := os.WriteFile(path, data, defaultKubeConfigMode); err != nil {
		return fmt.Errorf("writing kubeconfig to %s: %w", path, err)
	}
	slog.Info("wrote kubeconfig", "path", path)
	return nil
}

// whereaboutsConf is the JSON schema for whereabouts.conf.
type whereaboutsConf struct {
	Datastore                string         `json:"datastore"`
	Kubernetes               kubernetesConf `json:"kubernetes"`
	ReconcilerCronExpression string         `json:"reconciler_cron_expression"`
}

type kubernetesConf struct {
	Kubeconfig string `json:"kubeconfig"`
}

// writeWhereaboutsConf writes the whereabouts.conf JSON configuration.
func writeWhereaboutsConf(cfg *config) error {
	conf := whereaboutsConf{
		Datastore: "kubernetes",
		Kubernetes: kubernetesConf{
			Kubeconfig: cfg.kubeconfigLiteral(),
		},
		ReconcilerCronExpression: cfg.ReconcilerCron,
	}

	data, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling whereabouts.conf: %w", err)
	}
	data = append(data, '\n')

	path := cfg.whereaboutsConfPath()
	if err := os.WriteFile(path, data, defaultKubeConfigMode); err != nil {
		return fmt.Errorf("writing whereabouts.conf to %s: %w", path, err)
	}
	slog.Info("wrote whereabouts.conf", "path", path)
	return nil
}

// copyFile copies src to dst, preserving the executable bit.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening %s: %w", src, err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copying %s to %s: %w", src, dst, err)
	}

	if err := out.Chmod(0o755); err != nil {
		return fmt.Errorf("chmod %s: %w", dst, err)
	}

	slog.Info("copied whereabouts binary", "src", src, "dst", dst)
	return nil
}

// watchAndRegenerate watches the service account token directory for
// changes using fsnotify, with a polling fallback. When a change is
// detected it regenerates the kubeconfig.
func watchAndRegenerate(ctx context.Context, cfg *config) {
	tokenHash := fileHash(cfg.tokenPath())
	caHash := fileHash(cfg.KubeCAFile)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Warn("fsnotify unavailable, falling back to polling only", "error", err)
		pollLoop(ctx, cfg, tokenHash, caHash)
		return
	}
	defer watcher.Close()

	// Watch the ServiceAccount directory (tokens are symlinked, so we
	// watch the directory rather than the file itself).
	if err := watcher.Add(cfg.ServiceAccountPath); err != nil {
		slog.Warn("cannot watch SA directory, falling back to polling", "error", err)
		pollLoop(ctx, cfg, tokenHash, caHash)
		return
	}
	slog.Info("watching for credential changes", "path", cfg.ServiceAccountPath)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			// Kubernetes projected volumes do an atomic symlink swap,
			// so we look for Create and Write events.
			if event.Op&(fsnotify.Create|fsnotify.Write) == 0 {
				continue
			}
			tokenHash, caHash = maybeRegenerate(cfg, tokenHash, caHash)

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			slog.Warn("fsnotify error", "error", err)

		case <-ticker.C:
			// Periodic fallback check in case fsnotify missed an event.
			tokenHash, caHash = maybeRegenerate(cfg, tokenHash, caHash)
		}
	}
}

// pollLoop is the pure-polling fallback when fsnotify is not available.
func pollLoop(ctx context.Context, cfg *config, tokenHash, caHash string) {
	slog.Info("using polling-only mode for credential changes", "interval", pollInterval)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tokenHash, caHash = maybeRegenerate(cfg, tokenHash, caHash)
		}
	}
}

// maybeRegenerate checks whether the token or CA file changed and, if
// so, regenerates the kubeconfig. Returns the updated hashes.
func maybeRegenerate(cfg *config, prevToken, prevCA string) (tokenHash, caHash string) {
	newToken := fileHash(cfg.tokenPath())
	newCA := fileHash(cfg.KubeCAFile)

	changed := newToken != prevToken
	if !cfg.SkipTLSVerify && newCA != prevCA {
		changed = true
	}

	if changed {
		slog.Info("detected credential change, regenerating kubeconfig")
		if err := writeKubeConfig(cfg); err != nil {
			slog.Error("failed to regenerate kubeconfig", "error", err)
			// Keep old hashes so we retry on next tick.
			return prevToken, prevCA
		}
	}
	return newToken, newCA
}

// fileHash returns the hex-encoded SHA-256 hash of a file, or empty
// string if the file cannot be read.
func fileHash(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// envOr returns the environment variable value, or the fallback if unset/empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
