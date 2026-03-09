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
https://github.com/kubernetes-sigs/cluster-api/tree/7478817225e0a75acb6e14fc7b438231578073d2/util/conditions/getter.go,
and initially adapted to work with the `metav1.Condition` and `metav1.ConditionStatus` types.
More concretely, this includes the removal of "condition severity" related functionalities, as this is not supported by
the `metav1.Condition` type.
*/

package conditions

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/fluxcd/pkg/apis/meta"
)

// Getter interface defines methods that a Kubernetes resource object should implement in order to use the conditions
// package for getting conditions.
type Getter interface {
	client.Object
	meta.ObjectWithConditions
}

// Get returns the condition with the given type, if the condition does not exists, it returns nil.
func Get(from Getter, t string) *metav1.Condition {
	conditions := from.GetConditions()
	if conditions == nil {
		return nil
	}

	for _, condition := range conditions {
		if condition.Type == t {
			return &condition
		}
	}
	return nil
}

// Has returns true if a condition with the given type exists.
func Has(from Getter, t string) bool {
	return Get(from, t) != nil
}

// HasAny returns true if a condition with any of the given types exist.
func HasAny(from Getter, t []string) bool {
	for _, ct := range t {
		if Has(from, ct) {
			return true
		}
	}
	return false
}

// HasAnyReason returns true if a condition with the given
// type exists and any of the given reasons exist.
func HasAnyReason(from Getter, t string, r ...string) bool {
	for _, reason := range r {
		if GetReason(from, t) == reason {
			return true
		}
	}
	return false
}

// IsTrue is true if the condition with the given type is True, otherwise it is false if the condition is not True or if
// the condition does not exist (is nil).
func IsTrue(from Getter, t string) bool {
	if c := Get(from, t); c != nil {
		return c.Status == metav1.ConditionTrue
	}
	return false
}

// IsFalse is true if the condition with the given type is False, otherwise it is false if the condition is not False or
// if the condition does not exist (is nil).
func IsFalse(from Getter, t string) bool {
	if c := Get(from, t); c != nil {
		return c.Status == metav1.ConditionFalse
	}
	return false
}

// IsUnknown is true if the condition with the given type is Unknown or if the condition does not exist (is nil).
func IsUnknown(from Getter, t string) bool {
	if c := Get(from, t); c != nil {
		return c.Status == metav1.ConditionUnknown
	}
	return true
}

// IsReady is true if IsStalled and IsReconciling are False, and meta.ReadyCondition is True, otherwise it is false if
// the condition is not True or if it does not exist (is nil).
func IsReady(from Getter) bool {
	return !IsStalled(from) && !IsReconciling(from) && IsTrue(from, meta.ReadyCondition)
}

// IsStalled is true if meta.StalledCondition is True and meta.ReconcilingCondition is False or does not exist,
// otherwise it is false.
func IsStalled(from Getter) bool {
	return !IsTrue(from, meta.ReconcilingCondition) && IsTrue(from, meta.StalledCondition)
}

// IsReconciling is true if meta.ReconcilingCondition is True and meta.StalledCondition is False or does not exist,
// otherwise it is false.
func IsReconciling(from Getter) bool {
	return !IsTrue(from, meta.StalledCondition) && IsTrue(from, meta.ReconcilingCondition)
}

// GetReason returns a nil safe string of Reason for the condition with the given type.
func GetReason(from Getter, t string) string {
	if c := Get(from, t); c != nil {
		return c.Reason
	}
	return ""
}

// GetMessage returns a nil safe string of Message for the condition with the given type.
func GetMessage(from Getter, t string) string {
	if c := Get(from, t); c != nil {
		return c.Message
	}
	return ""
}

// GetLastTransitionTime returns the LastTransitionType or nil if the condition does not exist (is nil).
func GetLastTransitionTime(from Getter, t string) *metav1.Time {
	if c := Get(from, t); c != nil {
		return &c.LastTransitionTime
	}
	return nil
}

// GetObservedGeneration returns a nil safe int64 of ObservedGeneration for the condition with the given type.
func GetObservedGeneration(from Getter, t string) int64 {
	if c := Get(from, t); c != nil {
		return c.ObservedGeneration
	}
	return 0
}

// summary returns a condition with the summary of all the conditions existing on an object. If the object does not have
// other conditions, no summary condition is generated.
func summary(from Getter, t string, options ...MergeOption) *metav1.Condition {
	conditions := from.GetConditions()

	mergeOpt := &mergeOptions{}
	for _, o := range options {
		o(mergeOpt)
	}

	// Identifies the conditions in scope for the Summary by taking all the existing conditions except t,
	// or, if a list of conditions types is specified, only the conditions the condition in that list.
	conditionsInScope := make([]localizedCondition, 0, len(conditions))
	for i := range conditions {
		c := conditions[i]
		if c.Type == t {
			continue
		}

		if mergeOpt.conditionTypes != nil {
			found := false
			for _, tt := range mergeOpt.conditionTypes {
				if c.Type == tt {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		conditionsInScope = append(conditionsInScope, localizedCondition{
			Condition: &c,
			Getter:    from,
		})
	}

	// If it is required to add a step counter only if a subset of condition exists, check if the conditions
	// in scope are included in this subset or not.
	if mergeOpt.addStepCounterIfOnlyConditionTypes != nil {
		for _, c := range conditionsInScope {
			found := false
			for _, tt := range mergeOpt.addStepCounterIfOnlyConditionTypes {
				if c.Type == tt {
					found = true
					break
				}
			}
			if !found {
				mergeOpt.addStepCounter = false
				break
			}
		}
	}

	// If it is required to add a step counter, determine the total number of conditions defaulting
	// to the selected conditions or, if defined, to the total number of conditions type to be considered.
	if mergeOpt.addStepCounter {
		mergeOpt.stepCounter = len(conditionsInScope)
		if mergeOpt.conditionTypes != nil {
			mergeOpt.stepCounter = len(mergeOpt.conditionTypes)
		}
		if mergeOpt.addStepCounterIfOnlyConditionTypes != nil {
			mergeOpt.stepCounter = len(mergeOpt.addStepCounterIfOnlyConditionTypes)
		}
	}

	return merge(conditionsInScope, t, mergeOpt)
}

// mirrorOptions allows to set options for the mirror operation.
type mirrorOptions struct {
	fallbackTo      *bool
	fallbackReason  string
	fallbackMessage string
}

// MirrorOptions defines an option for mirroring conditions.
type MirrorOptions func(*mirrorOptions)

// WithFallbackValue specify a fallback value to use in case the mirrored condition does not exists; in case the
// fallbackValue is false, given values for reason and message will be used.
func WithFallbackValue(fallbackValue bool, reason string, message string) MirrorOptions {
	return func(c *mirrorOptions) {
		c.fallbackTo = &fallbackValue
		c.fallbackReason = reason
		c.fallbackMessage = message
	}
}

// mirror mirrors the Ready condition from a dependent object into the target condition; if the Ready condition does not
// exists in the source object, no target conditions is generated.
func mirror(from Getter, targetCondition string, options ...MirrorOptions) *metav1.Condition {
	mirrorOpt := &mirrorOptions{}
	for _, o := range options {
		o(mirrorOpt)
	}

	condition := Get(from, meta.ReadyCondition)

	if mirrorOpt.fallbackTo != nil && condition == nil {
		switch *mirrorOpt.fallbackTo {
		case true:
			condition = TrueCondition(targetCondition, mirrorOpt.fallbackReason, "%s", mirrorOpt.fallbackMessage)
		case false:
			condition = FalseCondition(targetCondition, mirrorOpt.fallbackReason, "%s", mirrorOpt.fallbackMessage)
		}
	}

	if condition != nil {
		condition.Type = targetCondition
	}

	return condition
}

// aggregate the conditions from a list of depending objects into the target object; the condition scope can be set
// using WithConditions; if none of the source objects have the conditions within the scope, no target condition is
// generated.
func aggregate(from []Getter, targetCondition string, options ...MergeOption) *metav1.Condition {
	mergeOpt := &mergeOptions{
		stepCounter: len(from),
	}
	for _, o := range options {
		o(mergeOpt)
	}

	conditionsInScope := make([]localizedCondition, 0, len(from))
	for i := range from {
		conditions := from[i].GetConditions()
		for i, _ := range conditions {
			c := conditions[i]
			if mergeOpt.conditionTypes != nil {
				found := false
				for _, tt := range mergeOpt.conditionTypes {
					if c.Type == tt {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}

			conditionsInScope = append(conditionsInScope, localizedCondition{
				Condition: &c,
				Getter:    from[i],
			})
		}
	}

	// If it is required to add a counter only if a subset of condition exists, check if the conditions
	// in scope are included in this subset or not.
	if mergeOpt.addCounterOnlyIfConditionTypes != nil {
		for _, c := range conditionsInScope {
			found := false
			for _, tt := range mergeOpt.addCounterOnlyIfConditionTypes {
				if c.Type == tt {
					found = true
					break
				}
			}
			if !found {
				mergeOpt.addCounter = false
				break
			}
		}
	}

	// If it is required to add a source ref only if a condition type exists, check if the conditions
	// in scope are included in this subset or not.
	if mergeOpt.addSourceRefIfConditionTypes != nil {
		for _, c := range conditionsInScope {
			found := false
			for _, tt := range mergeOpt.addSourceRefIfConditionTypes {
				if c.Type == tt {
					found = true
					break
				}
			}
			if found {
				mergeOpt.addSourceRef = true
				break
			}
		}
	}

	return merge(conditionsInScope, targetCondition, mergeOpt)
}
