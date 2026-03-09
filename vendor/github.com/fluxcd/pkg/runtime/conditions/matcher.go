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
https://github.com/kubernetes-sigs/cluster-api/tree/7478817225e0a75acb6e14fc7b438231578073d2/util/conditions/matcher.go,
and initially adapted to work with the `metav1.Condition` and `metav1.ConditionStatus` types.
More concretely, this includes the removal of "condition severity" related functionalities, as this is not supported by
the `metav1.Condition` type.
*/

package conditions

import (
	"fmt"

	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MatchConditions returns a custom matcher to check equality of a metav1.Condition slice, the condition messages are
// checked for a subset string match.
func MatchConditions(expected []metav1.Condition) types.GomegaMatcher {
	return &matchConditions{
		expected: expected,
	}
}

type matchConditions struct {
	expected []metav1.Condition
}

func (m matchConditions) Match(actual interface{}) (success bool, err error) {
	elems := []interface{}{}
	for _, condition := range m.expected {
		elems = append(elems, MatchCondition(condition))
	}
	return ConsistOf(elems).Match(actual)
}

func (m matchConditions) FailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("expected\n\t%#v\nto match\n\t%#v\n", actual, m.expected)
}

func (m matchConditions) NegatedFailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("expected\n\t%#v\nto not match\n\t%#v\n", actual, m.expected)
}

// MatchCondition returns a custom matcher to check equality of metav1.Condition.
func MatchCondition(expected metav1.Condition) types.GomegaMatcher {
	return &matchCondition{
		expected: expected,
	}
}

type matchCondition struct {
	expected metav1.Condition
}

func (m matchCondition) Match(actual interface{}) (success bool, err error) {
	actualCondition, ok := actual.(metav1.Condition)
	if !ok {
		return false, fmt.Errorf("actual should be of type Condition")
	}

	ok, err = Equal(m.expected.Type).Match(actualCondition.Type)
	if !ok {
		return ok, err
	}
	ok, err = Equal(m.expected.Status).Match(actualCondition.Status)
	if !ok {
		return ok, err
	}
	ok, err = Equal(m.expected.Reason).Match(actualCondition.Reason)
	if !ok {
		return ok, err
	}
	ok, err = ContainSubstring(m.expected.Message).Match(actualCondition.Message)
	if !ok {
		return ok, err
	}

	return ok, err
}

func (m matchCondition) FailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("expected\n\t%#v\nto match\n\t%#v\n", actual, m.expected)
}

func (m matchCondition) NegatedFailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("expected\n\t%#v\nto not match\n\t%#v\n", actual, m.expected)
}
