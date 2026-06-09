// Command zeedumper connects to a Kubernetes cluster using the caller's
// kubeconfig and dumps component z-pages (flagz, statusz, and configz for the
// kubelet) via the API server proxy, in text, JSON, or HTML.
package main

func main() {
	Execute()
}
