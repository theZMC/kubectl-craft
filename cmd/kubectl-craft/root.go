package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/thezmc/kubectl-craft/internal/data"
)

// sessionShell launches the presentation layer for a Session whose live
// index serves groupCount API groups. Production wiring passes tui.Run;
// command specs inject a recorder so they can observe the launch without
// a controlling terminal.
type sessionShell func(ctx context.Context, groupCount int) error

// newRootCommand wires the standard kubectl plugin flags
// (genericclioptions.ConfigFlags: --context, --kubeconfig, --namespace, …)
// into the root command. The context those flags resolve is fixed for the
// Session — switching clusters means starting a new Session.
func newRootCommand(shell sessionShell) *cobra.Command {
	configFlags := genericclioptions.NewConfigFlags(true)

	cmd := &cobra.Command{
		Use:           "kubectl-craft",
		Short:         "Compose Kubernetes Manifests from your cluster's live Type Schemas",
		Version:       version,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSession(cmd.Context(), configFlags, shell)
		},
	}

	configFlags.AddFlags(cmd.Flags())

	return cmd
}

// runSession connects to the cluster the Session's resolved context points
// to, fetches the live OpenAPI v3 index, and only then launches the Session
// shell. An unreachable cluster or a cluster that doesn't serve OpenAPI v3
// hard-fails here — before the alt screen ever opens — surfacing on stderr
// as a non-zero exit (DESIGN.md — Data layer).
func runSession(
	ctx context.Context,
	configFlags *genericclioptions.ConfigFlags,
	shell sessionShell,
) error {
	restConfig, err := configFlags.ToRESTConfig()
	if err != nil {
		return fmt.Errorf("loading kubeconfig: %w", err)
	}

	client, err := data.NewClient(restConfig)
	if err != nil {
		return fmt.Errorf("preparing the cluster client: %w", err)
	}

	groups, err := client.FetchIndex(ctx)
	if err != nil {
		return fmt.Errorf("connecting the Session to the cluster: %w", err)
	}

	return shell(ctx, len(groups))
}
