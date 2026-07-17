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

func TestMergeKubeletNewlyCoveredDefaults(t *testing.T) {
	// A configz body where the zero-value defaults added to close the fill gaps
	// are all absent (dropped by omitempty), including the nested memorySwap.
	input := `{
		"kubeletconfig": {
			"kind": "KubeletConfiguration",
			"memorySwap": {}
		}
	}`

	result, err := Merge(input, "kubelet", 36)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	inner := result.Merged["kubeletconfig"].(map[string]interface{})

	// Top-level string/slice defaults that were previously under-reported.
	wantString := []string{"cgroupRoot", "clusterDomain", "providerID", "staticPodPath"}
	for _, k := range wantString {
		v, ok := inner[k]
		if !ok {
			t.Errorf("%s not inserted", k)
		} else if v != "" {
			t.Errorf("%s: got %v, want empty string", k, v)
		}
		if !result.Filled[k] {
			t.Errorf("%s not in Filled set", k)
		}
	}

	if v, ok := inner["clusterDNS"]; !ok {
		t.Error("clusterDNS not inserted")
	} else if s, ok := v.([]interface{}); !ok || len(s) != 0 {
		t.Errorf("clusterDNS: got %v, want empty slice", v)
	}

	// Nested fill: memorySwap is present but its swapBehavior child is dropped.
	ms, ok := inner["memorySwap"].(map[string]interface{})
	if !ok {
		t.Fatalf("memorySwap: got %T, want map", inner["memorySwap"])
	}
	if v, ok := ms["swapBehavior"]; !ok {
		t.Error("memorySwap.swapBehavior not inserted")
	} else if v != "" {
		t.Errorf("memorySwap.swapBehavior: got %v, want empty string", v)
	}
	if !result.Filled["memorySwap.swapBehavior"] {
		t.Error("memorySwap.swapBehavior not in Filled set")
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

	// windowsRunAsService (omitempty bool, default false) is dropped by configz
	// and must be filled.
	if v, ok := inner["windowsRunAsService"]; !ok {
		t.Error("windowsRunAsService not inserted for kube-proxy")
	} else if v != false {
		t.Errorf("windowsRunAsService: got %v, want false", v)
	}
	if !result.Filled["windowsRunAsService"] {
		t.Error("windowsRunAsService not in Filled set")
	}

	// KubeProxyConfiguration v1alpha1 has no `linux` field; it must not be
	// fabricated into the output.
	if _, ok := inner["linux"]; ok {
		t.Error("kube-proxy output should not contain a fabricated `linux` field")
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
