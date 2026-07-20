package dumper

import (
	"reflect"
	"testing"
)

func TestOverlayPages(t *testing.T) {
	// Proxy-discovered kubelet instances: statusz carries the proxy's Forbidden
	// error that the node agent should override.
	base := []Instance{
		{Name: "node-a", Pages: []Page{
			{Name: "flagz", Content: "flagz-a"},
			{Name: "statusz", Error: "Forbidden"},
			{Name: "configz", Content: "configz-a"},
		}},
		{Name: "node-b", Pages: []Page{
			{Name: "flagz", Content: "flagz-b"},
			{Name: "statusz", Error: "Forbidden"},
			{Name: "configz", Content: "configz-b"},
		}},
	}

	// Node agent supplies a good statusz for node-a only.
	supplemental := []Instance{
		{Name: "node-a", Pages: []Page{{Name: "statusz", Content: "good-statusz"}}},
	}

	got := overlayPages(base, supplemental)

	// node-a statusz is replaced in place, preserving flagz/statusz/configz order.
	a := got[0]
	if a.Name != "node-a" || len(a.Pages) != 3 {
		t.Fatalf("node-a = %+v", a)
	}

	if a.Pages[1].Name != "statusz" || a.Pages[1].Content != "good-statusz" || a.Pages[1].Error != "" {
		t.Errorf("node-a statusz not overridden in place: %+v", a.Pages)
	}

	// node-b is untouched (agent produced nothing for it): proxy error stands.
	b := got[1]
	if b.Pages[1].Error != "Forbidden" {
		t.Errorf("node-b statusz should retain proxy error, got %+v", b.Pages[1])
	}
}

func TestOverlayPagesAppendsNewInstanceAndPage(t *testing.T) {
	base := []Instance{
		{Name: "node-a", Pages: []Page{{Name: "flagz", Content: "flagz-a"}}},
	}
	supplemental := []Instance{
		{Name: "node-a", Pages: []Page{{Name: "statusz", Content: "s-a"}}}, // new page on existing instance
		{Name: "node-c", Pages: []Page{{Name: "statusz", Content: "s-c"}}}, // brand-new instance
	}

	got := overlayPages(base, supplemental)

	want := []Instance{
		{Name: "node-a", Pages: []Page{{Name: "flagz", Content: "flagz-a"}, {Name: "statusz", Content: "s-a"}}},
		{Name: "node-c", Pages: []Page{{Name: "statusz", Content: "s-c"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
