// Package output renders a dumper.Dump in text, JSON, or self-contained HTML.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

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
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	return enc.Encode(d)
}

func renderText(w io.Writer, d *dumper.Dump) error {
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
					bw.printf("\n[%s] %s\n", page.Name, page.Path)
					bw.printf("%s\n", strings.TrimRight(page.Content, "\n"))
				} else {
					bw.printf("\n[%s] ERROR: %s\n", page.Name, page.Error)
				}
			}
		}
	}

	return bw.err
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
