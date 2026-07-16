// Package output renders a dumper.Dump in text, JSON, or self-contained HTML.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/raesene/zeedumper/internal/configz"
	"github.com/raesene/zeedumper/internal/dumper"
)

// Format is a supported output format.
type Format string

// Supported output formats.
const (
	FormatText Format = "text"
	FormatJSON Format = "json"
	FormatHTML Format = "html"
)

// ParseFormat validates a user-supplied format string.
func ParseFormat(s string) (Format, error) {
	switch Format(strings.ToLower(s)) {
	case FormatText:
		return FormatText, nil
	case FormatJSON:
		return FormatJSON, nil
	case FormatHTML:
		return FormatHTML, nil
	default:
		return "", fmt.Errorf("unknown output format %q (want text, json, or html)", s)
	}
}

// Render writes the dump to w in the requested format.
func Render(w io.Writer, d *dumper.Dump, format Format) error {
	switch format {
	case FormatJSON:
		return renderJSON(w, d)
	case FormatHTML:
		return renderHTML(w, d)
	case FormatText:
		return renderText(w, d)
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func renderJSON(w io.Writer, d *dumper.Dump) error {
	merged := mergeConfigzPages(d)

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	return enc.Encode(merged)
}

func renderText(w io.Writer, d *dumper.Dump) error {
	k8sMinor := parseMinor(d.ServerVersion)

	bw := &errWriter{w: w}
	bw.printf("zeedumper: Kubernetes z-page dump\n")
	bw.printf("Cluster:   %s\n", d.Cluster)

	if d.Context != "" {
		bw.printf("Context:   %s\n", d.Context)
	}

	bw.printf("Timestamp: %s\n", d.Timestamp)

	for _, comp := range d.Components {
		bw.printf("\n================================================================\n")
		bw.printf("COMPONENT: %s\n", comp.Name)
		bw.printf("================================================================\n")

		for _, inst := range comp.Instances {
			bw.printf("\n--- instance: %s ---\n", inst.Name)

			for _, page := range inst.Pages {
				if page.OK() {
					content := page.Content
					if page.Name == "configz" && k8sMinor > 0 {
						if merged, err := configz.MergeJSON(content, comp.Name, k8sMinor); err == nil {
							content = merged
						}
					}

					bw.printf("\n[%s] %s\n", page.Name, page.Path)
					bw.printf("%s\n", strings.TrimRight(content, "\n"))
				} else {
					bw.printf("\n[%s] ERROR: %s\n", page.Name, page.Error)
				}
			}
		}
	}

	return bw.err
}

// mergeConfigzPages returns a shallow copy of the Dump with configz page
// content replaced by the merged (defaults-filled) JSON. The original Dump
// is not modified.
func mergeConfigzPages(d *dumper.Dump) *dumper.Dump {
	k8sMinor := parseMinor(d.ServerVersion)
	if k8sMinor == 0 {
		return d
	}

	out := *d
	out.Components = make([]dumper.Component, len(d.Components))

	for ci, comp := range d.Components {
		out.Components[ci] = dumper.Component{Name: comp.Name}
		out.Components[ci].Instances = make([]dumper.Instance, len(comp.Instances))

		for ii, inst := range comp.Instances {
			out.Components[ci].Instances[ii] = dumper.Instance{Name: inst.Name}
			out.Components[ci].Instances[ii].Pages = make([]dumper.Page, len(inst.Pages))

			for pi, page := range inst.Pages {
				out.Components[ci].Instances[ii].Pages[pi] = page

				if page.Name == "configz" && page.OK() {
					if merged, err := configz.MergeJSON(page.Content, comp.Name, k8sMinor); err == nil {
						out.Components[ci].Instances[ii].Pages[pi].Content = merged
					}
				}
			}
		}
	}

	return &out
}

func parseMinor(serverVersion string) int {
	if serverVersion == "" {
		return 0
	}

	parts := strings.SplitN(serverVersion, ".", 2)
	if len(parts) < 2 {
		return 0
	}

	v, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0
	}

	return v
}

// errWriter collapses repeated write-error checks in the text renderer.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, args ...any) {
	if e.err != nil {
		return
	}

	_, e.err = fmt.Fprintf(e.w, format, args...)
}
