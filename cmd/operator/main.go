// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	whereaboutsv1alpha1 "github.com/k8snetworkplumbingwg/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/version"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(whereaboutsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(nadv1.AddToScheme(scheme))
}

func main() {
	rootCmd := newRootCommand()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "whereabouts-operator",
		Short:   "Whereabouts IPAM operator",
		Version: version.GetFullVersionWithRuntimeInfo(),
	}

	cmd.PersistentFlags().String("log-level", "info", "Log level (debug, info, warn, error)")

	cmd.AddCommand(
		newControllerCommand(),
	)

	return cmd
}

func setupLogger(cmd *cobra.Command) {
	logLevel, err := cmd.Root().PersistentFlags().GetString("log-level")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read log-level flag: %v\n", err)
		return
	}

	var opts zap.Options
	switch logLevel {
	case "debug":
		opts.Development = true
		opts.Level = zapcore.DebugLevel
	case "info":
		opts.Development = false
		opts.Level = zapcore.InfoLevel
	case "warn":
		opts.Development = false
		opts.Level = zapcore.WarnLevel
	case "error":
		opts.Development = false
		opts.Level = zapcore.ErrorLevel
	default:
		fmt.Fprintf(os.Stderr, "unrecognized log-level %q, defaulting to info\n", logLevel)
		opts.Development = false
		opts.Level = zapcore.InfoLevel
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
}
