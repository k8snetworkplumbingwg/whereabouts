// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

// Package controller provides a PatchHelper inspired by the ClusterAPI
// patch.Helper pattern.  It snapshots an object before reconciliation and
// defers a single, diff-based MergeFrom patch at the end.  If nothing changed
// the helper is a no-op and no API call is made.
//
// Unlike a status-only helper, PatchHelper handles spec, metadata, AND status
// changes — issuing at most two API calls (one for the main object, one for
// the status subresource) and zero calls when nothing changed.
package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PatchHelper implements the "snapshot → mutate → deferred patch" pattern
// from ClusterAPI's patch.Helper.
//
// Usage:
//
//	helper, err := NewPatchHelper(obj, c)
//	if err != nil { return err }
//	defer func() {
//	    if pErr := helper.Patch(ctx, obj); pErr != nil { retErr = pErr }
//	}()
//	// … mutate obj (spec, metadata, status, conditions, etc.) …
type PatchHelper struct {
	client       client.Client
	beforeObject client.Object
	beforeSpec   []byte // JSON-serialized spec + metadata snapshot (everything minus status)
	beforeStatus []byte // JSON-serialized .status snapshot
}

// NewPatchHelper snapshots the current state of obj so it can later be
// compared with the mutated state.  Call this *before* making any changes
// to the object.
func NewPatchHelper(obj client.Object, c client.Client) (*PatchHelper, error) {
	if obj == nil {
		return nil, fmt.Errorf("cannot create PatchHelper for nil object")
	}

	spec, status, err := splitObject(obj)
	if err != nil {
		return nil, fmt.Errorf("snapshot object: %w", err)
	}

	copied := obj.DeepCopyObject()
	beforeObject, ok := copied.(client.Object)
	if !ok {
		return nil, fmt.Errorf("snapshot object: DeepCopyObject() returned type %T which does not implement client.Object", copied)
	}

	return &PatchHelper{
		client:       c,
		beforeObject: beforeObject,
		beforeSpec:   spec,
		beforeStatus: status,
	}, nil
}

// Patch compares the current object with the snapshot taken at creation time
// and issues the minimal set of API calls:
//   - If spec or metadata changed: one MergeFrom Patch on the main object
//   - If status changed: one Status().Update on the status subresource
//   - If nothing changed: zero API calls
//
// When both spec and status changed, the spec patch is sent first.  Since
// client.Patch updates obj in-place with the server response (resetting
// status to server state), the desired status is preserved and restored
// before the Status().Update call.
//
// Returns nil when nothing was patched.
func (h *PatchHelper) Patch(ctx context.Context, obj client.Object) error {
	if h == nil {
		return nil
	}

	afterSpec, afterStatus, err := splitObject(obj)
	if err != nil {
		return fmt.Errorf("serializing after object: %w", err)
	}

	specChanged := !jsonEqual(h.beforeSpec, afterSpec)
	statusChanged := !jsonEqual(h.beforeStatus, afterStatus)

	if !specChanged && !statusChanged {
		return nil
	}

	var errs []error

	switch {
	case specChanged && statusChanged:
		// Save the desired object (with both spec+status changes) so we
		// can restore its status after the spec patch resets it.
		copied := obj.DeepCopyObject()
		desired, ok := copied.(client.Object)
		if !ok {
			return fmt.Errorf("snapshot object: DeepCopyObject() returned type %T which does not implement client.Object", copied)
		}

		if err := h.client.Patch(ctx, obj, client.MergeFrom(h.beforeObject)); err != nil {
			errs = append(errs, fmt.Errorf("patching spec/metadata: %w", err))
		} else {
			// Re-apply the desired status onto obj (which now reflects the
			// server-returned spec but has stale/reset status).
			if err := overwriteStatus(desired, obj); err != nil {
				errs = append(errs, fmt.Errorf("restoring status: %w", err))
			} else if err := h.client.Status().Update(ctx, obj); err != nil {
				errs = append(errs, fmt.Errorf("updating status: %w", err))
			}
		}
	case specChanged:
		if err := h.client.Patch(ctx, obj, client.MergeFrom(h.beforeObject)); err != nil {
			errs = append(errs, fmt.Errorf("patching spec/metadata: %w", err))
		}
	default:
		// Only status changed.
		if err := h.client.Status().Update(ctx, obj); err != nil {
			errs = append(errs, fmt.Errorf("updating status: %w", err))
		}
	}

	return kerrors.NewAggregate(errs)
}

// overwriteStatus copies the "status" field from src onto dst via JSON
// round-trip.  All other fields on dst (including resourceVersion) are left
// intact so the caller can issue a Status().Update with the correct RV.
func overwriteStatus(src, dst client.Object) error {
	srcRaw, err := json.Marshal(src)
	if err != nil {
		return err
	}
	dstRaw, err := json.Marshal(dst)
	if err != nil {
		return err
	}

	var srcMap, dstMap map[string]json.RawMessage
	if err := json.Unmarshal(srcRaw, &srcMap); err != nil {
		return err
	}
	if err := json.Unmarshal(dstRaw, &dstMap); err != nil {
		return err
	}

	// Replace status in dst with status from src.
	if status, ok := srcMap["status"]; ok {
		dstMap["status"] = status
	} else {
		delete(dstMap, "status")
	}

	merged, err := json.Marshal(dstMap)
	if err != nil {
		return err
	}
	return json.Unmarshal(merged, dst)
}

// HasChanges reports whether the object has been modified since the snapshot
// was taken.  Useful in tests or logging.
func (h *PatchHelper) HasChanges(obj client.Object) bool {
	if h == nil {
		return false
	}
	afterSpec, afterStatus, err := splitObject(obj)
	if err != nil {
		return true // err on the side of "changed"
	}
	return !jsonEqual(h.beforeSpec, afterSpec) || !jsonEqual(h.beforeStatus, afterStatus)
}

// splitObject serializes the object to JSON and splits it into two blobs:
//   - spec: everything except the "status" key
//   - status: just the "status" key
//
// This allows independent diffing of spec+metadata vs status.
func splitObject(obj client.Object) (spec, status []byte, err error) {
	raw, err := json.Marshal(obj)
	if err != nil {
		return nil, nil, err
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, nil, err
	}

	// Extract status.
	statusVal, ok := m["status"]
	if !ok || len(statusVal) == 0 || string(statusVal) == "null" {
		status = []byte("{}")
	} else {
		status = statusVal
	}

	// Build spec blob (everything minus status).
	delete(m, "status")
	spec, err = json.Marshal(m)
	if err != nil {
		return nil, nil, err
	}

	return spec, status, nil
}

// jsonEqual compares two JSON blobs for semantic equality.
// It unmarshals both blobs into generic interface{} values and uses
// reflect.DeepEqual, which correctly handles map key ordering differences.
// Falls back to byte-level comparison only when the first blob is malformed.
// If the first parses but the second does not, they are treated as not equal.
func jsonEqual(a, b []byte) bool {
	var aVal, bVal interface{}
	if err := json.Unmarshal(a, &aVal); err != nil {
		return reflect.DeepEqual(a, b)
	}
	if err := json.Unmarshal(b, &bVal); err != nil {
		return false
	}
	return reflect.DeepEqual(aVal, bVal)
}
