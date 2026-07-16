// Package k8s builds a Kubernetes clientset from the user's kubeconfig using
// the standard client-go loading rules (honouring --kubeconfig, $KUBECONFIG and
// the in-cluster fallback). The clientset's REST client is the transport used to
// reach component z-pages through the API server proxy.
package k8s

import (
	"fmt"
	"strconv"
	"strings"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Client wraps a clientset together with the cluster identity it connected to,
// so output can be labelled with which cluster was dumped.
type Client struct {
	Clientset *kubernetes.Clientset
	Context   string
	Host      string
}

// New builds a Client from the supplied kubeconfig path. An empty path falls
// back to the default loading rules ($KUBECONFIG, then ~/.kube/config).
func New(kubeconfigPath string) (*Client, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		loadingRules.ExplicitPath = kubeconfigPath
	}

	configOverrides := &clientcmd.ConfigOverrides{}
	deferred := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	restConfig, err := deferred.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("building clientset: %w", err)
	}

	// Best-effort: record which context/cluster we ended up talking to.
	var contextName string
	if rawConfig, err := deferred.RawConfig(); err == nil {
		contextName = rawConfig.CurrentContext
	}

	return &Client{
		Clientset: clientset,
		Context:   contextName,
		Host:      restConfig.Host,
	}, nil
}

// ServerMinorVersion returns the Kubernetes minor version of the connected
// cluster (e.g. 36 for v1.36.x). The Minor field from the discovery API
// sometimes has a trailing "+" which is stripped before parsing.
func (c *Client) ServerMinorVersion() (int, error) {
	info, err := c.Clientset.Discovery().ServerVersion()
	if err != nil {
		return 0, fmt.Errorf("fetching server version: %w", err)
	}

	minor := strings.TrimRight(info.Minor, "+")

	v, err := strconv.Atoi(minor)
	if err != nil {
		return 0, fmt.Errorf("parsing minor version %q: %w", info.Minor, err)
	}

	return v, nil
}
