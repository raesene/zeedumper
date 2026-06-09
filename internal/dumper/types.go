package dumper

// Page is a single z-page fetched from a component instance.
type Page struct {
	Name        string `json:"name"`                  // flagz, statusz, configz
	Path        string `json:"path"`                  // API server path used to fetch it
	ContentType string `json:"contentType,omitempty"` // as reported by the server
	Content     string `json:"content,omitempty"`     // raw body on success
	Error       string `json:"error,omitempty"`       // populated when the fetch failed
}

// OK reports whether the page was retrieved without error.
func (p Page) OK() bool { return p.Error == "" }

// Instance is a single running copy of a component (a node for kubelet, a pod
// for control-plane components, or the API server itself).
type Instance struct {
	Name  string `json:"name"` // node name, pod name, or "apiserver"
	Pages []Page `json:"pages"`
}

// Component groups all instances of a single Kubernetes component.
type Component struct {
	Name      string     `json:"name"`
	Instances []Instance `json:"instances"`
}

// Dump is the complete result of a run, ready to be rendered in any format.
type Dump struct {
	Cluster    string      `json:"cluster"`
	Context    string      `json:"context,omitempty"`
	Timestamp  string      `json:"timestamp"`
	Components []Component `json:"components"`
}
