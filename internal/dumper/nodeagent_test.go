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
	for _, want := range []string{"10259/flagz", "10249/statusz", "--max-time 10", "Authorization: Bearer"} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q", want)
		}
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
