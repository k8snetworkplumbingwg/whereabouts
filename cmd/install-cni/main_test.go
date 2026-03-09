// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	clientcmd "k8s.io/client-go/tools/clientcmd"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newTestConfig returns a config pointing at temp directories, with the
// minimal SA files (token, optionally ca.crt) pre-created.  The caller
// can override fields after construction.
func newTestConfig(t *testing.T, withCA bool) *config {
	t.Helper()
	tmp := t.TempDir()
	saDir := filepath.Join(tmp, "sa")
	must(t, os.MkdirAll(saDir, 0o755))
	must(t, os.WriteFile(filepath.Join(saDir, "token"), []byte("test-token"), 0o600))
	if withCA {
		must(t, os.WriteFile(filepath.Join(saDir, "ca.crt"), []byte("fake-ca"), 0o600))
	}

	cniDir := filepath.Join(tmp, "cni")
	confDir := filepath.Join(cniDir, "whereabouts.d")
	must(t, os.MkdirAll(confDir, 0o755))

	binDir := filepath.Join(tmp, "bin")
	must(t, os.MkdirAll(binDir, 0o755))

	return &config{
		CNIBinDir:          binDir,
		CNIBinSrc:          "/whereabouts",
		CNIConfDir:         cniDir,
		ReconcilerCron:     defaultReconcilerCron,
		ServiceAccountPath: saDir,
		KubeCAFile:         filepath.Join(saDir, "ca.crt"),
		SkipTLSVerify:      !withCA,
		KubeProtocol:       "https",
		KubeHost:           "10.96.0.1",
		KubePort:           "443",
		Namespace:          "kube-system",
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// envOr
// ---------------------------------------------------------------------------

func TestEnvOr(t *testing.T) {
	t.Setenv("TEST_ENVOR_SET", "val")
	if got := envOr("TEST_ENVOR_SET", "fb"); got != "val" {
		t.Errorf("envOr(set) = %q, want %q", got, "val")
	}
	if got := envOr("TEST_ENVOR_UNSET", "fb"); got != "fb" {
		t.Errorf("envOr(unset) = %q, want %q", got, "fb")
	}
}

// ---------------------------------------------------------------------------
// loadConfig
// ---------------------------------------------------------------------------

func TestLoadConfig_Defaults(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.96.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	// Clear optional vars to assert defaults.
	t.Setenv("CNI_BIN_DIR", "")
	t.Setenv("CNI_CONF_DIR", "")
	t.Setenv("SKIP_TLS_VERIFY", "")
	t.Setenv("WHEREABOUTS_RECONCILER_CRON", "")
	t.Setenv("WHEREABOUTS_NAMESPACE", "")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "CNIBinDir", cfg.CNIBinDir, defaultCNIBinDir)
	assertEqual(t, "CNIConfDir", cfg.CNIConfDir, defaultCNIConfDir)
	assertEqual(t, "ReconcilerCron", cfg.ReconcilerCron, defaultReconcilerCron)
	assertEqual(t, "KubeProtocol", cfg.KubeProtocol, defaultKubeProtocol)
	if cfg.SkipTLSVerify {
		t.Error("SkipTLSVerify should default to false")
	}
}

func TestLoadConfig_CustomValues(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "192.168.1.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "6443")
	t.Setenv("CNI_BIN_DIR", "/custom/bin")
	t.Setenv("CNI_CONF_DIR", "/custom/conf")
	t.Setenv("SKIP_TLS_VERIFY", "true")
	t.Setenv("WHEREABOUTS_RECONCILER_CRON", "*/5 * * * *")
	t.Setenv("WHEREABOUTS_NAMESPACE", "my-ns")
	t.Setenv("KUBERNETES_SERVICE_PROTOCOL", "http")
	t.Setenv("SERVICE_ACCOUNT_PATH", "/custom/sa")
	t.Setenv("KUBE_CA_FILE", "/custom/ca.pem")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "CNIBinDir", cfg.CNIBinDir, "/custom/bin")
	assertEqual(t, "CNIConfDir", cfg.CNIConfDir, "/custom/conf")
	assertEqual(t, "ReconcilerCron", cfg.ReconcilerCron, "*/5 * * * *")
	assertEqual(t, "KubeProtocol", cfg.KubeProtocol, "http")
	assertEqual(t, "Namespace", cfg.Namespace, "my-ns")
	assertEqual(t, "ServiceAccountPath", cfg.ServiceAccountPath, "/custom/sa")
	assertEqual(t, "KubeCAFile", cfg.KubeCAFile, "/custom/ca.pem")
	if !cfg.SkipTLSVerify {
		t.Error("SkipTLSVerify should be true")
	}
}

func TestLoadConfig_MissingHost(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	if _, err := loadConfig(); err == nil {
		t.Fatal("expected error for missing KUBERNETES_SERVICE_HOST")
	}
}

func TestLoadConfig_MissingPort(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("expected error for missing KUBERNETES_SERVICE_PORT")
	}
}

// ---------------------------------------------------------------------------
// config path helpers
// ---------------------------------------------------------------------------

func TestConfigPaths(t *testing.T) {
	c := &config{
		CNIConfDir:         "/host/etc/cni/net.d",
		ServiceAccountPath: "/var/run/secrets/kubernetes.io/serviceaccount",
	}
	assertEqual(t, "tokenPath", c.tokenPath(), "/var/run/secrets/kubernetes.io/serviceaccount/token")
	assertEqual(t, "confDir", c.confDir(), "/host/etc/cni/net.d/whereabouts.d")
	assertEqual(t, "kubeconfigPath", c.kubeconfigPath(), "/host/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig")
	assertEqual(t, "kubeconfigLiteral", c.kubeconfigLiteral(), "/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig")
	assertEqual(t, "whereaboutsConfPath", c.whereaboutsConfPath(), "/host/etc/cni/net.d/whereabouts.d/whereabouts.conf")
}

// ---------------------------------------------------------------------------
// wrappedHost
// ---------------------------------------------------------------------------

func TestWrappedHost(t *testing.T) {
	tests := []struct {
		host, want string
	}{
		{"10.96.0.1", "10.96.0.1"},
		{"192.168.1.1", "192.168.1.1"},
		{"fd00::1", "[fd00::1]"},
		{"::1", "[::1]"},
		{"2001:db8::1", "[2001:db8::1]"},
	}
	for _, tt := range tests {
		c := &config{KubeHost: tt.host}
		if got := c.wrappedHost(); got != tt.want {
			t.Errorf("wrappedHost(%q) = %q, want %q", tt.host, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// writeKubeConfig
// ---------------------------------------------------------------------------

func TestWriteKubeConfig_SkipTLS(t *testing.T) {
	cfg := newTestConfig(t, false)
	must(t, writeKubeConfig(cfg))

	data, err := os.ReadFile(cfg.kubeconfigPath())
	must(t, err)

	// Parse back via clientcmd to validate structure.
	parsed, err := clientcmd.Load(data)
	if err != nil {
		t.Fatalf("generated kubeconfig is not valid: %v", err)
	}

	cluster, ok := parsed.Clusters["local"]
	if !ok {
		t.Fatal("cluster 'local' not found")
	}
	assertEqual(t, "server", cluster.Server, "https://10.96.0.1:443")
	if !cluster.InsecureSkipTLSVerify {
		t.Error("expected InsecureSkipTLSVerify=true")
	}
	if len(cluster.CertificateAuthorityData) > 0 {
		t.Error("CertificateAuthorityData should be empty when SkipTLS")
	}

	user, ok := parsed.AuthInfos["whereabouts"]
	if !ok {
		t.Fatal("user 'whereabouts' not found")
	}
	assertEqual(t, "token", user.Token, "test-token")

	ctx, ok := parsed.Contexts["whereabouts-context"]
	if !ok {
		t.Fatal("context 'whereabouts-context' not found")
	}
	assertEqual(t, "context.cluster", ctx.Cluster, "local")
	assertEqual(t, "context.user", ctx.AuthInfo, "whereabouts")
	assertEqual(t, "context.namespace", ctx.Namespace, "kube-system")
	assertEqual(t, "current-context", parsed.CurrentContext, "whereabouts-context")
}

func TestWriteKubeConfig_WithCA(t *testing.T) {
	cfg := newTestConfig(t, true)
	must(t, writeKubeConfig(cfg))

	data, err := os.ReadFile(cfg.kubeconfigPath())
	must(t, err)

	parsed, err := clientcmd.Load(data)
	if err != nil {
		t.Fatalf("generated kubeconfig is not valid: %v", err)
	}

	cluster := parsed.Clusters["local"]
	if cluster.InsecureSkipTLSVerify {
		t.Error("InsecureSkipTLSVerify should be false when CA is provided")
	}
	if string(cluster.CertificateAuthorityData) != "fake-ca" {
		t.Errorf("CertificateAuthorityData = %q, want %q", string(cluster.CertificateAuthorityData), "fake-ca")
	}
}

func TestWriteKubeConfig_IPv6(t *testing.T) {
	cfg := newTestConfig(t, false)
	cfg.KubeHost = "fd00::1"
	cfg.KubePort = "6443"
	must(t, writeKubeConfig(cfg))

	data, err := os.ReadFile(cfg.kubeconfigPath())
	must(t, err)

	parsed, err := clientcmd.Load(data)
	if err != nil {
		t.Fatalf("generated kubeconfig is not valid: %v", err)
	}
	assertEqual(t, "server", parsed.Clusters["local"].Server, "https://[fd00::1]:6443")
}

func TestWriteKubeConfig_MissingToken(t *testing.T) {
	cfg := newTestConfig(t, false)
	// Remove the token file.
	os.Remove(cfg.tokenPath())

	err := writeKubeConfig(cfg)
	if err == nil {
		t.Fatal("expected error when token file is missing")
	}
	assertContains(t, err.Error(), "reading service account token")
}

func TestWriteKubeConfig_MissingCA(t *testing.T) {
	cfg := newTestConfig(t, false)
	cfg.SkipTLSVerify = false
	// ca.crt was not created since withCA=false

	err := writeKubeConfig(cfg)
	if err == nil {
		t.Fatal("expected error when CA file is missing and SkipTLS=false")
	}
	assertContains(t, err.Error(), "reading CA file")
}

func TestWriteKubeConfig_UnwritableDir(t *testing.T) {
	cfg := newTestConfig(t, false)
	// Point config dir to a path that doesn't exist and can't be created.
	cfg.CNIConfDir = "/proc/nonexistent"

	err := writeKubeConfig(cfg)
	if err == nil {
		t.Fatal("expected error when kubeconfig path is unwritable")
	}
	assertContains(t, err.Error(), "writing kubeconfig")
}

func TestWriteKubeConfig_FilePermissions(t *testing.T) {
	cfg := newTestConfig(t, false)
	must(t, writeKubeConfig(cfg))

	info, err := os.Stat(cfg.kubeconfigPath())
	must(t, err)
	if info.Mode().Perm() != defaultKubeConfigMode {
		t.Errorf("kubeconfig mode = %o, want %o", info.Mode().Perm(), defaultKubeConfigMode)
	}
}

// ---------------------------------------------------------------------------
// writeWhereaboutsConf
// ---------------------------------------------------------------------------

func TestWriteWhereaboutsConf(t *testing.T) {
	cfg := newTestConfig(t, false)
	cfg.ReconcilerCron = "0 */6 * * *"
	must(t, writeWhereaboutsConf(cfg))

	data, err := os.ReadFile(cfg.whereaboutsConfPath())
	must(t, err)

	// Parse back via JSON to validate structure.
	var parsed whereaboutsConf
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("generated conf is not valid JSON: %v", err)
	}
	assertEqual(t, "datastore", parsed.Datastore, "kubernetes")
	assertEqual(t, "kubeconfig", parsed.Kubernetes.Kubeconfig, cfg.kubeconfigLiteral())
	assertEqual(t, "cron", parsed.ReconcilerCronExpression, "0 */6 * * *")
}

func TestWriteWhereaboutsConf_UnwritableDir(t *testing.T) {
	cfg := newTestConfig(t, false)
	cfg.CNIConfDir = "/proc/nonexistent"

	err := writeWhereaboutsConf(cfg)
	if err == nil {
		t.Fatal("expected error when conf path is unwritable")
	}
	assertContains(t, err.Error(), "writing whereabouts.conf")
}

// ---------------------------------------------------------------------------
// copyFile
// ---------------------------------------------------------------------------

func TestCopyFile(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src-binary")
	dst := filepath.Join(tmp, "dst-binary")
	must(t, os.WriteFile(src, []byte("binary content"), 0o755))

	must(t, copyFile(src, dst))

	data, err := os.ReadFile(dst)
	must(t, err)
	assertEqual(t, "content", string(data), "binary content")

	info, err := os.Stat(dst)
	must(t, err)
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %o, want 755", info.Mode().Perm())
	}
}

func TestCopyFile_ReplacesExisting(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	dst := filepath.Join(tmp, "dst")

	must(t, os.WriteFile(src, []byte("v2"), 0o755))
	must(t, os.WriteFile(dst, []byte("v1"), 0o755))

	must(t, copyFile(src, dst))

	data, err := os.ReadFile(dst)
	must(t, err)
	assertEqual(t, "replaced content", string(data), "v2")
}

func TestCopyFile_MissingSrc(t *testing.T) {
	tmp := t.TempDir()
	err := copyFile(filepath.Join(tmp, "missing"), filepath.Join(tmp, "dst"))
	if err == nil {
		t.Fatal("expected error for missing source")
	}
	assertContains(t, err.Error(), "opening")
}

func TestCopyFile_UnwritableDst(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	must(t, os.WriteFile(src, []byte("data"), 0o755))

	err := copyFile(src, filepath.Join("/proc/nonexistent", "dst"))
	if err == nil {
		t.Fatal("expected error for unwritable destination")
	}
	assertContains(t, err.Error(), "creating")
}

// ---------------------------------------------------------------------------
// fileHash
// ---------------------------------------------------------------------------

func TestFileHash(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "f")
	must(t, os.WriteFile(f, []byte("hello"), 0o600))

	h1 := fileHash(f)
	if h1 == "" {
		t.Fatal("fileHash returned empty for existing file")
	}

	// Same content → same hash.
	if h2 := fileHash(f); h1 != h2 {
		t.Errorf("same content gave different hashes: %q vs %q", h1, h2)
	}

	// Different content → different hash.
	must(t, os.WriteFile(f, []byte("world"), 0o600))
	if h3 := fileHash(f); h1 == h3 {
		t.Error("different content should produce different hash")
	}
}

func TestFileHash_Missing(t *testing.T) {
	if got := fileHash("/nonexistent/file"); got != "" {
		t.Errorf("fileHash(missing) = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// maybeRegenerate
// ---------------------------------------------------------------------------

func TestMaybeRegenerate_NoChange(t *testing.T) {
	cfg := newTestConfig(t, false)
	tokenH := fileHash(cfg.tokenPath())
	caH := fileHash(cfg.KubeCAFile)

	// Write initial kubeconfig so we can verify it doesn't change.
	must(t, writeKubeConfig(cfg))
	before, err := os.ReadFile(cfg.kubeconfigPath())
	must(t, err)

	newT, newC := maybeRegenerate(cfg, tokenH, caH)
	assertEqual(t, "tokenHash", newT, tokenH)
	assertEqual(t, "caHash", newC, caH)

	after, err := os.ReadFile(cfg.kubeconfigPath())
	must(t, err)
	if string(before) != string(after) {
		t.Error("kubeconfig should not have been rewritten when nothing changed")
	}
}

func TestMaybeRegenerate_TokenChange(t *testing.T) {
	cfg := newTestConfig(t, false)
	tokenH := fileHash(cfg.tokenPath())
	caH := fileHash(cfg.KubeCAFile)

	must(t, writeKubeConfig(cfg))

	// Rotate the token.
	must(t, os.WriteFile(cfg.tokenPath(), []byte("rotated-token"), 0o600))
	newT, _ := maybeRegenerate(cfg, tokenH, caH)
	if newT == tokenH {
		t.Error("token hash should have changed")
	}

	// Verify kubeconfig was updated with the rotated token.
	data, err := os.ReadFile(cfg.kubeconfigPath())
	must(t, err)
	parsed, err := clientcmd.Load(data)
	if err != nil {
		t.Fatalf("kubeconfig not valid after regen: %v", err)
	}
	assertEqual(t, "rotated token", parsed.AuthInfos["whereabouts"].Token, "rotated-token")
}

func TestMaybeRegenerate_CAChange(t *testing.T) {
	cfg := newTestConfig(t, true) // withCA → SkipTLSVerify=false
	must(t, writeKubeConfig(cfg))

	tokenH := fileHash(cfg.tokenPath())
	caH := fileHash(cfg.KubeCAFile)

	// Rotate the CA.
	must(t, os.WriteFile(cfg.KubeCAFile, []byte("rotated-ca"), 0o600))
	_, newC := maybeRegenerate(cfg, tokenH, caH)
	if newC == caH {
		t.Error("CA hash should have changed")
	}
}

func TestMaybeRegenerate_CAChangeIgnoredWhenSkipTLS(t *testing.T) {
	cfg := newTestConfig(t, false) // SkipTLSVerify=true
	// Create a ca.crt so we get a real initial hash.
	must(t, os.WriteFile(cfg.KubeCAFile, []byte("ca-v1"), 0o600))
	must(t, writeKubeConfig(cfg))
	before, err := os.ReadFile(cfg.kubeconfigPath())
	must(t, err)

	tokenH := fileHash(cfg.tokenPath())
	caH := fileHash(cfg.KubeCAFile)

	// Rotate CA — should be ignored because SkipTLSVerify=true.
	must(t, os.WriteFile(cfg.KubeCAFile, []byte("ca-v2"), 0o600))
	newT, _ := maybeRegenerate(cfg, tokenH, caH)
	assertEqual(t, "tokenHash unchanged", newT, tokenH)

	after, err := os.ReadFile(cfg.kubeconfigPath())
	must(t, err)
	if string(before) != string(after) {
		t.Error("kubeconfig should not change when only CA rotates and SkipTLS is true")
	}
}

func TestMaybeRegenerate_WriteFailRetainsOldHashes(t *testing.T) {
	cfg := newTestConfig(t, false)
	tokenH := fileHash(cfg.tokenPath())
	caH := fileHash(cfg.KubeCAFile)

	// Rotate the token so a change IS detected …
	must(t, os.WriteFile(cfg.tokenPath(), []byte("new-tok"), 0o600))
	// … but make the kubeconfig path unwritable so regeneration fails.
	cfg.CNIConfDir = "/proc/nonexistent"

	retT, retC := maybeRegenerate(cfg, tokenH, caH)
	// On failure the OLD hashes are returned so we retry next tick.
	assertEqual(t, "tokenHash on failure", retT, tokenH)
	assertEqual(t, "caHash on failure", retC, caH)
}

// ---------------------------------------------------------------------------
// watchAndRegenerate (with fsnotify)
// ---------------------------------------------------------------------------

func TestWatchAndRegenerate_ContextCancel(t *testing.T) {
	cfg := newTestConfig(t, false)
	must(t, writeKubeConfig(cfg))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		watchAndRegenerate(ctx, cfg)
		close(done)
	}()

	// Give the watcher time to start, then cancel.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// success — watchAndRegenerate returned promptly.
	case <-time.After(5 * time.Second):
		t.Fatal("watchAndRegenerate did not return after context cancellation")
	}
}

func TestWatchAndRegenerate_DetectsFileChange(t *testing.T) {
	cfg := newTestConfig(t, false)
	must(t, writeKubeConfig(cfg))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		watchAndRegenerate(ctx, cfg)
		close(done)
	}()

	// Give the watcher time to start.
	time.Sleep(200 * time.Millisecond)

	// Rotate the token — fsnotify should pick up the write.
	must(t, os.WriteFile(cfg.tokenPath(), []byte("rotated"), 0o600))

	// Wait for the regeneration to be visible via parsed kubeconfig.
	var found bool
	for range 30 {
		time.Sleep(100 * time.Millisecond)
		data, err := os.ReadFile(cfg.kubeconfigPath())
		if err != nil {
			continue
		}
		parsed, err := clientcmd.Load(data)
		if err != nil {
			continue
		}
		if ai, ok := parsed.AuthInfos["whereabouts"]; ok && ai.Token == "rotated" {
			found = true
			break
		}
	}
	cancel()
	<-done

	if !found {
		t.Error("kubeconfig was not regenerated after token rotation")
	}
}

// ---------------------------------------------------------------------------
// pollLoop
// ---------------------------------------------------------------------------

func TestPollLoop_ContextCancel(t *testing.T) {
	cfg := newTestConfig(t, false)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		pollLoop(ctx, cfg, "", "")
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pollLoop did not return after context cancellation")
	}
}

// ---------------------------------------------------------------------------
// run (integration — exercises the full setup path)
// ---------------------------------------------------------------------------

func TestRun_ConfigError(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := run(ctx)
	if err == nil {
		t.Fatal("expected error from run() with missing env vars")
	}
	assertContains(t, err.Error(), "loading configuration")
}

func TestRun_BadConfigDir(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	t.Setenv("CNI_CONF_DIR", "/proc/nonexistent/deep/path")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := run(ctx)
	if err == nil {
		t.Fatal("expected error from run() with unwritable config dir")
	}
	assertContains(t, err.Error(), "creating config directory")
}

func TestRun_MissingToken(t *testing.T) {
	tmp := t.TempDir()
	saDir := filepath.Join(tmp, "sa")
	must(t, os.MkdirAll(saDir, 0o755))
	// Do NOT write a token file.

	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	t.Setenv("CNI_CONF_DIR", filepath.Join(tmp, "cni"))
	t.Setenv("SERVICE_ACCOUNT_PATH", saDir)
	t.Setenv("SKIP_TLS_VERIFY", "true")

	must(t, os.MkdirAll(filepath.Join(tmp, "cni", "whereabouts.d"), 0o755))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := run(ctx)
	if err == nil {
		t.Fatal("expected error from run() with missing token")
	}
	assertContains(t, err.Error(), "writing kubeconfig")
}

func TestRun_MissingSourceBinary(t *testing.T) {
	cfg := newTestConfig(t, false)

	t.Setenv("KUBERNETES_SERVICE_HOST", cfg.KubeHost)
	t.Setenv("KUBERNETES_SERVICE_PORT", cfg.KubePort)
	t.Setenv("CNI_BIN_DIR", cfg.CNIBinDir)
	t.Setenv("CNI_BIN_SRC", filepath.Join(t.TempDir(), "nonexistent"))
	t.Setenv("CNI_CONF_DIR", cfg.CNIConfDir)
	t.Setenv("SERVICE_ACCOUNT_PATH", cfg.ServiceAccountPath)
	t.Setenv("SKIP_TLS_VERIFY", "true")
	t.Setenv("WHEREABOUTS_NAMESPACE", cfg.Namespace)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := run(ctx)
	if err == nil {
		t.Fatal("expected error from run() when /whereabouts binary doesn't exist")
	}
	assertContains(t, err.Error(), "copying whereabouts binary")
}

func TestRun_WhereaboutsConfFails(t *testing.T) {
	cfg := newTestConfig(t, false)

	// Place a directory where whereabouts.conf should be written,
	// so the write fails while kubeconfig (different filename) succeeds.
	must(t, os.MkdirAll(cfg.whereaboutsConfPath(), 0o755))

	t.Setenv("KUBERNETES_SERVICE_HOST", cfg.KubeHost)
	t.Setenv("KUBERNETES_SERVICE_PORT", cfg.KubePort)
	t.Setenv("CNI_BIN_DIR", cfg.CNIBinDir)
	t.Setenv("CNI_CONF_DIR", cfg.CNIConfDir)
	t.Setenv("SERVICE_ACCOUNT_PATH", cfg.ServiceAccountPath)
	t.Setenv("SKIP_TLS_VERIFY", "true")
	t.Setenv("WHEREABOUTS_NAMESPACE", cfg.Namespace)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := run(ctx)
	if err == nil {
		t.Fatal("expected error when whereabouts.conf path is a directory")
	}
	assertContains(t, err.Error(), "writing whereabouts.conf")
}

func TestRun_FullSuccess(t *testing.T) {
	cfg := newTestConfig(t, false)

	// Create a fake whereabouts binary as the source.
	fakeBin := filepath.Join(t.TempDir(), "whereabouts")
	must(t, os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0o755))

	t.Setenv("KUBERNETES_SERVICE_HOST", cfg.KubeHost)
	t.Setenv("KUBERNETES_SERVICE_PORT", cfg.KubePort)
	t.Setenv("CNI_BIN_DIR", cfg.CNIBinDir)
	t.Setenv("CNI_BIN_SRC", fakeBin)
	t.Setenv("CNI_CONF_DIR", cfg.CNIConfDir)
	t.Setenv("SERVICE_ACCOUNT_PATH", cfg.ServiceAccountPath)
	t.Setenv("SKIP_TLS_VERIFY", "true")
	t.Setenv("WHEREABOUTS_NAMESPACE", cfg.Namespace)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately so watchAndRegenerate returns right away.
	done := make(chan error, 1)
	go func() {
		done <- run(ctx)
	}()

	// Give run() enough time to finish setup, then cancel.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run() returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run() did not return after context cancellation")
	}

	// Verify all artifacts were created.
	if _, err := os.Stat(cfg.kubeconfigPath()); err != nil {
		t.Errorf("kubeconfig not created: %v", err)
	}
	if _, err := os.Stat(cfg.whereaboutsConfPath()); err != nil {
		t.Errorf("whereabouts.conf not created: %v", err)
	}
	dstBin := filepath.Join(cfg.CNIBinDir, "whereabouts")
	if _, err := os.Stat(dstBin); err != nil {
		t.Errorf("binary not copied: %v", err)
	}

	// Validate kubeconfig is parseable.
	data, err := os.ReadFile(cfg.kubeconfigPath())
	must(t, err)
	if _, err := clientcmd.Load(data); err != nil {
		t.Errorf("kubeconfig not valid: %v", err)
	}

	// Validate whereabouts.conf is valid JSON.
	confData, err := os.ReadFile(cfg.whereaboutsConfPath())
	must(t, err)
	var conf whereaboutsConf
	if err := json.Unmarshal(confData, &conf); err != nil {
		t.Errorf("whereabouts.conf not valid JSON: %v", err)
	}
}

// ---------------------------------------------------------------------------
// watchAndRegenerate — polling fallback
// ---------------------------------------------------------------------------

func TestWatchAndRegenerate_FallbackOnBadSADir(t *testing.T) {
	cfg := newTestConfig(t, false)
	must(t, writeKubeConfig(cfg))

	// Point ServiceAccountPath to a nonexistent dir so watcher.Add fails
	// and watchAndRegenerate falls back to pollLoop.
	cfg.ServiceAccountPath = filepath.Join(t.TempDir(), "does-not-exist")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		watchAndRegenerate(ctx, cfg)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watchAndRegenerate polling fallback did not return after cancel")
	}
}

func TestWatchAndRegenerate_TickerTriggersRegenerate(t *testing.T) {
	old := pollInterval
	pollInterval = 100 * time.Millisecond
	t.Cleanup(func() { pollInterval = old })

	cfg := newTestConfig(t, false)
	must(t, writeKubeConfig(cfg))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		watchAndRegenerate(ctx, cfg)
		close(done)
	}()

	// Let the watcher start, then rotate token.
	time.Sleep(50 * time.Millisecond)
	must(t, os.WriteFile(cfg.tokenPath(), []byte("ticker-token"), 0o600))

	// Wait long enough for the ticker to fire (100ms) and regenerate.
	var found bool
	for range 30 {
		time.Sleep(50 * time.Millisecond)
		data, err := os.ReadFile(cfg.kubeconfigPath())
		if err != nil {
			continue
		}
		parsed, err := clientcmd.Load(data)
		if err != nil {
			continue
		}
		if ai, ok := parsed.AuthInfos["whereabouts"]; ok && ai.Token == "ticker-token" {
			found = true
			break
		}
	}
	cancel()
	<-done

	if !found {
		t.Error("kubeconfig was not regenerated via ticker fallback")
	}
}

func TestPollLoop_TickerTriggersRegenerate(t *testing.T) {
	old := pollInterval
	pollInterval = 100 * time.Millisecond
	t.Cleanup(func() { pollInterval = old })

	cfg := newTestConfig(t, false)
	must(t, writeKubeConfig(cfg))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tokenH := fileHash(cfg.tokenPath())
	caH := fileHash(cfg.KubeCAFile)

	done := make(chan struct{})
	go func() {
		pollLoop(ctx, cfg, tokenH, caH)
		close(done)
	}()

	// Rotate token; pollLoop should pick it up on next tick.
	time.Sleep(50 * time.Millisecond)
	must(t, os.WriteFile(cfg.tokenPath(), []byte("poll-token"), 0o600))

	// Wait for at least one tick.
	var found bool
	for range 30 {
		time.Sleep(50 * time.Millisecond)
		data, err := os.ReadFile(cfg.kubeconfigPath())
		if err != nil {
			continue
		}
		parsed, err := clientcmd.Load(data)
		if err != nil {
			continue
		}
		if ai, ok := parsed.AuthInfos["whereabouts"]; ok && ai.Token == "poll-token" {
			found = true
			break
		}
	}
	cancel()
	<-done

	if !found {
		t.Error("kubeconfig was not regenerated via polling ticker")
	}
}

// ---------------------------------------------------------------------------
// assertion helpers
// ---------------------------------------------------------------------------

func assertEqual(t *testing.T, label, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", label, got, want)
	}
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected string to contain %q, got:\n%s", substr, s)
	}
}
