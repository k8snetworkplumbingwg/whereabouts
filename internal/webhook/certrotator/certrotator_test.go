// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package certrotator

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func newTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	return scheme
}

var _ = Describe("ensureSecret", func() {
	var (
		ctx       context.Context
		secretKey types.NamespacedName
	)

	BeforeEach(func() {
		ctx = context.Background()
		secretKey = types.NamespacedName{
			Namespace: "kube-system",
			Name:      "whereabouts-webhook-cert",
		}
	})

	It("should create the secret when it does not exist", func() {
		fakeClient := fake.NewClientBuilder().
			WithScheme(newTestScheme()).
			Build()

		err := ensureSecret(ctx, fakeClient, secretKey)
		Expect(err).NotTo(HaveOccurred())

		// Verify the secret was actually created.
		var created corev1.Secret
		err = fakeClient.Get(ctx, secretKey, &created)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Name).To(Equal(secretKey.Name))
		Expect(created.Namespace).To(Equal(secretKey.Namespace))
		Expect(created.Type).To(Equal(corev1.SecretTypeTLS))
		Expect(created.Data).To(HaveKeyWithValue(corev1.TLSCertKey, []byte{}))
		Expect(created.Data).To(HaveKeyWithValue(corev1.TLSPrivateKeyKey, []byte{}))
	})

	It("should not error when the secret already exists", func() {
		existingSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretKey.Name,
				Namespace: secretKey.Namespace,
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"ca.crt":  []byte("existing-ca"),
				"tls.crt": []byte("existing-cert"),
			},
		}
		fakeClient := fake.NewClientBuilder().
			WithScheme(newTestScheme()).
			WithObjects(existingSecret).
			Build()

		err := ensureSecret(ctx, fakeClient, secretKey)
		Expect(err).NotTo(HaveOccurred())

		// Verify the existing secret data is preserved (not overwritten).
		var fetched corev1.Secret
		err = fakeClient.Get(ctx, secretKey, &fetched)
		Expect(err).NotTo(HaveOccurred())
		Expect(fetched.Data).To(HaveKeyWithValue("ca.crt", []byte("existing-ca")))
		Expect(fetched.Data).To(HaveKeyWithValue("tls.crt", []byte("existing-cert")))
	})

	It("should propagate non-NotFound Get errors", func() {
		expectedErr := apierrors.NewForbidden(
			schema.GroupResource{Resource: "secrets"},
			secretKey.Name,
			fmt.Errorf("access denied"),
		)
		fakeClient := fake.NewClientBuilder().
			WithScheme(newTestScheme()).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
					return expectedErr
				},
			}).
			Build()

		err := ensureSecret(ctx, fakeClient, secretKey)
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsForbidden(err)).To(BeTrue())
	})

	It("should propagate Create errors", func() {
		createErr := fmt.Errorf("simulated create failure")
		fakeClient := fake.NewClientBuilder().
			WithScheme(newTestScheme()).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.CreateOption) error {
					return createErr
				},
			}).
			Build()

		err := ensureSecret(ctx, fakeClient, secretKey)
		Expect(err).To(MatchError(ContainSubstring("simulated create failure")))
	})

	It("should be idempotent across multiple calls", func() {
		fakeClient := fake.NewClientBuilder().
			WithScheme(newTestScheme()).
			Build()

		// First call creates.
		err := ensureSecret(ctx, fakeClient, secretKey)
		Expect(err).NotTo(HaveOccurred())

		// Second call is a no-op (secret already exists).
		err = ensureSecret(ctx, fakeClient, secretKey)
		Expect(err).NotTo(HaveOccurred())

		// Only one secret should exist.
		var secretList corev1.SecretList
		err = fakeClient.List(ctx, &secretList, client.InNamespace(secretKey.Namespace))
		Expect(err).NotTo(HaveOccurred())
		Expect(secretList.Items).To(HaveLen(1))
	})
})
