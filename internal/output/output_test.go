package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/raesene/zeedumper/internal/dumper"
)

func sampleDump() *dumper.Dump {
	return &dumper.Dump{
		Cluster:   "https://example:6443",
		Context:   "test-ctx",
		Timestamp: "2026-01-01T00:00:00Z",
		Components: []dumper.Component{{
			Name: "kube-apiserver",
			Instances: []dumper.Instance{{
				Name: "apiserver",
				Pages: []dumper.Page{
					{Name: "flagz", Path: "/flagz", Content: "v=1"},
					{Name: "statusz", Path: "/statusz", Error: "forbidden"},
				},
			}},
		}},
	}
}

func TestParseFormat(t *testing.T) {
	for _, in := range []string{"text", "JSON", "Html"} {
		if _, err := ParseFormat(in); err != nil {
			t.Errorf("ParseFormat(%q) unexpected error: %v", in, err)
		}
	}
	if _, err := ParseFormat("yaml"); err == nil {
		t.Error("ParseFormat(yaml) should error")
	}
}

func TestRenderJSONRoundTrips(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, sampleDump(), FormatJSON); err != nil {
		t.Fatal(err)
	}
	var got dumper.Dump
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if got.Components[0].Instances[0].Pages[1].Error != "forbidden" {
		t.Errorf("error field lost in round trip: %+v", got)
	}
}

func TestRenderTextIncludesContentAndErrors(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, sampleDump(), FormatText); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"kube-apiserver", "v=1", "ERROR: forbidden"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q", want)
		}
	}
}

func TestRenderHTMLEscapesAndAnchors(t *testing.T) {
	d := sampleDump()
	d.Components[0].Instances[0].Pages[0].Content = "<script>x</script>"
	var buf bytes.Buffer
	if err := Render(&buf, d, FormatHTML); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "<script>x</script>") {
		t.Error("html output did not escape page content")
	}
	if !strings.Contains(out, `id="kube-apiserver--apiserver"`) {
		t.Error("html output missing instance anchor")
	}
}
