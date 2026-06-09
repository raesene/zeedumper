package dumper

import (
	"context"
	"fmt"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// AllComponents is the default set dumped when the user does not restrict it.
// Order is the display order.
var AllComponents = []string{
	"kube-apiserver",
	"kube-controller-manager",
	"kube-scheduler",
	"kube-proxy",
	"kubelet",
}

// target is a discovered component instance plus the proxy base path used to
// reach its z-pages. A page path is basePath + "/" + pageName.
type target struct {
	instance string // node name, pod name, or "apiserver"
	basePath string // "" means the API server's own paths (e.g. /flagz)
	pages    []string
}

// componentSpec describes how to discover a component's instances and which
// pages it serves.
type componentSpec struct {
	pages    []string
	discover func(ctx context.Context, cs kubernetes.Interface, namespace string) ([]target, error)
}

// specs maps component name -> how to dump it. All paths go through the API
// server proxy, so the only credentials in play are the user's kubeconfig.
var specs = map[string]componentSpec{
	"kube-apiserver": {
		pages: []string{"flagz", "statusz"},
		discover: func(_ context.Context, _ kubernetes.Interface, _ string) ([]target, error) {
			// The API server serves its own z-pages directly; no proxy needed.
			return []target{{
				instance: "apiserver",
				basePath: "",
				pages:    []string{"flagz", "statusz"},
			}}, nil
		},
	},
	"kube-controller-manager": {
		pages:    []string{"flagz", "statusz"},
		discover: podProxyDiscoverer("component=kube-controller-manager", "https", 10257, []string{"flagz", "statusz"}),
	},
	"kube-scheduler": {
		pages:    []string{"flagz", "statusz"},
		discover: podProxyDiscoverer("component=kube-scheduler", "https", 10259, []string{"flagz", "statusz"}),
	},
	"kube-proxy": {
		pages:    []string{"flagz", "statusz"},
		discover: podProxyDiscoverer("k8s-app=kube-proxy", "http", 10249, []string{"flagz", "statusz"}),
	},
	"kubelet": {
		pages:    []string{"flagz", "statusz", "configz"},
		discover: discoverKubelets,
	},
}

// podProxyDiscoverer builds a discoverer that lists pods matching labelSelector
// in the given namespace and proxies to each over the supplied scheme/port.
func podProxyDiscoverer(labelSelector, scheme string, port int, pages []string) func(context.Context, kubernetes.Interface, string) ([]target, error) {
	return func(ctx context.Context, cs kubernetes.Interface, namespace string) ([]target, error) {
		pods, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
		if err != nil {
			return nil, fmt.Errorf("listing pods (%s): %w", labelSelector, err)
		}
		var targets []target
		for _, pod := range pods.Items {
			// https endpoints need the "https:" scheme prefix in the proxy
			// path; http endpoints use bare name:port.
			var hostPort string
			if scheme == "https" {
				hostPort = fmt.Sprintf("https:%s:%d", pod.Name, port)
			} else {
				hostPort = fmt.Sprintf("%s:%d", pod.Name, port)
			}
			targets = append(targets, target{
				instance: pod.Name,
				basePath: fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/proxy", namespace, hostPort),
				pages:    pages,
			})
		}
		if len(targets) == 0 {
			return nil, fmt.Errorf("no pods matched selector %q in namespace %q", labelSelector, namespace)
		}
		return targets, nil
	}
}

// discoverKubelets lists nodes and proxies to each kubelet's read-only-ish
// secure port (10250) via the nodes/proxy subresource.
func discoverKubelets(ctx context.Context, cs kubernetes.Interface, _ string) ([]target, error) {
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}
	var targets []target
	for _, node := range nodes.Items {
		targets = append(targets, target{
			instance: node.Name,
			basePath: fmt.Sprintf("/api/v1/nodes/%s:10250/proxy", node.Name),
			pages:    []string{"flagz", "statusz", "configz"},
		})
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no nodes found")
	}
	return targets, nil
}

// ResolveComponents validates the requested component names against the known
// set, returning them in canonical display order.
func ResolveComponents(requested []string) ([]string, error) {
	if len(requested) == 0 {
		return AllComponents, nil
	}
	known := make(map[string]bool, len(AllComponents))
	for _, c := range AllComponents {
		known[c] = true
	}
	seen := make(map[string]bool, len(requested))
	for _, c := range requested {
		if !known[c] {
			return nil, fmt.Errorf("unknown component %q (known: %v)", c, AllComponents)
		}
		seen[c] = true
	}
	var out []string
	for _, c := range AllComponents { // preserve canonical order
		if seen[c] {
			out = append(out, c)
		}
	}
	return out, nil
}

// filterPages returns the intersection of a target's pages with the requested
// page filter, preserving the target's order. An empty filter means all pages.
func filterPages(targetPages, requested []string) []string {
	if len(requested) == 0 {
		return targetPages
	}
	want := make(map[string]bool, len(requested))
	for _, p := range requested {
		want[p] = true
	}
	var out []string
	for _, p := range targetPages {
		if want[p] {
			out = append(out, p)
		}
	}
	return out
}

// sortInstances gives deterministic output regardless of list ordering.
func sortInstances(insts []Instance) {
	sort.Slice(insts, func(i, j int) bool { return insts[i].Name < insts[j].Name })
}
