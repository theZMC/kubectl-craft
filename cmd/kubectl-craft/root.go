package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/thezmc/kubectl-craft/internal/data"
)

// sessionShell launches the presentation layer for a Session over what the
// Session resolved before the shell starts: the cluster's browsable Kind
// list from discovery, the Fetcher sourcing OpenAPI v3 group documents
// (the disk cache over the live client in production — ADR-0002), and the
// live /openapi/v3 index whose content hashes address every lazy fetch.
// Production wiring passes tui.Run; command specs inject a recorder so
// they can observe the launch without a controlling terminal.
type sessionShell func(ctx context.Context, kinds []data.Kind, fetcher data.Fetcher, index []data.GroupVersion) error

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
// to, wraps the live client in the hash-validated disk cache (ADR-0002),
// fetches the live OpenAPI v3 index, discovers the browsable Kind list,
// and only then launches the Session shell on the Kind picker. An
// unreachable cluster, a cluster that doesn't serve OpenAPI v3, or a
// failed discovery hard-fails here — before the alt screen ever opens —
// surfacing on stderr as a non-zero exit (DESIGN.md — Data layer). Group
// documents are not fetched here: they load lazily, per group, on the
// first open of one of its Kinds.
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

	fetcher := sessionFetcher(client, restConfig.Host)

	index, err := fetcher.FetchIndex(ctx)
	if err != nil {
		return fmt.Errorf("connecting the Session to the cluster: %w", err)
	}

	lister, err := data.NewKindLister(restConfig)
	if err != nil {
		return fmt.Errorf("preparing the Session's Kind discovery: %w", err)
	}

	kinds, err := data.DiscoverKinds(lister)
	if err != nil {
		return fmt.Errorf("listing the Session's browsable Kinds: %w", err)
	}

	return shell(ctx, kinds, fetcher, index)
}

// sessionFetcher wraps the live client in the disk cache rooted at the
// user cache directory, keyed by the Session's cluster host (ADR-0002).
// The cache is a speedup, never a gate: when the user cache directory
// cannot be resolved, the Session simply runs on the live client alone.
func sessionFetcher(client *data.Client, serverHost string) data.Fetcher {
	cacheRoot, err := data.DefaultCacheRoot()
	if err != nil {
		return client
	}
	return data.NewCache(client, cacheRoot, serverHost)
}
