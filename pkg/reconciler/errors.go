package reconciler

const (
	kubeconfigNotFound = iota + 1
	couldNotStartOrphanedIPMonitor
	failedToReconcileIPPools
	failedToReconcileClusterWideIPs
)
