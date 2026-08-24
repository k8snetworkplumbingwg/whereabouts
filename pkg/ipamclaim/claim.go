package ipamclaim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	ipamclaimsv1alpha1 "github.com/k8snetworkplumbingwg/ipamclaims/pkg/crd/ipamclaims/v1alpha1"
	ipamclaimsclient "github.com/k8snetworkplumbingwg/ipamclaims/pkg/crd/ipamclaims/v1alpha1/apis/clientset/versioned"
	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	nadutils "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/utils"
	"gomodules.xyz/jsonpatch/v2"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	wbtypes "github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

// ResolveFromPodAnnotation looks up the pod and reads IPAMClaimReference from its
// network-selection elements when the config does not already carry a claim ref.
// This matches ipam-extensions / OVN-Kubernetes wiring.
func ResolveFromPodAnnotation(ctx context.Context, clientset kubernetes.Interface, ipamConf *wbtypes.IPAMConfig, ifName string) error {
	if ipamConf == nil || ipamConf.HasIPAMClaim() || clientset == nil {
		return nil
	}
	if ipamConf.PodName == "" || ipamConf.PodNamespace == "" {
		return nil
	}

	// During early pod sandbox creation, the Pod may not be readable from the API yet.
	// Retry NotFound for a bounded amount of time so we can resolve the IPAMClaim reference.
	// (The CNI ADD path can succeed even if claim resolution fails; that would leave
	// claim-backed allocations untagged.)
	var pod *corev1.Pod
	// Keep this reasonably high for "brand new pod" timing in kind clusters.
	deadline := time.Now().Add(45 * time.Second)
	for {
		// Don't couple the retry loop to the request context deadline. A short per-attempt
		// timeout keeps each API call bounded.
		attemptCtx, attemptCancel := context.WithTimeout(context.Background(), 1*time.Second)
		p, err := clientset.CoreV1().Pods(ipamConf.PodNamespace).Get(attemptCtx, ipamConf.PodName, metav1.GetOptions{})
		attemptCancel()
		if err == nil {
			pod = p
			break
		}
		if apierrors.IsNotFound(err) {
			logging.Debugf("Pod %s/%s not found while resolving IPAMClaim; retrying", ipamConf.PodNamespace, ipamConf.PodName)
			if time.Now().After(deadline) {
				logging.Debugf("Giving up resolving IPAMClaim: pod %s/%s not found after retries", ipamConf.PodNamespace, ipamConf.PodName)
				return nil
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}
		return fmt.Errorf("failed to get pod %s/%s for IPAMClaim resolution: %w", ipamConf.PodNamespace, ipamConf.PodName, err)
	}

	claimName, err := ClaimNameFromPod(pod, ipamConf.Name, ifName)
	if err != nil {
		return err
	}
	if claimName == "" {
		logging.Debugf("No IPAMClaimReference found in pod %s/%s for network=%q ifName=%q", pod.Namespace, pod.Name, ipamConf.Name, ifName)
		return nil
	}

	ipamConf.IPAMClaimReference = claimName
	if ipamConf.IPAMClaimNamespace == "" {
		ipamConf.IPAMClaimNamespace = ipamConf.PodNamespace
	}
	logging.Debugf("Resolved IPAMClaim reference %q from pod network-selection annotation", ipamConf.GetIPAMClaimRef())
	return nil
}

// ClaimNameFromPod extracts the IPAMClaim name for the given network/interface.
func ClaimNameFromPod(pod *corev1.Pod, networkName, ifName string) (string, error) {
	if pod == nil {
		return "", nil
	}
	elements, err := nadutils.ParsePodNetworkAnnotation(pod)
	if err != nil {
		var noNet *nadv1.NoK8sNetworkError
		if errors.As(err, &noNet) {
			return "", nil
		}
		return "", fmt.Errorf("failed to parse pod network annotation: %w", err)
	}
	logging.Debugf("Parsed %d NSE elements for pod %s/%s (network=%q ifName=%q)", len(elements), pod.Namespace, pod.Name, networkName, ifName)
	for _, el := range elements {
		if el == nil {
			continue
		}
		logging.Debugf("NSE element name=%q ifaceReq=%q claimRef=%q", el.Name, el.InterfaceRequest, el.IPAMClaimReference)
	}
	return ReferenceFromNetworkSelectionElements(elements, networkName, ifName), nil
}

// ParseClaimIPs converts IPAMClaim status.ips into net.IP values.
func ParseClaimIPs(claim *ipamclaimsv1alpha1.IPAMClaim) []net.IP {
	if claim == nil {
		return nil
	}
	var ips []net.IP
	for _, s := range claim.Status.IPs {
		if ip := net.ParseIP(s); ip != nil {
			ips = append(ips, ip)
		}
	}
	return ips
}

// GetClaim fetches an IPAMClaim by namespace/name from IPAMConfig.
func GetClaim(ctx context.Context, client ipamclaimsclient.Interface, ipamConf wbtypes.IPAMConfig) (*ipamclaimsv1alpha1.IPAMClaim, error) {
	if !ipamConf.HasIPAMClaim() || client == nil {
		return nil, nil
	}
	ns := ipamConf.IPAMClaimNamespace
	if ns == "" {
		ns = ipamConf.PodNamespace
	}
	return client.K8sV1alpha1().IPAMClaims(ns).Get(ctx, ipamConf.IPAMClaimReference, metav1.GetOptions{})
}

// PersistClaimIPs writes allocated addresses and ownerPod onto the IPAMClaim status.
func PersistClaimIPs(ctx context.Context, client ipamclaimsclient.Interface, ipamConf wbtypes.IPAMConfig, allocated []net.IPNet) error {
	if !ipamConf.HasIPAMClaim() || client == nil || len(allocated) == 0 {
		return nil
	}

	ns := ipamConf.IPAMClaimNamespace
	if ns == "" {
		ns = ipamConf.PodNamespace
	}

	claim, err := client.K8sV1alpha1().IPAMClaims(ns).Get(ctx, ipamConf.IPAMClaimReference, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get IPAMClaim %s/%s: %w", ns, ipamConf.IPAMClaimReference, err)
	}

	ipStrs := make([]string, 0, len(allocated))
	for _, ipn := range allocated {
		ipStrs = append(ipStrs, ipn.IP.String())
	}

	if len(claim.Status.IPs) == 0 {
		claim.Status.IPs = ipStrs
	}
	claim.Status.OwnerPod = &ipamclaimsv1alpha1.OwnerPod{Name: ipamConf.PodName}

	updated, err := client.K8sV1alpha1().IPAMClaims(ns).UpdateStatus(ctx, claim, metav1.UpdateOptions{})
	if err != nil {
		logging.Debugf("UpdateStatus failed (%v); attempting status patch", err)
		return patchClaimStatus(ctx, client, ns, claim.Name, claim.ResourceVersion, claim.Status.IPs, ipamConf.PodName)
	}
	logging.Debugf("Persisted IPs %v on IPAMClaim %s/%s (resourceVersion %s)", updated.Status.IPs, ns, claim.Name, updated.ResourceVersion)
	return nil
}

func patchClaimStatus(ctx context.Context, client ipamclaimsclient.Interface, ns, name, resourceVersion string, ips []string, ownerPod string) error {
	ops := []jsonpatch.Operation{
		{Operation: "test", Path: "/metadata/resourceVersion", Value: resourceVersion},
		{Operation: "replace", Path: "/status/ips", Value: ips},
		{Operation: "add", Path: "/status/ownerPod", Value: map[string]string{"name": ownerPod}},
	}
	patchData, err := json.Marshal(ops)
	if err != nil {
		return err
	}
	_, err = client.K8sV1alpha1().IPAMClaims(ns).Patch(ctx, name, types.JSONPatchType, patchData, metav1.PatchOptions{}, "status")
	return err
}
