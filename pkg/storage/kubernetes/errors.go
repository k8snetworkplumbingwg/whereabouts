package kubernetes

import "errors"

// ErrPoolInitialized is returned when an IPPool is freshly created and
// the allocation loop must retry so that metadata / resourceVersions are
// populated by the subsequent Get call.
var ErrPoolInitialized = errors.New("k8s pool initialized")

type temporaryError struct {
	error
}

func (t *temporaryError) Temporary() bool {
	return true
}

func (t *temporaryError) Unwrap() error {
	return t.error
}
