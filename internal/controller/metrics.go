// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
)

// Prometheus metrics for whereabouts operator controllers.
//
// All metrics use the "whereabouts" namespace and are registered with the
// controller-runtime metrics registry so they are served on the same
// /metrics endpoint exposed by the controller-runtime manager (default :8080).
var (
	// IPPool reconciler metrics.

	// ippoolAllocationsGauge reports the current number of IP allocations
	// in each IPPool. Labels: pool (IPPool name).
	ippoolAllocationsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "whereabouts",
			Subsystem: "ippool",
			Name:      "allocations",
			Help:      "Current number of IP allocations in the pool.",
		},
		[]string{"pool"},
	)

	// ippoolOrphansCleaned counts the total number of orphaned allocations
	// removed from IP pools. Labels: pool (IPPool name).
	ippoolOrphansCleaned = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "whereabouts",
			Subsystem: "ippool",
			Name:      "orphans_cleaned_total",
			Help:      "Total number of orphaned allocations removed from IP pools.",
		},
		[]string{"pool"},
	)

	// overlappingReservationsCleaned counts the total number of orphaned
	// OverlappingRangeIPReservation CRDs deleted.
	overlappingReservationsCleaned = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "whereabouts",
			Subsystem: "overlappingrange",
			Name:      "reservations_cleaned_total",
			Help:      "Total number of orphaned overlapping range reservations deleted.",
		},
	)

	// NodeSlice reconciler metrics.

	// nodesliceNodesGauge reports the number of nodes with assigned slices
	// in each NodeSlicePool. Labels: pool (NodeSlicePool name).
	nodesliceNodesGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "whereabouts",
			Subsystem: "nodeslicepool",
			Name:      "assigned_nodes",
			Help:      "Number of nodes with assigned IP slices in the pool.",
		},
		[]string{"pool"},
	)

	// nodesliceSlicesGauge reports the total number of slices in each
	// NodeSlicePool. Labels: pool (NodeSlicePool name).
	nodesliceSlicesGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "whereabouts",
			Subsystem: "nodeslicepool",
			Name:      "slices_total",
			Help:      "Total number of IP slices in the pool.",
		},
		[]string{"pool"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		ippoolAllocationsGauge,
		ippoolOrphansCleaned,
		overlappingReservationsCleaned,
		nodesliceNodesGauge,
		nodesliceSlicesGauge,
	)
}

// recordNodeSliceMetrics updates the NodeSlicePool gauges.
func recordNodeSliceMetrics(poolName string, allocations []whereaboutsv1alpha1.NodeSliceAllocation) {
	nodesliceSlicesGauge.WithLabelValues(poolName).Set(float64(len(allocations)))
	assigned := 0
	for i := range allocations {
		if allocations[i].NodeName != "" {
			assigned++
		}
	}
	nodesliceNodesGauge.WithLabelValues(poolName).Set(float64(assigned))
}
