package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/thezmc/kubectl-craft/internal/data"
	"github.com/thezmc/kubectl-craft/internal/tui"
)

// sessionShell launches the presentation layer for a Session over what the
// Session resolved before the shell starts: the cluster's browsable Kind
// list from discovery, the Fetcher sourcing OpenAPI v3 group documents
// (the disk cache over the live client in production — ADR-0002), the
// live /openapi/v3 index whose content hashes address every lazy fetch,
// and the launch arg's resolved deep link when one was given (nil opens
// the Kind picker). It returns the Session's Result — the emit decision
// and the Emitted Manifest's bytes — once the alt screen has closed.
// Production wiring passes tui.Run; command specs inject a recorder so
// they can observe the launch without a controlling terminal.
type sessionShell func(
	ctx context.Context,
	kinds []data.Kind,
	fetcher data.Fetcher,
	index []data.GroupVersion,
	link *tui.DeepLink,
) (tui.Result, error)

// rootLong documents the deep-link arg in kubectl explain's syntax: the
// launch surface DESIGN.md — Flow §1 promises, and the k9s-plugin
// integration hook.
const rootLong = `Compose Kubernetes Manifests from your cluster's live Type Schemas.

With no argument the Session opens on the Kind picker. An optional
positional argument in kubectl-explain syntax deep-links straight to a
Kind — and optionally a Field Path within it — skipping the picker. The
kind token may be the Kind name, its plural, or a short name, matched
case-insensitively and resolved against the cluster's discovery to the
Kind's Preferred Version.`

// rootExample shows both deep-link forms alongside the bare launch.
const rootExample = `  # open the Kind picker
  kubectl craft

  # deep-link to apps/v1 Deployment (short name, resolved via discovery)
  kubectl craft deploy

  # deep-link to a Field Path inside the Deployment's Type Schema
  kubectl craft deploy.spec.strategy`

// newRootCommand wires the standard kubectl plugin flags
// (genericclioptions.ConfigFlags: --context, --kubeconfig, --namespace, …)
// into the root command. The context those flags resolve is fixed for the
// Session — switching clusters means starting a new Session.
func newRootCommand(shell sessionShell) *cobra.Command {
	configFlags := genericclioptions.NewConfigFlags(true)

	cmd := &cobra.Command{
		Use:           "kubectl-craft [kind[.field.path]]",
		Short:         "Compose Kubernetes Manifests from your cluster's live Type Schemas",
		Long:          rootLong,
		Example:       rootExample,
		Version:       version,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSession(cmd.Context(), configFlags, shell, deepLinkArg(args), cmd.OutOrStdout())
		},
	}

	configFlags.AddFlags(cmd.Flags())

	return cmd
}

// deepLinkArg is the optional positional deep-link arg; empty when the
// Session should open on the Kind picker.
func deepLinkArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// runSession connects to the cluster the Session's resolved context points
// to, wraps the live client in the hash-validated disk cache (ADR-0002),
// fetches the live OpenAPI v3 index, discovers the browsable Kind list,
// resolves the deep-link arg against it when one was given, and only then
// launches the Session shell. An unreachable cluster, a cluster that
// doesn't serve OpenAPI v3, a failed discovery, or an unresolvable
// deep-link kind token hard-fails here — before the alt screen ever opens
// — surfacing on stderr as a non-zero exit (DESIGN.md — Data layer). Group
// documents are not fetched here: they load lazily, per group, on the
// first open of one of its Kinds.
//
// When the Session ends on an emit ramp, the Emitted Manifest's bytes are
// written to stdout here — after the shell has returned and the alt screen
// has closed, never from inside the TUI (DESIGN.md — Output) — so
// `kubectl craft > x.yaml` captures exactly the Manifest. A discard ramp
// writes nothing, and every ramp exits zero.
func runSession(
	ctx context.Context,
	configFlags *genericclioptions.ConfigFlags,
	shell sessionShell,
	arg string,
	stdout io.Writer,
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

	var link *tui.DeepLink
	if arg != "" {
		if link, err = resolveDeepLink(kinds, arg); err != nil {
			return err
		}
	}

	result, err := shell(ctx, kinds, fetcher, index, link)
	if err != nil {
		return err
	}
	if !result.Emitted {
		return nil
	}
	if _, err := stdout.Write(result.Manifest); err != nil {
		return fmt.Errorf("writing the Emitted Manifest to stdout: %w", err)
	}
	return nil
}

// resolveDeepLink resolves the positional deep-link arg — kubectl-explain
// syntax, kind[.field.path] — against the discovered browsable Kinds: the
// first dot-segment is the kind token, the remainder a schema-level Field
// Path. An unknown kind token hard-fails here, before the alt screen
// opens, like every other pre-flight failure. The Field Path is not
// validated here on purpose: a path the Type Schema doesn't define is the
// compose view's non-fatal notice, so the Kind stays browsable.
func resolveDeepLink(kinds []data.Kind, arg string) (*tui.DeepLink, error) {
	token, fieldPath, _ := strings.Cut(arg, ".")

	kind, err := data.ResolveKindToken(kinds, token)
	if err != nil {
		return nil, fmt.Errorf("resolving the deep-link argument %q: %w", arg, err)
	}

	return &tui.DeepLink{Kind: kind, FieldPath: fieldPath}, nil
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
