package controlloop

const (
	kubeconfigNotFound = iota + 1
	couldNotStartOrphanedIPMonitor
	failedToReconcileIPPools
	failedToReconcileClusterWideIPs
)

// this stuff is used for ip reconciler^^^
