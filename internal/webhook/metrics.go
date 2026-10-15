// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Prometheus metrics for whereabouts validating webhooks.
//
// Note: controller-runtime already provides generic webhook metrics:
//   - controller_runtime_webhook_requests_total (by webhook path and code)
//   - controller_runtime_webhook_latency_seconds (by webhook path)
//
// The metrics below track domain-specific validation outcomes.
var (
	// webhookValidationTotal counts webhook validation outcomes per resource
	// type and result. Labels: resource (ippool|nodeslicepool|overlappingrange),
	// operation (create|update|delete), result (allowed|rejected).
	webhookValidationTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "whereabouts",
			Subsystem: "webhook",
			Name:      "validation_total",
			Help:      "Total number of webhook validation decisions.",
		},
		[]string{"resource", "operation", "result"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		webhookValidationTotal,
	)
}

// recordValidation increments the validation counter with the appropriate
// result label based on whether the validation returned an error.
func recordValidation(resource, operation string, err error) {
	result := "allowed"
	if err != nil {
		result = "rejected"
	}
	webhookValidationTotal.WithLabelValues(resource, operation, result).Inc()
}
