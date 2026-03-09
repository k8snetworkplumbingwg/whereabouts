/*
Copyright 2020 The Kubernetes Authors.
Copyright 2021 The Flux authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

This file is modified from the source at
https://github.com/kubernetes-sigs/cluster-api/tree/7478817225e0a75acb6e14fc7b438231578073d2/util/conditions/unstructured.go,
and initially adapted to work with the `metav1.Condition` and `metav1.ConditionStatus` types.
More concretely, this includes the removal of "condition severity" related functionalities, as this is not supported by
the `metav1.Condition` type.
*/

package conditions

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	ErrUnstructuredFieldNotFound = fmt.Errorf("field not found")
)

// UnstructuredGetter return a Getter object that can read conditions from an Unstructured object.
//
// IMPORTANT: This method should be used only with types implementing status conditions with a metav1.Condition type.
func UnstructuredGetter(u *unstructured.Unstructured) Getter {
	return &unstructuredWrapper{Unstructured: u}
}

// UnstructuredSetter return a Setter object that can set conditions from an Unstructured object.
//
// IMPORTANT: This method should be used only with types implementing status conditions with a metav1.Condition type.
func UnstructuredSetter(u *unstructured.Unstructured) Setter {
	return &unstructuredWrapper{Unstructured: u}
}

// UnstructuredUnmarshalField is a wrapper around JSON and Unstructured objects to decode and copy a specific field
// value into an object.
func UnstructuredUnmarshalField(u *unstructured.Unstructured, v interface{}, fields ...string) error {
	value, found, err := unstructured.NestedFieldNoCopy(u.Object, fields...)
	if err != nil {
		return errors.Wrapf(err, "failed to retrieve field %q from %q", strings.Join(fields, "."), u.GroupVersionKind())
	}
	if !found || value == nil {
		return ErrUnstructuredFieldNotFound
	}
	valueBytes, err := json.Marshal(value)
	if err != nil {
		return errors.Wrapf(err, "failed to json-encode field %q value from %q", strings.Join(fields, "."), u.GroupVersionKind())
	}
	if err := json.Unmarshal(valueBytes, v); err != nil {
		return errors.Wrapf(err, "failed to json-decode field %q value from %q", strings.Join(fields, "."), u.GroupVersionKind())
	}
	return nil
}

type unstructuredWrapper struct {
	*unstructured.Unstructured
}

// GetConditions returns the list of conditions from an Unstructured object.
//
// NOTE: Due to the constraints of JSON-unmarshal, this operation is to be considered best effort.
// In more details:
//   - Errors during JSON-unmarshal are ignored and a empty collection list is returned.
//   - It's not possible to detect if the object has an empty condition list or if it does not implement conditions;
//     in both cases the operation returns an empty slice.
//   - If the object doesn't implement status conditions as defined in GitOps Toolkit API,
//     JSON-unmarshal matches incoming object keys to the keys; this can lead to to conditions values partially set.
func (c *unstructuredWrapper) GetConditions() []metav1.Condition {
	conditions := []metav1.Condition{}
	if err := UnstructuredUnmarshalField(c.Unstructured, &conditions, "status", "conditions"); err != nil {
		return nil
	}
	return conditions
}

// SetConditions set the conditions into an Unstructured object.
//
// NOTE: Due to the constraints of JSON-unmarshal, this operation is to be considered best effort.
// In more details:
//   - Errors during JSON-unmarshal are ignored and a empty collection list is returned.
//   - It's not possible to detect if the object has an empty condition list or if it does not implement conditions;
//     in both cases the operation returns an empty slice is returned.
func (c *unstructuredWrapper) SetConditions(conditions []metav1.Condition) {
	v := make([]interface{}, 0, len(conditions))
	for i := range conditions {
		m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&conditions[i])
		if err != nil {
			log.Log.Error(err, "Failed to convert Condition to unstructured map. This error shouldn't have occurred, please file an issue.", "groupVersionKind", c.GroupVersionKind(), "name", c.GetName(), "namespace", c.GetNamespace())
			continue
		}
		v = append(v, m)
	}
	// unstructured.SetNestedField returns an error only if value cannot be set because one of
	// the nesting levels is not a map[string]interface{}; this is not the case so the error should never happen here.
	err := unstructured.SetNestedField(c.Unstructured.Object, v, "status", "conditions")
	if err != nil {
		log.Log.Error(err, "Failed to set Conditions on unstructured object. This error shouldn't have occurred, please file an issue.", "groupVersionKind", c.GroupVersionKind(), "name", c.GetName(), "namespace", c.GetNamespace())
	}
}
