// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"

	"k8s.io/kube-openapi/pkg/validation/spec"

	generatedopenapi "github.com/k8snetworkplumbingwg/whereabouts/pkg/generated/openapi"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ref := func(path string) spec.Ref {
		return spec.MustCreateRef("#/definitions/" + path)
	}
	defs := generatedopenapi.GetOpenAPIDefinitions(ref)

	out := make(spec.Definitions, len(defs))
	for k := range defs {
		out[k] = defs[k].Schema
	}

	swagger := spec.Swagger{
		SwaggerProps: spec.SwaggerProps{
			Swagger:     "2.0",
			Info:        &spec.Info{InfoProps: spec.InfoProps{Title: "Whereabouts", Version: "v1alpha1"}},
			Paths:       &spec.Paths{},
			Definitions: out,
		},
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(swagger)
}
