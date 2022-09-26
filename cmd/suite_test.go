package main

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/pkg/api/whereabouts.cni.cncf.io/v1alpha1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var etcdHost string
var tmpdir string
var kubeConfigPath string

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Whereabouts Suite",
		[]Reporter{})
}

var _ = BeforeSuite(func(done Done) {
	zap.WriteTo(GinkgoWriter)
	logf.SetLogger(zap.New())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "doc", "crds")},
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	Expect(testEnv.ControlPlane.Etcd).ToNot(BeNil())
	etcdURL := testEnv.ControlPlane.Etcd.URL
	Expect(etcdURL).ToNot(BeNil())
	etcdHost = etcdURL.String()

	Expect(testEnv.ControlPlane.APIServer).ToNot(BeNil())
	apiURL := testEnv.ControlPlane.APIServer.SecureServing.URL("https", "/").String()
	Expect(apiURL).ToNot(BeNil())

	tmpdir, err = os.MkdirTemp("/tmp", "whereabouts")
	Expect(err).ToNot(HaveOccurred())

	kubeConfigPath = fmt.Sprintf("%s/whereabouts.kubeconfig", tmpdir)
	Expect(copyEnvKubeconfigFile(testEnv.ControlPlane.APIServer.CertDir, kubeConfigPath)).To(Succeed())

	err = whereaboutsv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).ToNot(HaveOccurred())
	Expect(k8sClient).ToNot(BeNil())

	close(done)
}, 60)

var _ = AfterSuite(func() {
	defer os.RemoveAll(tmpdir)

	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).ToNot(HaveOccurred())
})

func copyEnvKubeconfigFile(envCertDir string, destFilePath string) error {
	entries, err := os.ReadDir(envCertDir)
	if err != nil {
		return fmt.Errorf("failed to read the certificate dir: %w", err)
	}
	files := make([]fs.FileInfo, 0, len(entries))

	for _, file := range files {
		if isKubeconfigFile(file) {
			return copyFile(path.Join(envCertDir, file.Name()), destFilePath, file.Mode())
		}
	}
	return fmt.Errorf("could not find the generated kubeconfig file")
}

func isKubeconfigFile(file fs.FileInfo) bool {
	return strings.HasSuffix(file.Name(), ".kubecfg") ||
		strings.HasSuffix(file.Name(), ".kubeconfig")
}

func copyFile(src string, dst string, mode fs.FileMode) error {
	fileContents, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("failed to read src file %s: %w", src, err)
	}

	if err := os.WriteFile(dst, fileContents, mode); err != nil {
		return fmt.Errorf("error writing dst file %s: %w", dst, err)
	}
	return nil
}
