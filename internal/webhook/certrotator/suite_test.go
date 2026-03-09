// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package certrotator

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCertRotator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CertRotator Suite")
}
