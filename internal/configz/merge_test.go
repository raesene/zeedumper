package configz

import (
	"encoding/json"
	"testing"
)

func TestMergeKubelet(t *testing.T) {
	input := `{
		"kubeletconfig": {
			"kind": "KubeletConfiguration",
			"apiVersion": "kubelet.config.k8s.io/v1beta1",
			"maxPods": 110,
			"readOnlyPort": 10255
		}
	}`

	result, err := Merge(input, "kubelet", 36)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	inner := result.Merged["kubeletconfig"].(map[string]interface{})

	if v := inner["maxPods"]; v != float64(110) {
		t.Errorf("maxPods: got %v, want 110", v)
	}
	if v := inner["readOnlyPort"]; v != float64(10255) {
		t.Errorf("readOnlyPort should not be overwritten: got %v, want 10255", v)
	}
	if result.Filled["readOnlyPort"] {
		t.Error("readOnlyPort was present in input but marked as filled")
	}

	if v, ok := inner["protectKernelDefaults"]; !ok {
		t.Error("protectKernelDefaults not inserted")
	} else if v != false {
		t.Errorf("protectKernelDefaults: got %v, want false", v)
	}
	if !result.Filled["protectKernelDefaults"] {
		t.Error("protectKernelDefaults not in Filled set")
	}

	if v, ok := inner["featureGates"]; !ok {
		t.Error("featureGates not inserted")
	} else {
		m, ok := v.(map[string]interface{})
		if !ok {
			t.Errorf("featureGates: got %T, want map", v)
		} else if len(m) != 0 {
			t.Errorf("featureGates: got %d entries, want 0", len(m))
		}
	}
}

func TestMergeKubeProxy(t *testing.T) {
	input := `{
		"kubeproxy.config.k8s.io": {
			"kind": "KubeProxyConfiguration",
			"mode": "iptables"
		}
	}`

	result, err := Merge(input, "kube-proxy", 36)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	inner := result.Merged["kubeproxy.config.k8s.io"].(map[string]interface{})

	if v := inner["mode"]; v != "iptables" {
		t.Errorf("mode: got %v, want iptables", v)
	}

	if _, ok := inner["featureGates"]; !ok {
		t.Error("featureGates not inserted for kube-proxy")
	}
}

func TestMergeUnknownVersion(t *testing.T) {
	input := `{"kubeletconfig": {"kind": "KubeletConfiguration"}}`

	result, err := Merge(input, "kubelet", 99)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	if len(result.Filled) != 0 {
		t.Errorf("expected no filled fields for unknown version, got %d", len(result.Filled))
	}
}

func TestMergeUnknownComponent(t *testing.T) {
	input := `{"config": {}}`

	result, err := Merge(input, "kube-scheduler", 36)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	if len(result.Filled) != 0 {
		t.Errorf("expected no filled fields for unknown component, got %d", len(result.Filled))
	}
}

func TestMergeInvalidJSON(t *testing.T) {
	_, err := Merge("not json", "kubelet", 36)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestMergeJSON(t *testing.T) {
	input := `{"kubeletconfig":{"kind":"KubeletConfiguration","maxPods":110}}`

	merged, err := MergeJSON(input, "kubelet", 36)
	if err != nil {
		t.Fatalf("MergeJSON: %v", err)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(merged), &top); err != nil {
		t.Fatalf("parse merged: %v", err)
	}

	var inner map[string]interface{}
	if err := json.Unmarshal(top["kubeletconfig"], &inner); err != nil {
		t.Fatalf("parse inner: %v", err)
	}

	if _, ok := inner["protectKernelDefaults"]; !ok {
		t.Error("MergeJSON output missing protectKernelDefaults")
	}
}

func TestMergeJSONUnknownVersionPassthrough(t *testing.T) {
	input := `{"kubeletconfig":{"kind":"KubeletConfiguration"}}`

	merged, err := MergeJSON(input, "kubelet", 99)
	if err != nil {
		t.Fatalf("MergeJSON: %v", err)
	}

	if merged != input {
		t.Error("MergeJSON should return original JSON for unknown version")
	}
}
