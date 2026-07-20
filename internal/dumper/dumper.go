// Package dumper discovers Kubernetes components and retrieves their z-pages,
// either through the API server proxy or, for loopback-bound components, via a
// temporary host-network node agent.
package dumper

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/raesene/zeedumper/internal/k8s"
)

// Options controls a dump run.
type Options struct {
	Components []string      // component names to dump; empty == all
	Pages      []string      // page names to include; empty == all applicable
	Namespace  string        // namespace holding control-plane pods (default kube-system)
	Timeout    time.Duration // per-page request timeout
	Now        time.Time     // run timestamp (injected for testability)

	// UseNodePods enables the node-agent strategy for loopback-bound components
	// (controller-manager, scheduler, kube-proxy): a temporary host-network pod
	// and RBAC are created on each node and removed afterwards. When false,
	// those components fall back to the (usually failing) API-server proxy.
	UseNodePods  bool
	NodePodImage string // container image for agent pods
}

const defaultNodePodImage = "curlimages/curl:latest"

func (o Options) nodePodImage() string {
	if o.NodePodImage != "" {
		return o.NodePodImage
	}

	return defaultNodePodImage
}

// runID is a short, run-unique suffix for the temporary resource names.
func (o Options) runID() string {
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}

	return fmt.Sprintf("%x", now.UnixNano())[:8]
}

// Run discovers the requested components and fetches their z-pages through the
// API server proxy. Per-page failures are captured on the Page rather than
// aborting the run, so a partially-gated cluster still yields useful output.
func Run(ctx context.Context, client *k8s.Client, opts Options) (*Dump, error) {
	components, err := ResolveComponents(opts.Components)
	if err != nil {
		return nil, err
	}

	namespace := opts.Namespace
	if namespace == "" {
		namespace = "kube-system"
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	dump := &Dump{
		Cluster:   client.Host,
		Context:   client.Context,
		Timestamp: now.UTC().Format(time.RFC3339),
	}

	// Loopback-bound components are gathered via the node-agent strategy when
	// enabled; results are merged back into canonical order below.
	nodeAgentResults := map[string]Component{}

	if opts.UseNodePods {
		gathered, err := gatherViaNodePods(ctx, client, components, opts.Pages, opts)
		if err != nil {
			return nil, err
		}

		for _, comp := range gathered {
			nodeAgentResults[comp.Name] = comp
		}
	}

	for _, name := range components {
		// Loopback components are wholly owned by the node agent. The kubelet is
		// an exception: it is reached via the API proxy for flagz/configz and
		// only has statusz supplied by the node agent, so it is merged in
		// fetchComponent rather than replaced.
		if comp, ok := nodeAgentResults[name]; ok && name != "kubelet" {
			dump.Components = append(dump.Components, comp)
			continue
		}

		dump.Components = append(dump.Components, fetchComponent(ctx, client, name, namespace, opts, nodeAgentResults))
	}

	return dump, nil
}

// fetchComponent discovers a component's instances and retrieves their z-pages
// through the API server proxy, then grafts on any node-agent-fetched pages
// (the kubelet's statusz) so a component split across both strategies is
// presented as one.
func fetchComponent(ctx context.Context, client *k8s.Client, name, namespace string, opts Options, nodeAgentResults map[string]Component) Component {
	comp := Component{Name: name}

	targets, derr := specs[name].discover(ctx, client.Clientset, namespace)
	if derr != nil {
		// Discovery failure (e.g. RBAC on list) is surfaced as a single
		// synthetic instance so it is visible in every output format.
		comp.Instances = append(comp.Instances, Instance{
			Name:  "(discovery failed)",
			Pages: []Page{{Name: "-", Error: derr.Error()}},
		})

		return comp
	}

	for _, tgt := range targets {
		inst := Instance{Name: tgt.instance}
		for _, page := range filterPages(tgt.pages, opts.Pages) {
			inst.Pages = append(inst.Pages, fetchPage(ctx, client, tgt.basePath, page, opts.Timeout))
		}

		comp.Instances = append(comp.Instances, inst)
	}

	// Graft node-agent-fetched pages (kubelet statusz) onto the proxy instances,
	// overriding the proxy's erroring statusz page per node. When node pods are
	// disabled or produced nothing, the proxy result stands.
	if na, ok := nodeAgentResults[name]; ok {
		comp.Instances = overlayPages(comp.Instances, na.Instances)
	}

	sortInstances(comp.Instances)

	return comp
}

// overlayPages merges supplemental instances into base, matched by instance
// name. For a matching instance, each supplemental page replaces a same-named
// page in place (preserving page order) or is appended if new. Instances with
// no match in base are appended. Used to graft node-agent-fetched kubelet
// statusz onto the proxy-discovered kubelet instances.
func overlayPages(base, supplemental []Instance) []Instance {
	idx := make(map[string]int, len(base))
	for i, inst := range base {
		idx[inst.Name] = i
	}

	for _, s := range supplemental {
		i, ok := idx[s.Name]
		if !ok {
			base = append(base, s)
			idx[s.Name] = len(base) - 1

			continue
		}

		for _, p := range s.Pages {
			base[i].Pages = replacePage(base[i].Pages, p)
		}
	}

	return base
}

// replacePage overwrites the page with the same Name, or appends it if absent.
func replacePage(pages []Page, p Page) []Page {
	for i := range pages {
		if pages[i].Name == p.Name {
			pages[i] = p

			return pages
		}
	}

	return append(pages, p)
}

// structuredAccept lists the Accept header values to try for each z-page, from
// newest to oldest. v1beta1 (Kubernetes v1.36+) is preferred; v1alpha1 (v1.35)
// is the fallback. If both are rejected (406) the page is fetched as plain text.
var structuredAccept = map[string][]string{
	"flagz": {
		"application/json;v=v1beta1;g=config.k8s.io;as=Flagz",
		"application/json;v=v1alpha1;g=config.k8s.io;as=Flagz",
	},
	"statusz": {
		"application/json;v=v1beta1;g=config.k8s.io;as=Statusz",
		"application/json;v=v1alpha1;g=config.k8s.io;as=Statusz",
	},
}

// fetchPage retrieves a single z-page via the API server proxy. The returned
// Page always has Name/Path set; Content or Error is populated by the outcome.
//
// For flagz and statusz it tries structured JSON Accept headers from newest to
// oldest API version, falling back to plain text on clusters that predate the
// feature.
func fetchPage(ctx context.Context, client *k8s.Client, basePath, page string, timeout time.Duration) Page {
	path := basePath + "/" + page

	reqCtx := ctx

	if timeout > 0 {
		var cancel context.CancelFunc

		reqCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	if versions, ok := structuredAccept[page]; ok {
		for _, accept := range versions {
			p := fetchPageWithAccept(reqCtx, client, path, page, accept)
			if p.Error != "" && isNotAcceptable(p.Error) {
				continue
			}

			return p
		}
	}

	return fetchPageWithAccept(reqCtx, client, path, page, "")
}

// fetchPageWithAccept fetches a z-page with an optional Accept header.
func fetchPageWithAccept(ctx context.Context, client *k8s.Client, path, page, accept string) Page {
	p := Page{Name: page, Path: path}

	req := client.Clientset.CoreV1().RESTClient().Get().AbsPath(path)

	if accept != "" {
		req.SetHeader("Accept", accept)
	}

	result := req.Do(ctx)

	var contentType string
	result.ContentType(&contentType)
	p.ContentType = contentType

	body, err := result.Raw()
	if err != nil {
		if len(body) > 0 {
			p.Error = fmt.Sprintf("%v: %s", err, string(body))
		} else {
			p.Error = err.Error()
		}

		return p
	}

	p.Content = string(body)

	return p
}

// isNotAcceptable checks whether an error string indicates a 406 Not Acceptable
// response from the API server.
func isNotAcceptable(errMsg string) bool {
	return strings.Contains(errMsg, "NotAcceptable")
}
