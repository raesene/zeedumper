package dumper

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/raesene/zeedumper/internal/k8s"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// stderr is where the node-agent narrates resource creation/teardown. It is a
// package var so tests can silence or capture it.
var stderr io.Writer = os.Stderr

// localComponent describes a component that binds to the node's loopback
// interface and so cannot be reached through the API server proxy. zeedumper
// reaches these by scheduling a short-lived host-network pod on the node.
type localComponent struct {
	name             string
	scheme           string // http or https
	port             int
	pages            []string
	controlPlaneOnly bool // cm/scheduler run only on control-plane nodes
}

// localComponents is the set handled via the node-agent strategy.
var localComponents = []localComponent{
	{name: "kube-controller-manager", scheme: "https", port: 10257, pages: []string{"flagz", "statusz", "configz"}, controlPlaneOnly: true},
	{name: "kube-scheduler", scheme: "https", port: 10259, pages: []string{"flagz", "statusz", "configz"}, controlPlaneOnly: true},
	{name: "kube-proxy", scheme: "http", port: 10249, pages: []string{"flagz", "statusz", "configz"}, controlPlaneOnly: false},
}

const (
	// monitoringClusterRole already grants GET on the /flagz, /statusz,
	// /metrics and /healthz non-resource URLs the components serve.
	monitoringClusterRole = "system:monitoring"

	// kubeletStatuszURL is the kubelet's /statusz on its secure port, curl'd
	// from a host-network pod on the node. Unlike the other components, the
	// kubelet authorizes this as the resource nodes/statusz rather than a
	// non-resource URL, so system:monitoring does not cover it — the node agent
	// grants the SA a dedicated nodes/statusz ClusterRole (see setup).
	kubeletStatuszURL = "https://127.0.0.1:10250/statusz"

	// configzURL is the non-resource URL the https loopback components
	// (scheduler, controller-manager) authorize /configz as. system:monitoring
	// deliberately excludes /configz, so the node agent grants the SA a
	// dedicated ClusterRole for it (see setupConfigz).
	configzURL = "/configz"

	//nolint:gosec // G101: this is the well-known projected-token mount path, not a credential.
	agentTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"

	markerStart = "@@ZD@@ "
	markerCode  = "@@CODE@@ "
	markerBody  = "@@BODY@@"
	markerErr   = "@@ERR@@"
	markerEnd   = "@@ZDEND@@"
)

// fetchSpec is one endpoint a node-agent pod should retrieve.
type fetchSpec struct {
	component string
	page      string
	url       string
}

// nodeWork is the set of endpoints to fetch from a single node.
type nodeWork struct {
	node  string
	specs []fetchSpec
}

// gatherViaNodePods dumps loopback-bound components by creating a temporary
// ServiceAccount + ClusterRoleBinding (to system:monitoring) and a host-network
// pod on each eligible node. All created resources are removed before it
// returns, including on error or cancellation.
func gatherViaNodePods(ctx context.Context, client *k8s.Client, requested, pages []string, opts Options) ([]Component, error) {
	wanted := wantedLocalComponents(requested)
	kubeletStatusz := kubeletStatuszWanted(requested, pages)

	if len(wanted) == 0 && !kubeletStatusz {
		return nil, nil
	}

	cs := client.Clientset

	namespace := opts.Namespace
	if namespace == "" {
		namespace = "kube-system"
	}

	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}

	work, covered := buildNodeWork(nodes.Items, wanted, pages, kubeletStatusz)
	results := map[string][]Instance{} // component -> instances

	// Loopback components asked for but with no eligible node (e.g. a managed
	// control plane) get a clear placeholder rather than silently vanishing.
	// The kubelet is deliberately excluded: it is owned by the proxy path and
	// only augmented with statusz here, so it is never in `covered`.
	for name := range wanted {
		if !covered[name] {
			results[name] = append(results[name],
				placeholderInstance("(no eligible node)", "no node in the cluster runs this component within reach of a host-network pod"))
		}
	}

	if len(work) > 0 {
		grants := rbacGrants{
			monitoring:     len(wanted) > 0,
			kubeletStatusz: kubeletStatusz,
			configz:        configzAuthWanted(wanted, pages),
		}
		if err := runNodeAgents(ctx, cs, namespace, work, covered, opts, results, grants); err != nil {
			return nil, err
		}
	}

	return assembleLocalResults(results), nil
}

// kubeletStatuszWanted reports whether the kubelet's statusz page should be
// fetched via the node agent (the kubelet is otherwise reached through the API
// proxy, but statusz is authorized as nodes/statusz, which the proxy path's
// kubelet client identity lacks).
func kubeletStatuszWanted(requested, pages []string) bool {
	if !slices.Contains(requested, "kubelet") {
		return false
	}

	return len(pages) == 0 || slices.Contains(pages, "statusz")
}

// configzAuthWanted reports whether the node agent needs the dedicated
// /configz ClusterRole. Only the https loopback components (scheduler,
// controller-manager) delegate authorization for /configz, and only when
// configz is among the requested pages; kube-proxy serves configz without any
// authentication, so it needs no grant.
func configzAuthWanted(wanted map[string]localComponent, pages []string) bool {
	if len(pages) != 0 && !slices.Contains(pages, "configz") {
		return false
	}

	for _, lc := range wanted {
		if lc.scheme == "https" {
			return true
		}
	}

	return false
}

// wantedLocalComponents returns the loopback components the caller asked for.
func wantedLocalComponents(requested []string) map[string]localComponent {
	wanted := map[string]localComponent{}

	for _, lc := range localComponents {
		if slices.Contains(requested, lc.name) {
			wanted[lc.name] = lc
		}
	}

	return wanted
}

// buildNodeWork produces the per-node fetch list and reports which components
// ended up with at least one eligible node.
func buildNodeWork(nodes []corev1.Node, wanted map[string]localComponent, pages []string, kubeletStatusz bool) (work []nodeWork, covered map[string]bool) {
	covered = map[string]bool{}

	for _, node := range nodes {
		specs := nodeFetchSpecs(node, wanted, pages, kubeletStatusz, covered)
		if len(specs) > 0 {
			work = append(work, nodeWork{node: node.Name, specs: specs})
		}
	}

	return work, covered
}

// nodeFetchSpecs builds the endpoints to fetch from one node, marking the
// covered set as a side effect.
func nodeFetchSpecs(node corev1.Node, wanted map[string]localComponent, pages []string, kubeletStatusz bool, covered map[string]bool) []fetchSpec {
	controlPlane := isControlPlane(node)

	var specs []fetchSpec

	for _, lc := range wanted {
		if lc.controlPlaneOnly && !controlPlane {
			continue
		}

		for _, page := range filterPages(lc.pages, pages) {
			specs = append(specs, fetchSpec{
				component: lc.name,
				page:      page,
				url:       fmt.Sprintf("%s://127.0.0.1:%d/%s", lc.scheme, lc.port, page),
			})
			covered[lc.name] = true
		}
	}

	// The kubelet runs on every node. Its statusz is grafted onto the
	// proxy-discovered kubelet instances later, so it is intentionally left out
	// of `covered` (no placeholder handling here).
	if kubeletStatusz {
		specs = append(specs, fetchSpec{
			component: "kubelet",
			page:      "statusz",
			url:       kubeletStatuszURL,
		})
	}

	return specs
}

// rbacGrants records which temporary permissions a node-agent run needs. Only
// the resources actually required for the requested components are created.
type rbacGrants struct {
	monitoring     bool // bind system:monitoring (flagz/statusz non-resource URLs)
	kubeletStatusz bool // create+bind a nodes/statusz ClusterRole for the kubelet
	configz        bool // create+bind a /configz ClusterRole for https loopback components
}

// runNodeAgents performs the pre-pull check, sets up (and always tears down)
// the temporary RBAC, and launches/collects an agent pod per node.
func runNodeAgents(ctx context.Context, cs kubernetes.Interface, namespace string, work []nodeWork, covered map[string]bool, opts Options, results map[string][]Instance, grants rbacGrants) error {
	runID := opts.runID()
	image := opts.nodePodImage()

	// Pre-pull check: validate the image on a target node before creating any
	// RBAC, so a bad or unreachable image fails fast with the real reason
	// instead of leaving the fleet stuck in ImagePullBackOff.
	fmt.Fprintf(stderr, "node-agent: verifying image %q is pullable on node %q\n", image, work[0].node)

	if pullErr := verifyImagePullable(ctx, cs, namespace, image, work[0].node, runID); pullErr != nil {
		fmt.Fprintf(stderr, "node-agent: image check failed (%v); skipping node-agent components\n", pullErr)
		msg := fmt.Sprintf("node-agent image %q could not be pulled: %v", image, pullErr)

		for name := range covered { // covered holds only components with eligible nodes
			results[name] = append(results[name], placeholderInstance("(image pull failed)", msg))
		}

		return nil
	}

	ra := &rbacAgent{
		cs: cs, namespace: namespace, runID: runID, image: image,
		grants: grants,
	}
	if err := ra.setup(ctx); err != nil {
		return fmt.Errorf("setting up node-agent RBAC: %w", err)
	}
	// Always tear down, even on cancellation: a fresh context keeps the cleanup
	// from being aborted by the caller's cancelled ctx.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		ra.teardown(cleanupCtx)
	}()

	fmt.Fprintf(stderr, "node-agent: created serviceaccount/%s and %s; launching %d pod(s)\n",
		ra.name(), ra.grantSummary(), len(work))

	ra.launchAndCollect(ctx, work, curlTimeoutSeconds(opts.Timeout), results)

	return nil
}

// launchAndCollect starts every agent pod, then gathers each pod's output.
func (r *rbacAgent) launchAndCollect(ctx context.Context, work []nodeWork, curlTimeout int, results map[string][]Instance) {
	podToNode := map[string]nodeWork{}

	for _, w := range work {
		podName, err := r.launchPod(ctx, w.node, buildScript(w.specs, curlTimeout))
		if err != nil {
			recordNodeError(results, w, fmt.Sprintf("launching pod: %v", err))
			continue
		}

		podToNode[podName] = w
	}

	for podName, w := range podToNode {
		raw, err := r.collect(ctx, podName, 2*time.Minute)
		if err != nil {
			recordNodeError(results, w, fmt.Sprintf("collecting pod output: %v", err))
			continue
		}

		for comp, pagesForComp := range parseAgentLog(raw) {
			results[comp] = append(results[comp], Instance{Name: w.node, Pages: pagesForComp})
		}
	}
}

// placeholderInstance builds a synthetic instance carrying a single error,
// used when no real instance output is available.
func placeholderInstance(name, errMsg string) Instance {
	return Instance{Name: name, Pages: []Page{{Name: "-", Error: errMsg}}}
}

// recordNodeError attributes an error to every component targeted on a node.
func recordNodeError(results map[string][]Instance, w nodeWork, errMsg string) {
	for _, comp := range distinctComponents(w.specs) {
		results[comp] = append(results[comp], placeholderInstance(w.node, errMsg))
	}
}

// curlTimeoutSeconds converts the per-page timeout to whole seconds for curl,
// falling back to a sane default.
func curlTimeoutSeconds(d time.Duration) int {
	if s := int(d.Seconds()); s > 0 {
		return s
	}

	return 10
}

// assembleLocalResults flattens the per-component instance map into Components
// in canonical display order. The kubelet is included when present but is later
// merged into its proxy-discovered component by the caller rather than emitted
// standalone.
func assembleLocalResults(results map[string][]Instance) []Component {
	var out []Component

	for _, lc := range localComponents {
		if insts, ok := results[lc.name]; ok {
			sortInstances(insts)
			out = append(out, Component{Name: lc.name, Instances: insts})
		}
	}

	if insts, ok := results["kubelet"]; ok {
		sortInstances(insts)
		out = append(out, Component{Name: "kubelet", Instances: insts})
	}

	return out
}

func isControlPlane(node corev1.Node) bool {
	for _, key := range []string{"node-role.kubernetes.io/control-plane", "node-role.kubernetes.io/master"} {
		if _, ok := node.Labels[key]; ok {
			return true
		}
	}

	return false
}

func distinctComponents(specs []fetchSpec) []string {
	seen := map[string]bool{}

	var out []string

	for _, s := range specs {
		if !seen[s.component] {
			seen[s.component] = true
			out = append(out, s.component)
		}
	}

	return out
}

// buildScript renders the POSIX-sh program each agent pod runs. For every
// endpoint it emits a marker block carrying the HTTP code plus base64-encoded
// body and stderr, so arbitrary page content survives transport through logs.
func buildScript(specs []fetchSpec, curlTimeoutSec int) string {
	var b strings.Builder
	b.WriteString("set -u\n")
	fmt.Fprintf(&b, "TOKEN=$(cat %s 2>/dev/null || echo '')\n", agentTokenPath)

	for _, s := range specs {
		fmt.Fprintf(&b, "echo '%s%s %s %s'\n", markerStart, s.component, s.page, s.url)

		if versions, ok := structuredAccept[s.page]; ok {
			// Try each structured version; use the first that doesn't 406.
			fmt.Fprintf(&b, "code='406'\n")

			for _, accept := range versions {
				fmt.Fprintf(&b, "if [ \"$code\" = '406' ]; then\n")
				fmt.Fprintf(&b, "  code=$(curl -sk --max-time %d -o /tmp/body -w '%%{http_code}' -H \"Authorization: Bearer $TOKEN\" -H 'Accept: %s' '%s' 2>/tmp/err)\n", curlTimeoutSec, accept, s.url)
				fmt.Fprintf(&b, "fi\n")
			}

			// Fall back to plain text if all structured versions were rejected.
			fmt.Fprintf(&b, "if [ \"$code\" = '406' ]; then\n")
			fmt.Fprintf(&b, "  code=$(curl -sk --max-time %d -o /tmp/body -w '%%{http_code}' -H \"Authorization: Bearer $TOKEN\" '%s' 2>/tmp/err)\n", curlTimeoutSec, s.url)
			fmt.Fprintf(&b, "fi\n")
		} else {
			fmt.Fprintf(&b, "code=$(curl -sk --max-time %d -o /tmp/body -w '%%{http_code}' -H \"Authorization: Bearer $TOKEN\" '%s' 2>/tmp/err)\n", curlTimeoutSec, s.url)
		}

		fmt.Fprintf(&b, "echo \"%s$code\"\n", markerCode)
		fmt.Fprintf(&b, "echo '%s'\n", markerBody)
		b.WriteString("base64 /tmp/body 2>/dev/null\n")
		fmt.Fprintf(&b, "echo '%s'\n", markerErr)
		b.WriteString("base64 /tmp/err 2>/dev/null\n")
		fmt.Fprintf(&b, "echo '%s'\n", markerEnd)
	}

	return b.String()
}

// parseAgentLog turns an agent pod's log stream into pages keyed by component.
func parseAgentLog(raw []byte) map[string][]Page {
	out := map[string][]Page{}
	lines := strings.Split(string(raw), "\n")

	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], markerStart) {
			continue
		}

		fields := strings.SplitN(strings.TrimPrefix(lines[i], markerStart), " ", 3)
		if len(fields) != 3 {
			continue
		}

		comp, pageName, url := fields[0], fields[1], fields[2]

		var code, bodyB64, errB64 string

		section := ""

		for i++; i < len(lines); i++ {
			line := lines[i]
			switch {
			case strings.HasPrefix(line, markerCode):
				code = strings.TrimPrefix(line, markerCode)
			case line == markerBody:
				section = "body"
			case line == markerErr:
				section = "err"
			case line == markerEnd:
				section = ""
			default:
				switch section {
				case "body":
					bodyB64 += line
				case "err":
					errB64 += line
				}
			}

			if line == markerEnd {
				break
			}
		}

		out[comp] = append(out[comp], buildAgentPage(pageName, url, code, bodyB64, errB64))
	}

	return out
}

func buildAgentPage(name, url, code, bodyB64, errB64 string) Page {
	p := Page{Name: name, Path: url}
	body := decodeAgentField(bodyB64)
	stderr := strings.TrimSpace(decodeAgentField(errB64))

	switch code {
	case "200":
		p.Content = body
	case "000", "":
		// curl never received a response (connection refused, timeout, ...).
		if stderr != "" {
			p.Error = stderr
		} else {
			p.Error = "request failed (no response from endpoint)"
		}
	default:
		detail := strings.TrimSpace(body)
		if detail == "" {
			detail = stderr
		}

		p.Error = fmt.Sprintf("HTTP %s: %s", code, detail)
	}

	return p
}

// decodeAgentField strips the line wrapping busybox base64 adds and decodes.
func decodeAgentField(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}

		return r
	}, s)
	if cleaned == "" {
		return ""
	}

	decoded, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return ""
	}

	return string(decoded)
}

// rbacAgent owns the lifecycle of the temporary SA, ClusterRoleBinding(s),
// ClusterRole and pods.
type rbacAgent struct {
	cs        kubernetes.Interface
	namespace string
	runID     string
	image     string

	// grants records which temporary permissions to create for this run.
	grants rbacGrants
}

func (r *rbacAgent) name() string { return "zeedumper-zpages-" + r.runID }

// statuszName is the name of the dedicated kubelet nodes/statusz ClusterRole and
// its binding.
func (r *rbacAgent) statuszName() string { return r.name() + "-kubelet-statusz" }

// configzName is the name of the dedicated /configz ClusterRole and its binding
// for the https loopback components.
func (r *rbacAgent) configzName() string { return r.name() + "-configz" }

func (r *rbacAgent) labels() map[string]string {
	return map[string]string{"app": "zeedumper-zpages", "zeedumper-run": r.runID}
}

// grantSummary describes the RBAC the agent created, for the narration line.
func (r *rbacAgent) grantSummary() string {
	var grants []string
	if r.grants.monitoring {
		grants = append(grants, "clusterrolebinding -> "+monitoringClusterRole)
	}

	if r.grants.kubeletStatusz {
		grants = append(grants, "clusterrole+binding (nodes/statusz)")
	}

	if r.grants.configz {
		grants = append(grants, "clusterrole+binding (/configz)")
	}

	if len(grants) == 0 {
		return "no rbac"
	}

	return strings.Join(grants, " and ")
}

func (r *rbacAgent) setup(ctx context.Context) error {
	if _, err := r.cs.CoreV1().ServiceAccounts(r.namespace).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: r.name(), Namespace: r.namespace, Labels: r.labels()},
	}, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("creating serviceaccount: %w", err)
	}

	if r.grants.monitoring {
		_, err := r.cs.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: r.name(), Labels: r.labels()},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     monitoringClusterRole,
			},
			Subjects: []rbacv1.Subject{r.subject()},
		}, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("creating clusterrolebinding: %w", err)
		}
	}

	if r.grants.kubeletStatusz {
		if err := r.setupKubeletStatusz(ctx); err != nil {
			return err
		}
	}

	if r.grants.configz {
		if err := r.setupConfigz(ctx); err != nil {
			return err
		}
	}

	return nil
}

// setupConfigz creates the dedicated ClusterRole granting GET on the /configz
// non-resource URL and binds the agent SA to it. The https loopback components
// (scheduler, controller-manager) delegate authorization for /configz to the
// API server, and system:monitoring deliberately omits it, so this grant is
// what makes their configz retrievable.
func (r *rbacAgent) setupConfigz(ctx context.Context) error {
	if _, err := r.cs.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: r.configzName(), Labels: r.labels()},
		Rules: []rbacv1.PolicyRule{{
			NonResourceURLs: []string{configzURL},
			Verbs:           []string{"get"},
		}},
	}, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("creating configz clusterrole: %w", err)
	}

	_, err := r.cs.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: r.configzName(), Labels: r.labels()},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     r.configzName(),
		},
		Subjects: []rbacv1.Subject{r.subject()},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating configz clusterrolebinding: %w", err)
	}

	return nil
}

// setupKubeletStatusz creates the dedicated ClusterRole granting the kubelet's
// nodes/statusz resource and binds the agent SA to it. The kubelet authorizes
// /statusz as this resource (not a non-resource URL), so system:monitoring is
// insufficient and no built-in role grants it.
func (r *rbacAgent) setupKubeletStatusz(ctx context.Context) error {
	if _, err := r.cs.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: r.statuszName(), Labels: r.labels()},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{""},
			Resources: []string{"nodes/statusz"},
			Verbs:     []string{"get"},
		}},
	}, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("creating kubelet statusz clusterrole: %w", err)
	}

	_, err := r.cs.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: r.statuszName(), Labels: r.labels()},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     r.statuszName(),
		},
		Subjects: []rbacv1.Subject{r.subject()},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating kubelet statusz clusterrolebinding: %w", err)
	}

	return nil
}

func (r *rbacAgent) subject() rbacv1.Subject {
	return rbacv1.Subject{Kind: "ServiceAccount", Name: r.name(), Namespace: r.namespace}
}

func (r *rbacAgent) teardown(ctx context.Context) {
	selector := "zeedumper-run=" + r.runID
	_ = r.cs.CoreV1().Pods(r.namespace).DeleteCollection(ctx,
		metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: selector})

	// Delete every resource this run may have created; NotFound is expected for
	// any that a given run did not need.
	for _, crb := range []string{r.name(), r.statuszName(), r.configzName()} {
		if err := r.cs.RbacV1().ClusterRoleBindings().Delete(ctx, crb, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			fmt.Fprintf(stderr, "node-agent: warning: failed to delete clusterrolebinding %s: %v\n", crb, err)
		}
	}

	for _, cr := range []string{r.statuszName(), r.configzName()} {
		if err := r.cs.RbacV1().ClusterRoles().Delete(ctx, cr, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			fmt.Fprintf(stderr, "node-agent: warning: failed to delete clusterrole %s: %v\n", cr, err)
		}
	}

	if err := r.cs.CoreV1().ServiceAccounts(r.namespace).Delete(ctx, r.name(), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		fmt.Fprintf(stderr, "node-agent: warning: failed to delete serviceaccount %s: %v\n", r.name(), err)
	}
}

func (r *rbacAgent) launchPod(ctx context.Context, nodeName, script string) (string, error) {
	podName := r.name() + "-" + sanitizeName(nodeName)

	_, err := r.cs.CoreV1().Pods(r.namespace).Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: r.namespace, Labels: r.labels()},
		Spec: corev1.PodSpec{
			HostNetwork:        true,
			NodeName:           nodeName,
			ServiceAccountName: r.name(),
			RestartPolicy:      corev1.RestartPolicyNever,
			Tolerations:        []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
			Containers: []corev1.Container{{
				Name:    "agent",
				Image:   r.image,
				Command: []string{"sh", "-c"},
				Args:    []string{script},
			}},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return "", err
	}

	return podName, nil
}

// collect waits for a pod to finish and returns its logs. A per-node image
// pull failure (the pre-pull check only probes one node) is surfaced promptly
// rather than waiting out the whole timeout.
func (r *rbacAgent) collect(ctx context.Context, podName string, timeout time.Duration) ([]byte, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		pod, err := r.cs.CoreV1().Pods(r.namespace).Get(waitCtx, podName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}

		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			return r.cs.CoreV1().Pods(r.namespace).GetLogs(podName, &corev1.PodLogOptions{}).DoRaw(ctx)
		}

		if reason, msg, failed := imagePullFailure(pod); failed {
			return nil, fmt.Errorf("image %q pull failed: %s: %s", r.image, reason, msg)
		}

		select {
		case <-waitCtx.Done():
			return nil, fmt.Errorf("timed out waiting for pod %s", podName)
		case <-time.After(2 * time.Second):
		}
	}
}

// imagePullFailReasons are container "waiting" reasons that mean the image will
// never be obtained, so there is no point waiting for the kubelet to retry.
var imagePullFailReasons = map[string]bool{
	"ImagePullBackOff":    true,
	"ErrImagePull":        true,
	"InvalidImageName":    true,
	"ImageInspectError":   true,
	"RegistryUnavailable": true,
}

// imagePullFailure reports whether any container is stuck on an unrecoverable
// image pull error, returning the reason and message.
func imagePullFailure(pod *corev1.Pod) (reason, msg string, failed bool) {
	for _, st := range pod.Status.ContainerStatuses {
		if w := st.State.Waiting; w != nil && imagePullFailReasons[w.Reason] {
			return w.Reason, strings.TrimSpace(w.Message), true
		}
	}

	return "", "", false
}

// verifyImagePullable runs a throwaway pod (no RBAC, no host network) whose only
// job is to pull image on nodeName. It returns nil as soon as the container
// starts or finishes, or an error if the image cannot be pulled. The probe pod
// is always deleted before returning.
func verifyImagePullable(ctx context.Context, cs kubernetes.Interface, namespace, image, nodeName, runID string) error {
	name := "zeedumper-zpages-" + runID + "-imgcheck"

	probe := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"app": "zeedumper-zpages", "zeedumper-run": runID},
		},
		Spec: corev1.PodSpec{
			NodeName:      nodeName,
			RestartPolicy: corev1.RestartPolicyNever,
			Tolerations:   []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
			Containers: []corev1.Container{{
				Name:    "imgcheck",
				Image:   image,
				Command: []string{"sh", "-c", "exit 0"},
			}},
		},
	}
	if _, err := cs.CoreV1().Pods(namespace).Create(ctx, probe, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("creating image-check pod: %w", err)
	}

	defer func() {
		delCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		_ = cs.CoreV1().Pods(namespace).Delete(delCtx, name, metav1.DeleteOptions{})
	}()

	waitCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	for {
		pod, err := cs.CoreV1().Pods(namespace).Get(waitCtx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		// Once the container is running or has terminated, the image is present.
		if pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodSucceeded {
			return nil
		}

		for _, st := range pod.Status.ContainerStatuses {
			if st.State.Running != nil || st.State.Terminated != nil {
				return nil
			}
		}

		if reason, msg, failed := imagePullFailure(pod); failed {
			return fmt.Errorf("%s: %s", reason, msg)
		}

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out after 90s waiting to pull image (last phase: %s)", pod.Status.Phase)
		case <-time.After(2 * time.Second):
		}
	}
}

func sanitizeName(s string) string {
	var b strings.Builder

	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}

	out := b.String()
	if len(out) > 40 {
		out = out[:40]
	}

	return strings.Trim(out, "-")
}
