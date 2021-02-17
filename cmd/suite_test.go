package main

import (
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	whereaboutsv1alpha1 "github.com/dougbtv/whereabouts/pkg/api/v1alpha1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var etcdHost string
var tmpdir string
var kubeConfigPath string

func crdDir() string {
	if dir := os.Getenv("WHEREABOUTS_CRD_DIR"); dir != "" {
		return dir
	}
	return "doc"
}

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Whereabouts Suite",
		[]Reporter{printer.NewlineReporter{}})
}

var _ = BeforeSuite(func(done Done) {
	logf.SetLogger(zap.LoggerTo(GinkgoWriter, true))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", crdDir())},
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
	apiURL := testEnv.ControlPlane.APIServer.URL.String()
	Expect(apiURL).ToNot(BeNil())

	var caContents string
	for _, s := range strings.Split(string(cfg.TLSClientConfig.CAData), "\n") {
		caContents += base64.StdEncoding.EncodeToString([]byte(s))
	}

	tmpdir, err = ioutil.TempDir("/tmp", "whereabouts")
	Expect(err).ToNot(HaveOccurred())

	kubeconfig := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"local": {Server: apiURL, CertificateAuthorityData: []byte(caContents)},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"whereabouts": {
				Token: cfg.BearerToken,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"whereabouts-context": {
				Cluster:   "local",
				AuthInfo:  "whereabouts",
				Namespace: "default",
			},
		},
		CurrentContext: "whereabouts-context",
	}
	kubeConfigPath = fmt.Sprintf("%s/whereabouts.kubeconfig", tmpdir)
	err = clientcmd.WriteToFile(kubeconfig, kubeConfigPath)
	Expect(err).ToNot(HaveOccurred())

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
