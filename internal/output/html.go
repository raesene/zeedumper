package output

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"regexp"
	"strings"

	"github.com/raesene/zeedumper/internal/dumper"
)

// View models below carry precomputed, document-unique anchors so the template
// stays declarative — no cross-loop index arithmetic in template land.
type htmlView struct {
	Cluster    string
	Context    string
	Timestamp  string
	Components []componentView
}

type componentView struct {
	Name      string
	Instances []instanceView
}

type instanceView struct {
	Name   string
	Anchor string
	Pages  []pageView
}

type pageView struct {
	Name    string
	Path    string
	Content string
	Error   string
	Anchor  string
	OK      bool
	IsJSON  bool
}

// formatContent checks whether s is valid JSON and, if so, returns a
// pretty-printed version. For non-JSON content the original string is returned
// unchanged.
func formatContent(s string) (formatted string, isJSON bool) {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) == 0 {
		return s, false
	}

	if trimmed[0] != '{' && trimmed[0] != '[' {
		return s, false
	}

	var raw json.RawMessage

	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return s, false
	}

	pretty, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return s, false
	}

	return string(pretty), true
}

var anchorUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func sanitizeAnchor(s string) string {
	return anchorUnsafe.ReplaceAllString(s, "-")
}

func buildView(d *dumper.Dump) htmlView {
	v := htmlView{Cluster: d.Cluster, Context: d.Context, Timestamp: d.Timestamp}
	for _, comp := range d.Components {
		cv := componentView{Name: comp.Name}
		for _, inst := range comp.Instances {
			instAnchor := sanitizeAnchor(comp.Name + "--" + inst.Name)

			iv := instanceView{Name: inst.Name, Anchor: instAnchor}
			for _, page := range inst.Pages {
				content, isJSON := formatContent(page.Content)
				iv.Pages = append(iv.Pages, pageView{
					Name:    page.Name,
					Path:    page.Path,
					Content: content,
					Error:   page.Error,
					Anchor:  instAnchor + "--" + sanitizeAnchor(page.Name),
					OK:      page.OK(),
					IsJSON:  isJSON,
				})
			}

			cv.Instances = append(cv.Instances, iv)
		}

		v.Components = append(v.Components, cv)
	}

	return v
}

func renderHTML(w io.Writer, d *dumper.Dump) error {
	if err := htmlTemplate.Execute(w, buildView(d)); err != nil {
		return fmt.Errorf("rendering html: %w", err)
	}

	return nil
}

// htmlTemplate renders a single self-contained page: a sidebar listing every
// component/instance/page anchor, and a content column with each z-page in a
// <pre> block. No external assets, so the file opens straight from disk.
var htmlTemplate = template.Must(template.New("dump").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>zeedumper — {{.Cluster}}</title>
<style>
  :root { color-scheme: light dark; }
  * { box-sizing: border-box; }
  body { margin: 0; font-family: system-ui, sans-serif; display: flex; }
  nav { width: 280px; min-width: 280px; height: 100vh; overflow-y: auto;
        position: sticky; top: 0; padding: 1rem; border-right: 1px solid #8884;
        background: #00000008; }
  nav h1 { font-size: 1.1rem; margin: 0 0 .25rem; }
  nav .meta { font-size: .75rem; opacity: .7; word-break: break-all; margin-bottom: 1rem; }
  nav ul { list-style: none; padding-left: .75rem; margin: .25rem 0; }
  nav .comp { font-weight: 600; margin-top: .5rem; }
  nav .inst { font-size: .85rem; opacity: .85; }
  nav a { text-decoration: none; color: inherit; }
  nav a:hover { text-decoration: underline; }
  nav a.bad { color: #c0392b; }
  main { flex: 1; padding: 1.5rem 2rem; overflow-x: auto; min-width: 0; }
  section.inst { margin-bottom: 2rem; }
  h2 { border-bottom: 2px solid #8884; padding-bottom: .25rem; }
  h3 { margin-bottom: .25rem; }
  .path { font-family: monospace; font-size: .8rem; opacity: .6; margin-bottom: .25rem; }
  pre { background: #00000010; padding: 1rem; border-radius: 6px;
        overflow-x: auto; white-space: pre-wrap; word-break: break-word; }
  pre.json { white-space: pre; tab-size: 2; font-size: .85rem; line-height: 1.5; }
  .error { background: #c0392b18; border-left: 3px solid #c0392b; padding: .75rem 1rem;
           border-radius: 4px; font-family: monospace; font-size: .85rem; white-space: pre-wrap; }
  .badge { font-size: .7rem; padding: .1rem .4rem; border-radius: 4px;
           background: #2ecc7130; margin-left: .5rem; }
  .badge.err { background: #c0392b30; }
</style>
</head>
<body>
<nav>
  <h1>zeedumper</h1>
  <div class="meta">{{.Cluster}}{{if .Context}}<br>ctx: {{.Context}}{{end}}<br>{{.Timestamp}}</div>
  {{range .Components}}
    <div class="comp">{{.Name}}</div>
    {{range .Instances}}
      <ul class="inst">
        <li><a href="#{{.Anchor}}">{{.Name}}</a>
          <ul>
          {{range .Pages}}
            <li><a class="{{if not .OK}}bad{{end}}" href="#{{.Anchor}}">{{.Name}}</a></li>
          {{end}}
          </ul>
        </li>
      </ul>
    {{end}}
  {{end}}
</nav>
<main>
  {{range .Components}}
    <h2>{{.Name}}</h2>
    {{$comp := .Name}}
    {{range .Instances}}
      <section class="inst" id="{{.Anchor}}">
        <h3>{{$comp}} / {{.Name}}</h3>
        {{range .Pages}}
          <div id="{{.Anchor}}">
            <h3>{{.Name}}
              {{if .OK}}<span class="badge">ok</span>{{else}}<span class="badge err">error</span>{{end}}
            </h3>
            <div class="path">{{.Path}}</div>
            {{if .OK}}
              <pre{{if .IsJSON}} class="json"{{end}}>{{.Content}}</pre>
            {{else}}
              <div class="error">{{.Error}}</div>
            {{end}}
          </div>
        {{end}}
      </section>
    {{end}}
  {{end}}
</main>
</body>
</html>
`))
