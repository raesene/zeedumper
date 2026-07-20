package dumper

import (
	"encoding/base64"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// agentBlock renders one marker block the way the in-pod script would.
func agentBlock(comp, page, url, code, body, stderr string) string {
	var b strings.Builder
	b.WriteString(markerStart + comp + " " + page + " " + url + "\n")
	b.WriteString(markerCode + code + "\n")
	b.WriteString(markerBody + "\n")
	b.WriteString(base64.StdEncoding.EncodeToString([]byte(body)) + "\n")
	b.WriteString(markerErr + "\n")
	b.WriteString(base64.StdEncoding.EncodeToString([]byte(stderr)) + "\n")
	b.WriteString(markerEnd + "\n")

	return b.String()
}

func TestParseAgentLog(t *testing.T) {
	log := agentBlock("kube-scheduler", "flagz", "https://127.0.0.1:10259/flagz", "200", "scheduler flagz\nfoo=bar", "") +
		agentBlock("kube-controller-manager", "statusz", "https://127.0.0.1:10257/statusz", "403", `{"reason":"Forbidden"}`, "") +
		agentBlock("kube-proxy", "flagz", "http://127.0.0.1:10249/flagz", "000", "", "curl: (7) Failed to connect")

	got := parseAgentLog([]byte(log))

	sched := got["kube-scheduler"]
	if len(sched) != 1 || !sched[0].OK() || !strings.Contains(sched[0].Content, "foo=bar") {
		t.Fatalf("scheduler page = %+v", sched)
	}

	cm := got["kube-controller-manager"]
	if len(cm) != 1 || cm[0].OK() || !strings.Contains(cm[0].Error, "HTTP 403") {
		t.Fatalf("controller-manager page = %+v", cm)
	}

	kp := got["kube-proxy"]
	if len(kp) != 1 || kp[0].OK() || !strings.Contains(kp[0].Error, "Failed to connect") {
		t.Fatalf("kube-proxy page = %+v", kp)
	}
}

func TestBuildScriptCoversEveryEndpoint(t *testing.T) {
	specs := []fetchSpec{
		{component: "kube-scheduler", page: "flagz", url: "https://127.0.0.1:10259/flagz"},
		{component: "kube-proxy", page: "statusz", url: "http://127.0.0.1:10249/statusz"},
	}

	script := buildScript(specs, 10)
	for _, want := range []string{
		"10259/flagz", "10249/statusz", "--max-time 10", "Authorization: Bearer",
		"v=v1beta1;g=config.k8s.io;as=Flagz", "v=v1alpha1;g=config.k8s.io;as=Flagz",
		"v=v1beta1;g=config.k8s.io;as=Statusz", "v=v1alpha1;g=config.k8s.io;as=Statusz",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q", want)
		}
	}
}

func TestKubeletStatuszWanted(t *testing.T) {
	tests := []struct {
		name      string
		requested []string
		pages     []string
		want      bool
	}{
		{name: "kubelet, all pages", requested: []string{"kubelet"}, pages: nil, want: true},
		{name: "kubelet, statusz only", requested: []string{"kubelet"}, pages: []string{"statusz"}, want: true},
		{name: "kubelet, but statusz filtered out", requested: []string{"kubelet"}, pages: []string{"flagz", "configz"}, want: false},
		{name: "kubelet not requested", requested: []string{"kube-proxy"}, pages: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := kubeletStatuszWanted(tt.requested, tt.pages); got != tt.want {
				t.Errorf("kubeletStatuszWanted(%v, %v) = %v, want %v", tt.requested, tt.pages, got, tt.want)
			}
		})
	}
}

func TestConfigzAuthWanted(t *testing.T) {
	https := map[string]localComponent{"kube-scheduler": {name: "kube-scheduler", scheme: "https"}}
	httpOnly := map[string]localComponent{"kube-proxy": {name: "kube-proxy", scheme: "http"}}

	tests := []struct {
		name   string
		wanted map[string]localComponent
		pages  []string
		want   bool
	}{
		{name: "https component, all pages", wanted: https, pages: nil, want: true},
		{name: "https component, configz only", wanted: https, pages: []string{"configz"}, want: true},
		{name: "https component, configz filtered out", wanted: https, pages: []string{"flagz", "statusz"}, want: false},
		{name: "http-only component needs no grant", wanted: httpOnly, pages: nil, want: false},
		{name: "no loopback components", wanted: map[string]localComponent{}, pages: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := configzAuthWanted(tt.wanted, tt.pages); got != tt.want {
				t.Errorf("configzAuthWanted(%v, %v) = %v, want %v", tt.wanted, tt.pages, got, tt.want)
			}
		})
	}
}

func TestGrantSummary(t *testing.T) {
	tests := []struct {
		name   string
		grants rbacGrants
		want   string
	}{
		{name: "none", grants: rbacGrants{}, want: "no rbac"},
		{name: "monitoring only", grants: rbacGrants{monitoring: true}, want: "clusterrolebinding -> system:monitoring"},
		{name: "configz only", grants: rbacGrants{configz: true}, want: "clusterrole+binding (/configz)"},
		{
			name:   "all three",
			grants: rbacGrants{monitoring: true, kubeletStatusz: true, configz: true},
			want:   "clusterrolebinding -> system:monitoring and clusterrole+binding (nodes/statusz) and clusterrole+binding (/configz)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &rbacAgent{grants: tt.grants}
			if got := r.grantSummary(); got != tt.want {
				t.Errorf("grantSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNodeFetchSpecsKubeletStatusz(t *testing.T) {
	node := corev1.Node{}
	node.Name = "worker-1" // not a control-plane node

	// Kubelet statusz is added on every node and never marks covered (the
	// kubelet is owned by the proxy path and only augmented here).
	specs := nodeFetchSpecs(node, nil, nil, true, map[string]bool{})
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec, got %d: %+v", len(specs), specs)
	}

	if specs[0].component != "kubelet" || specs[0].page != "statusz" || specs[0].url != kubeletStatuszURL {
		t.Errorf("unexpected kubelet spec: %+v", specs[0])
	}

	covered := map[string]bool{}
	nodeFetchSpecs(node, nil, nil, true, covered)

	if covered["kubelet"] {
		t.Error("kubelet must not be marked covered")
	}

	// When not wanted, no kubelet spec is produced.
	if specs := nodeFetchSpecs(node, nil, nil, false, map[string]bool{}); len(specs) != 0 {
		t.Errorf("expected no specs when kubelet statusz not wanted, got %+v", specs)
	}
}

func TestSanitizeName(t *testing.T) {
	if got := sanitizeName("Node.Example_01"); got != "node-example-01" {
		t.Errorf("sanitizeName = %q", got)
	}
}

func podWithWaiting(reason, msg string) *corev1.Pod {
	return &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: msg},
				},
			}},
		},
	}
}

func TestImagePullFailure(t *testing.T) {
	reason, msg, failed := imagePullFailure(podWithWaiting("ImagePullBackOff", "Back-off pulling image \"bogus\""))
	if !failed || reason != "ImagePullBackOff" || !strings.Contains(msg, "bogus") {
		t.Fatalf("expected detected pull failure, got failed=%v reason=%q msg=%q", failed, reason, msg)
	}

	// A benign waiting reason (still creating the container) is not a failure.
	if _, _, failed := imagePullFailure(podWithWaiting("ContainerCreating", "")); failed {
		t.Error("ContainerCreating should not be treated as an image pull failure")
	}

	// A running container is not a failure.
	running := &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	}}}}
	if _, _, failed := imagePullFailure(running); failed {
		t.Error("running container should not be a failure")
	}
}
