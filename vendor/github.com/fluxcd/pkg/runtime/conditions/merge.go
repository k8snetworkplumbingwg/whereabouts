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
https://github.com/kubernetes-sigs/cluster-api/tree/7478817225e0a75acb6e14fc7b438231578073d2/util/conditions/merge.go,
and initially adapted to work with the `metav1.Condition` and `metav1.ConditionStatus` types.
More concretely, this includes the removal of "condition severity" related functionalities, as this is not supported by
the `metav1.Condition` type.
*/

package conditions

import (
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// localizedCondition defines a condition with the information of the object the conditions was originated from.
type localizedCondition struct {
	*metav1.Condition
	Getter
}

// merge a list of condition into a single one.
// This operation is designed to ensure visibility of the most relevant conditions for defining the operational state of
// a component. E.g. If there is one error in the condition list, this one takes priority over the other conditions and
// it is should be reflected in the target condition.
//
// More specifically:
// 1. Conditions are grouped by status, polarity and observed generation (optional).
// 2. The resulting condition groups are sorted according to the following priority:
//   - P0 - Status=True, NegativePolarity=True
//   - P1 - Status=False, NegativePolarity=False
//   - P2 - Status=True, NegativePolarity=False
//   - P3 - Status=False, NegativePolarity=True
//   - P4 - Status=Unknown
//
// 3. The group with highest priority is used to determine status, and other info of the target condition.
// 4. If the polarity of the highest priority and target priority differ, it is inverted.
// 5. If the observed generation is considered, the condition groups with the latest generation get the highest
// priority.
//
// Please note that the last operation includes also the task of computing the Reason and the Message for the target
// condition; in order to complete such task some trade-off should be made, because there is no a golden rule for
// summarizing many Reason/Message into single Reason/Message. mergeOptions allows the user to adapt this process to the
// specific needs by exposing a set of merge strategies.
func merge(conditions []localizedCondition, targetCondition string, options *mergeOptions) *metav1.Condition {
	g := getConditionGroups(conditions, options)
	if len(g) == 0 {
		return nil
	}

	topGroup := g.TopGroup()
	targetReason := getReason(g, options)
	targetMessage := getMessage(g, options)
	targetNegativePolarity := stringInSlice(options.negativePolarityConditionTypes, targetCondition)

	switch topGroup.status {
	case metav1.ConditionTrue:
		// Inverse the negative polarity if the target condition has positive polarity.
		if topGroup.negativePolarity != targetNegativePolarity {
			return FalseCondition(targetCondition, targetReason, "%s", targetMessage)
		}
		return TrueCondition(targetCondition, targetReason, "%s", targetMessage)
	case metav1.ConditionFalse:
		// Inverse the negative polarity if the target condition has positive polarity.
		if topGroup.negativePolarity != targetNegativePolarity {
			return TrueCondition(targetCondition, targetReason, "%s", targetMessage)
		}
		return FalseCondition(targetCondition, targetReason, "%s", targetMessage)
	default:
		return UnknownCondition(targetCondition, targetReason, "%s", targetMessage)
	}
}

// getConditionGroups groups a list of conditions according to status values and polarity.
// Additionally, the resulting groups are sorted by mergePriority.
func getConditionGroups(conditions []localizedCondition, options *mergeOptions) conditionGroups {
	groups := conditionGroups{}

	for _, condition := range conditions {
		if condition.Condition == nil {
			continue
		}

		added := false
		for i := range groups {
			if groups[i].status == condition.Status &&
				groups[i].negativePolarity == stringInSlice(options.negativePolarityConditionTypes, condition.Type) {
				// If withLatestGeneration is true, add to group only if the generation match.
				if options.withLatestGeneration && groups[i].generation != condition.ObservedGeneration {
					continue
				}
				groups[i].conditions = append(groups[i].conditions, condition)
				added = true
				break
			}
		}
		if !added {
			groups = append(groups, conditionGroup{
				conditions:       []localizedCondition{condition},
				status:           condition.Status,
				negativePolarity: stringInSlice(options.negativePolarityConditionTypes, condition.Type),
				generation:       condition.ObservedGeneration,
			})
		}
	}

	// If withLatestGeneration is true, form a conditionGroups of the groups
	// with the latest generation.
	if options.withLatestGeneration {
		latestGen := groups.latestGeneration()
		latestGroups := conditionGroups{}
		for _, g := range groups {
			if g.generation == latestGen {
				latestGroups = append(latestGroups, g)
			}
		}
		groups = latestGroups
	}

	// sort groups by priority
	sort.Sort(groups)

	// sorts conditions in the TopGroup so we ensure predictable result for merge strategies.
	// condition are sorted using the same lexicographic order used by Set; in case two conditions
	// have the same type, condition are sorted using according to the alphabetical order of the source object name.
	if len(groups) > 0 {
		sort.Slice(groups[0].conditions, func(i, j int) bool {
			a := groups[0].conditions[i]
			b := groups[0].conditions[j]
			if a.Type != b.Type {
				return lexicographicLess(a.Condition, b.Condition)
			}
			return a.GetName() < b.GetName()
		})
	}

	return groups
}

// conditionGroups provides supports for grouping a list of conditions to be merged into a single condition.
// ConditionGroups can be sorted by mergePriority.
type conditionGroups []conditionGroup

func (g conditionGroups) Len() int {
	return len(g)
}

func (g conditionGroups) Less(i, j int) bool {
	return g[i].mergePriority() < g[j].mergePriority()
}

func (g conditionGroups) Swap(i, j int) {
	g[i], g[j] = g[j], g[i]
}

// TopGroup returns the the condition group with the highest mergePriority.
func (g conditionGroups) TopGroup() *conditionGroup {
	if len(g) == 0 {
		return nil
	}
	return &g[0]
}

// TruePositivePolarityGroup returns the the condition group with status True/Positive, if any.
func (g conditionGroups) TruePositivePolarityGroup() *conditionGroup {
	if g.Len() == 0 {
		return nil
	}
	for _, group := range g {
		if !group.negativePolarity && group.status == metav1.ConditionTrue {
			return &group
		}
	}
	return nil
}

// latestGeneration returns the latest generation of the conditionGroups.
func (g conditionGroups) latestGeneration() int64 {
	var max int64
	for _, group := range g {
		if group.generation > max {
			max = group.generation
		}
	}
	return max
}

// conditionGroup defines a group of conditions with the same metav1.ConditionStatus, polarity and observed generation, and thus with the
// same priority when merging into a condition.
type conditionGroup struct {
	status           metav1.ConditionStatus
	negativePolarity bool
	conditions       []localizedCondition
	generation       int64
}

// mergePriority provides a priority value for the status and polarity tuple that identifies this condition group. The
// mergePriority value allows an easier sorting of conditions groups.
func (g conditionGroup) mergePriority() (p int) {
	switch g.status {
	case metav1.ConditionTrue:
		p = 0
		if !g.negativePolarity {
			p = 2
		}
		return
	case metav1.ConditionFalse:
		p = 1
		if g.negativePolarity {
			p = 3
		}
		return
	case metav1.ConditionUnknown:
		return 4
	default:
		return 99
	}
}

func stringInSlice(s []string, val string) bool {
	for _, s := range s {
		if s == val {
			return true
		}
	}
	return false
}
