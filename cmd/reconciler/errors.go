package main

const (
	kubeconfigNotFound = iota + 1
	couldNotStartOrphanedIPMonitor
	failedToReconcileIPPools
	failedToReconcileClusterWideIPs
)
