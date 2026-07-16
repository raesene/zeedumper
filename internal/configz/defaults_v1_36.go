package configz

// kubeletDefaults_v1_36 lists the KubeletConfiguration fields that configz
// omits because their effective value is a Go zero value (dropped by
// omitempty). Combined with the configz response, these give the complete
// 125-field effective configuration. Derived from Kubernetes v1.36 source.
var kubeletDefaults_v1_36 = map[string]interface{}{
	// Booleans defaulting to false
	"enableContentionProfiling": false,
	"kernelMemcgNotification":   false,
	"protectKernelDefaults":     false,
	"rotateCertificates":        false,
	"runOnce":                   false,
	"serverTLSBootstrap":        false,

	// Integers defaulting to 0
	"evictionMaxPodGracePeriod": float64(0),
	"podsPerCore":               float64(0),
	"readOnlyPort":              float64(0),

	// Empty strings
	"imageServiceEndpoint":      "",
	"kubeletCgroups":            "",
	"kubeReservedCgroup":        "",
	"podCIDR":                   "",
	"reservedSystemCPUs":        "",
	"showHiddenMetricsForVersion": "",
	"staticPodURL":              "",
	"systemCgroups":             "",
	"systemReservedCgroup":      "",
	"tlsMinVersion":             "",

	// Nil slices (rendered as empty lists)
	"allowedUnsafeSysctls":                   []interface{}{},
	"preloadedImagesVerificationAllowlist":    []interface{}{},
	"registerWithTaints":                      []interface{}{},
	"reservedMemory":                          []interface{}{},
	"shutdownGracePeriodByPodPriority":        []interface{}{},
	"tlsCipherSuites":                         []interface{}{},
	"tlsCurvePreferences":                     []interface{}{},

	// Nil maps (rendered as empty objects)
	"cpuManagerPolicyOptions":    map[string]interface{}{},
	"evictionMinimumReclaim":     map[string]interface{}{},
	"evictionSoft":               map[string]interface{}{},
	"evictionSoftGracePeriod":    map[string]interface{}{},
	"featureGates":               map[string]interface{}{},
	"kubeReserved":               map[string]interface{}{},
	"qosReserved":                map[string]interface{}{},
	"staticPodURLHeader":         map[string]interface{}{},
	"systemReserved":             map[string]interface{}{},
	"topologyManagerPolicyOptions": map[string]interface{}{},

	// Nil pointers
	"maxParallelImagePulls": nil,
	"singleProcessOOMKill":  nil,
	"userNamespaces":        nil,

	// Empty struct
	"tracing": map[string]interface{}{},
}

// kubeProxyDefaults_v1_36 lists the KubeProxyConfiguration fields omitted by
// configz due to omitempty in Kubernetes v1.36.
var kubeProxyDefaults_v1_36 = map[string]interface{}{
	// Nil slices
	"nodePortAddresses": []interface{}{},

	// Nil maps
	"featureGates": map[string]interface{}{},

	// Nested fields inside linux (if present)
	"linux": map[string]interface{}{},
}
