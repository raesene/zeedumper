package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/raesene/zeedumper/internal/dumper"
	"github.com/raesene/zeedumper/internal/k8s"
	"github.com/raesene/zeedumper/internal/output"
)

const envPrefix = "ZEEDUMPER"

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "zeedumper",
		Short: "Dump Kubernetes component z-pages (flagz, statusz, configz)",
		Long: `zeedumper connects to a Kubernetes cluster using your kubeconfig and
retrieves component z-pages through the API server proxy.

By default it dumps every supported component:
  kube-apiserver, kube-controller-manager, kube-scheduler, kube-proxy, kubelet

Each exposes flagz and statusz; the kubelet additionally exposes configz.
Pages that are gated off or blocked by RBAC are reported as per-page errors
rather than failing the whole run.`,
		Example: `  # Dump everything as text
  zeedumper

  # Only the API server and scheduler, as JSON
  zeedumper --components kube-apiserver,kube-scheduler -o json

  # Only configz pages, written to an HTML file
  zeedumper --pages configz -o html -f dump.html`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return initConfig(cmd)
		},
		RunE: runDump,
	}

	flags := cmd.Flags()
	flags.String("kubeconfig", "", "path to kubeconfig file (defaults to $KUBECONFIG or ~/.kube/config)")
	flags.StringSlice("components", nil, "components to dump (default: all)")
	flags.StringSlice("pages", nil, "z-pages to include, e.g. flagz,statusz,configz (default: all applicable)")
	flags.StringP("output", "o", "text", "output format: text, json, or html")
	flags.StringP("output-file", "f", "", "write output to this file instead of stdout")
	flags.String("namespace", "kube-system", "namespace for control-plane pods and temporary node-agent resources")
	flags.Duration("timeout", 15*time.Second, "per-page request timeout")
	flags.Bool("no-node-pods", false, "disable the node-agent strategy; reach loopback-bound components (controller-manager, scheduler, kube-proxy) via the API proxy only (usually fails)")
	flags.String("node-pod-image", "curlimages/curl:latest", "container image used for temporary node-agent pods")

	cmd.AddCommand(newVersionCmd())

	return cmd
}

// initConfig wires viper to the command's flags and environment so that any
// flag can also be supplied as ZEEDUMPER_<FLAG>.
func initConfig(cmd *cobra.Command) error {
	v := viper.New()
	v.SetEnvPrefix(envPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()

	var bindErr error

	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if err := v.BindPFlag(f.Name, f); err != nil {
			bindErr = err
			return
		}
		// Apply values from env/config when the flag was not set explicitly.
		if !f.Changed && v.IsSet(f.Name) {
			if err := cmd.Flags().Set(f.Name, fmt.Sprintf("%v", v.Get(f.Name))); err != nil {
				bindErr = err
			}
		}
	})

	return bindErr
}

func runDump(cmd *cobra.Command, _ []string) error {
	flags := cmd.Flags()
	kubeconfig, _ := flags.GetString("kubeconfig")
	components, _ := flags.GetStringSlice("components")
	pages, _ := flags.GetStringSlice("pages")
	outputFormat, _ := flags.GetString("output")
	outputFile, _ := flags.GetString("output-file")
	namespace, _ := flags.GetString("namespace")
	timeout, _ := flags.GetDuration("timeout")
	noNodePods, _ := flags.GetBool("no-node-pods")
	nodePodImage, _ := flags.GetString("node-pod-image")

	format, err := output.ParseFormat(outputFormat)
	if err != nil {
		return err
	}
	// Validate component names before touching the network.
	if _, err := dumper.ResolveComponents(components); err != nil {
		return err
	}

	client, err := k8s.New(kubeconfig)
	if err != nil {
		return err
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "connecting to %s\n", client.Host)

	dump, err := dumper.Run(context.Background(), client, dumper.Options{
		Components:   components,
		Pages:        pages,
		Namespace:    namespace,
		Timeout:      timeout,
		UseNodePods:  !noNodePods,
		NodePodImage: nodePodImage,
	})
	if err != nil {
		return err
	}

	if outputFile == "" {
		return output.Render(cmd.OutOrStdout(), dump, format)
	}

	f, err := os.Create(outputFile) //nolint:gosec // G304: the output path is a user-supplied CLI flag by design.
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}

	if rerr := output.Render(f, dump, format); rerr != nil {
		_ = f.Close()

		return rerr
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("closing output file: %w", err)
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "wrote %s output to %s\n", format, outputFile)

	return nil
}

// Execute runs the root command and maps errors to exit codes.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
