package configz

type componentDefaults struct {
	wrapperKey string
	defaults   map[string]interface{}
}

type versionEntry struct {
	components map[string]componentDefaults
}

var registry = map[int]versionEntry{
	36: {
		components: map[string]componentDefaults{
			"kubelet": {
				wrapperKey: "kubeletconfig",
				defaults:   kubeletDefaults_v1_36,
			},
			"kube-proxy": {
				wrapperKey: "kubeproxy.config.k8s.io",
				defaults:   kubeProxyDefaults_v1_36,
			},
		},
	},
}

func lookupDefaults(component string, minor int) *componentDefaults {
	entry, ok := registry[minor]
	if !ok {
		return nil
	}

	cd, ok := entry.components[component]
	if !ok {
		return nil
	}

	return &cd
}
