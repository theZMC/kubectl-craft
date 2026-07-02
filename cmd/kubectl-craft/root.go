package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/thezmc/kubectl-craft/internal/data"
)

// newRootCommand wires the standard kubectl plugin flags
// (genericclioptions.ConfigFlags: --context, --kubeconfig, --namespace, …)
// into the root command. The context those flags resolve is fixed for the
// Session — switching clusters means starting a new Session.
func newRootCommand(streams genericiooptions.IOStreams) *cobra.Command {
	configFlags := genericclioptions.NewConfigFlags(true)

	cmd := &cobra.Command{
		Use:           "kubectl-craft",
		Short:         "Compose Kubernetes Manifests from your cluster's live Type Schemas",
		Version:       version,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSession(cmd.Context(), configFlags, streams)
		},
	}

	configFlags.AddFlags(cmd.Flags())

	return cmd
}

// runSession connects to the cluster the Session's resolved context points
// to and fetches the live OpenAPI v3 index. An unreachable cluster or a
// cluster that doesn't serve OpenAPI v3 hard-fails here — before any TUI
// could launch — surfacing as a non-zero exit (DESIGN.md — Data layer).
func runSession(
	ctx context.Context,
	configFlags *genericclioptions.ConfigFlags,
	streams genericiooptions.IOStreams,
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

	// Plain output until the TUI lands (separate M0 issue): prove the
	// data layer end-to-end by reporting what the live index serves.
	_, _ = fmt.Fprintln(streams.Out, placeholder())
	_, _ = fmt.Fprintf(streams.Out,
		"connected: %d group versions serve OpenAPI v3 Documents on this cluster\n",
		len(groups))

	return nil
}
