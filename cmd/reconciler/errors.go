package main

const (
	kubeconfigNotFound = iota + 1
	couldNotStartOrphanedIPMonitor
	failedToReconcile
)
