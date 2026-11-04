package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"sort"
	"strconv"
	"strings"
)

type PodReport struct {
	Namespace  string   `json:"namespace"`
	Name       string   `json:"name"`
	Node       string   `json:"node,omitempty"`
	Phase      string   `json:"phase,omitempty"`
	Requests   int      `json:"requests"`
	Containers []string `json:"containers,omitempty"`
	Note       string   `json:"note,omitempty"`
}

type NodeReport struct {
	Name          string      `json:"name"`
	Product       string      `json:"product"`
	Capacity      int         `json:"capacity"`
	Allocated     int         `json:"allocated"`
	Free          int         `json:"free"`
	Overcommitted bool        `json:"overcommitted"`
	Pods          []PodReport `json:"pods"`
}

type Summary struct {
	Nodes       int `json:"nodes"`
	GPUs        int `json:"gpus"`
	Allocated   int `json:"allocated"`
	Free        int `json:"free"`
	Pods        int `json:"pods"`
	Unscheduled int `json:"unscheduled"`
	Misplaced   int `json:"misplaced"`
}

type Report struct {
	Generated   string       `json:"generated"`
	Source      string       `json:"source"`
	Namespace   string       `json:"namespace,omitempty"`
	Summary     Summary      `json:"summary"`
	Nodes       []NodeReport `json:"nodes"`
	Unscheduled []PodReport  `json:"unscheduled"`
	Misplaced   []PodReport  `json:"misplaced"`
}

func podKey(p PodReport) string {
	return p.Namespace + "/" + p.Name
}

func sortPods(pods []PodReport) {
	sort.SliceStable(pods, func(i, j int) bool {
		return podKey(pods[i]) < podKey(pods[j])
	})
}

// buildReport attributes each GPU request to a node. It deliberately reports
// allocation, not utilisation: utilisation needs a device exporter such as
// DCGM, and inventing it from the API would be dishonest.
func buildReport(capture Capture, namespace string) Report {
	nodes := make(map[string]*NodeReport)
	order := make([]string, 0, len(capture.Nodes))

	for _, node := range capture.Nodes {
		capacity := nodeCapacity(node)
		if capacity == 0 {
			continue
		}
		name := node.Metadata.Name
		nodes[name] = &NodeReport{
			Name:     name,
			Product:  nodeProduct(node),
			Capacity: capacity,
			Pods:     []PodReport{},
		}
		order = append(order, name)
	}
	sort.Strings(order)

	report := Report{
		Namespace:   namespace,
		Nodes:       make([]NodeReport, 0, len(order)),
		Unscheduled: []PodReport{},
		Misplaced:   []PodReport{},
	}

	for _, pod := range capture.Pods {
		if namespace != "" && pod.Metadata.Namespace != namespace {
			continue
		}
		requests, containers := gpuRequests(pod)
		if requests == 0 {
			continue
		}
		item := PodReport{
			Namespace:  pod.Metadata.Namespace,
			Name:       pod.Metadata.Name,
			Node:       pod.Spec.NodeName,
			Phase:      pod.Status.Phase,
			Requests:   requests,
			Containers: containers,
		}

		switch {
		case pod.Spec.NodeName == "":
			item.Note = "no node assigned"
			report.Unscheduled = append(report.Unscheduled, item)
		case nodes[pod.Spec.NodeName] != nil:
			nodes[pod.Spec.NodeName].Pods = append(nodes[pod.Spec.NodeName].Pods, item)
			nodes[pod.Spec.NodeName].Allocated += requests
		default:
			item.Note = "node has no GPU capacity in this capture"
			report.Misplaced = append(report.Misplaced, item)
		}
	}

	for _, name := range order {
		node := nodes[name]
		node.Free = node.Capacity - node.Allocated
		if node.Free < 0 {
			node.Free = 0
			node.Overcommitted = true
		}
		sortPods(node.Pods)
		report.Nodes = append(report.Nodes, *node)
		report.Summary.Nodes++
		report.Summary.GPUs += node.Capacity
		report.Summary.Allocated += node.Allocated
		report.Summary.Free += node.Free
	}

	sortPods(report.Unscheduled)
	sortPods(report.Misplaced)
	report.Summary.Pods = len(report.Unscheduled) + len(report.Misplaced)
	for _, node := range report.Nodes {
		report.Summary.Pods += len(node.Pods)
	}
	report.Summary.Unscheduled = len(report.Unscheduled)
	report.Summary.Misplaced = len(report.Misplaced)
	return report
}

func renderText(w io.Writer, report Report) error {
	fmt.Fprintf(w, "GPU allocation — %s\n", report.Source)
	fmt.Fprintf(w, "Generated %s", report.Generated)
	if report.Namespace != "" {
		fmt.Fprintf(w, " · namespace %s", report.Namespace)
	}
	fmt.Fprintln(w)

	if len(report.Nodes) == 0 {
		fmt.Fprintln(w, "\nNo nodes with GPU capacity in this capture.")
	} else {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%-24s %-22s %5s %6s %5s %5s\n", "NODE", "PRODUCT", "CAP", "ALLOC", "FREE", "PODS")
		fmt.Fprintln(w, strings.Repeat("-", 72))
		for _, node := range report.Nodes {
			product := node.Product
			if product == "" {
				product = "-"
			}
			fmt.Fprintf(w, "%-24s %-22s %5d %6d %5d %5d\n",
				node.Name, product, node.Capacity, node.Allocated, node.Free, len(node.Pods))
		}
		fmt.Fprintln(w, strings.Repeat("-", 72))
		fmt.Fprintf(w, "%-24s %-22s %5d %6d %5d %5d\n",
			"TOTAL", "", report.Summary.GPUs, report.Summary.Allocated, report.Summary.Free, report.Summary.Pods)
	}

	if len(report.Unscheduled) > 0 {
		fmt.Fprintf(w, "\nUnscheduled GPU pods (%d)\n", len(report.Unscheduled))
		for _, pod := range report.Unscheduled {
			fmt.Fprintf(w, "  %-40s requests %d  phase %s\n", podKey(pod), pod.Requests, pod.Phase)
		}
	}
	if len(report.Misplaced) > 0 {
		fmt.Fprintf(w, "\nGPU pods on nodes without GPU capacity (%d)\n", len(report.Misplaced))
		for _, pod := range report.Misplaced {
			fmt.Fprintf(w, "  %-40s requests %d  node %s\n", podKey(pod), pod.Requests, pod.Node)
		}
	}
	if len(report.Nodes) > 0 && report.Summary.Unscheduled == 0 && report.Summary.Misplaced == 0 {
		fmt.Fprintln(w, "\nEvery GPU request is assigned to a GPU node.")
	}
	return nil
}

func renderJSON(w io.Writer, report Report) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func renderCSV(w io.Writer, report Report) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"node", "product", "capacity", "allocated", "free", "pods", "overcommitted"}); err != nil {
		return err
	}
	for _, node := range report.Nodes {
		if err := cw.Write([]string{
			node.Name,
			node.Product,
			strconv.Itoa(node.Capacity),
			strconv.Itoa(node.Allocated),
			strconv.Itoa(node.Free),
			strconv.Itoa(len(node.Pods)),
			strconv.FormatBool(node.Overcommitted),
		}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

var reportTemplate = template.Must(template.New("report").Funcs(template.FuncMap{
	"percent": percent,
}).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>k8s-gpu-monitor report</title>
<style>
:root{--bg:#0b0f14;--panel:#121b23;--card:#16232d;--line:#23343f;--text:#eaf1f8;--muted:#8ba3b5;--accent:#00adb5;--ok:#3fb950;--warn:#d29922;--bad:#f85149}
*{box-sizing:border-box}
body{margin:0;background:radial-gradient(1000px 500px at 50% -10%,#123039 0%,var(--bg) 55%);color:var(--text);font:15px/1.5 ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
main{max-width:1080px;margin:0 auto;padding:40px 20px 64px}
h1{font-size:26px;margin:0 0 4px}
.sub{color:var(--muted);font-size:13px;margin-bottom:24px}
.cards{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px;margin-bottom:22px}
@media(max-width:760px){.cards{grid-template-columns:repeat(2,minmax(0,1fr))}}
.card{background:var(--panel);border:1px solid var(--line);border-radius:12px;padding:14px}
.card .k{color:var(--muted);font-size:12px;text-transform:uppercase;letter-spacing:.06em}
.card .v{font-size:26px;font-weight:700;margin-top:2px}
.card .v.ok{color:var(--ok)}.card .v.warn{color:var(--warn)}.card .v.bad{color:var(--bad)}
.node{background:var(--panel);border:1px solid var(--line);border-radius:12px;padding:16px;margin-bottom:12px}
.node.over{border-color:var(--bad)}
.node h2{font-size:16px;margin:0;display:flex;gap:10px;align-items:baseline;flex-wrap:wrap}
.node h2 .product{color:var(--muted);font-size:13px;font-weight:400}
.node h2 .badge{margin-left:auto;font-size:11px;border-radius:999px;padding:2px 9px;border:1px solid var(--line);color:var(--muted)}
.node h2 .badge.over{color:var(--bad);border-color:var(--bad)}
.bar{height:10px;border-radius:999px;background:#0b141a;overflow:hidden;margin:12px 0 8px}
.bar span{display:block;height:100%;background:linear-gradient(90deg,#00adb5,#3fb950)}
.node.over .bar span{background:linear-gradient(90deg,#d29922,#f85149)}
.meta{display:flex;gap:16px;flex-wrap:wrap;color:var(--muted);font-size:12px}
table{width:100%;border-collapse:collapse;margin-top:12px;font-size:13px}
th,td{text-align:left;padding:7px 8px;border-bottom:1px solid var(--line)}
th{color:var(--muted);font-weight:500;font-size:11px;text-transform:uppercase;letter-spacing:.05em}
td.num{font-family:ui-monospace,Consolas,monospace}
code{font-family:ui-monospace,SFMono-Regular,Consolas,monospace;background:#0b141a;border:1px solid var(--line);border-radius:4px;padding:1px 6px;font-size:12px}
section.block{margin-top:26px}
section.block h2{font-size:16px;margin:0 0 4px}
section.block p.hint{color:var(--muted);font-size:13px;margin:0 0 10px}
.row{display:flex;gap:10px;align-items:center;flex-wrap:wrap;background:var(--card);border:1px solid var(--line);border-radius:9px;padding:9px 12px;margin-bottom:8px;font-size:13px}
.row .name{font-weight:600}
.row .muted{color:var(--muted)}
.empty{color:var(--muted);font-size:13px}
footer{color:var(--muted);font-size:12px;margin-top:30px}
</style>
</head>
<body>
<main>
<h1>GPU allocation</h1>
<div class="sub">Source: {{.Source}} · generated {{.Generated}}{{if .Namespace}} · namespace {{.Namespace}}{{end}}</div>

<div class="cards">
  <div class="card"><div class="k">GPU nodes</div><div class="v">{{.Summary.Nodes}}</div></div>
  <div class="card"><div class="k">GPUs</div><div class="v">{{.Summary.GPUs}}</div></div>
  <div class="card"><div class="k">Allocated</div><div class="v">{{.Summary.Allocated}}</div></div>
  <div class="card"><div class="k">Free</div><div class="v {{if eq .Summary.Free 0}}warn{{else}}ok{{end}}">{{.Summary.Free}}</div></div>
</div>

{{if .Nodes}}{{range .Nodes}}<article class="node{{if .Overcommitted}} over{{end}}">
<h2>{{.Name}} <span class="product">{{if .Product}}{{.Product}}{{else}}unknown product{{end}}</span>
{{if .Overcommitted}}<span class="badge over">over-allocated</span>{{else}}<span class="badge">{{.Free}} free</span>{{end}}</h2>
<div class="bar"><span style="width:{{percent .Allocated .Capacity}}%"></span></div>
<div class="meta"><span>{{.Allocated}} / {{.Capacity}} allocated</span><span>{{len .Pods}} GPU pod{{if ne (len .Pods) 1}}s{{end}}</span></div>
{{if .Pods}}<table>
<thead><tr><th>Pod</th><th>Phase</th><th>GPUs</th><th>Containers</th></tr></thead>
<tbody>{{range .Pods}}<tr><td><code>{{.Namespace}}/{{.Name}}</code></td><td>{{if .Phase}}{{.Phase}}{{else}}-{{end}}</td><td class="num">{{.Requests}}</td><td>{{range $i, $c := .Containers}}{{if $i}}, {{end}}{{$c}}{{end}}</td></tr>{{end}}</tbody>
</table>{{else}}<p class="empty">No GPU requests on this node.</p>{{end}}
</article>{{end}}{{else}}<p class="empty">No nodes with GPU capacity in this capture.</p>{{end}}

{{if .Unscheduled}}<section class="block">
<h2>Unscheduled GPU pods ({{len .Unscheduled}})</h2>
<p class="hint">These request GPUs but have no node assigned. They are waiting on capacity, a taint, or a scheduler decision.</p>
{{range .Unscheduled}}<div class="row"><span class="name">{{.Namespace}}/{{.Name}}</span><span class="muted">requests {{.Requests}}</span>{{if .Phase}}<span class="muted">phase {{.Phase}}</span>{{end}}</div>{{end}}
</section>{{end}}

{{if .Misplaced}}<section class="block">
<h2>GPU pods on nodes without GPU capacity ({{len .Misplaced}})</h2>
<p class="hint">The pod asks for a GPU but sits on a node this capture does not show advertising one. Check the device plugin and the pod's node assignment.</p>
{{range .Misplaced}}<div class="row"><span class="name">{{.Namespace}}/{{.Name}}</span><span class="muted">requests {{.Requests}}</span><span class="muted">node {{.Node}}</span></div>{{end}}
</section>{{end}}

<footer>Generated by k8s-gpu-monitor {{.Version}}. Allocation comes from pod requests; utilisation needs a device exporter such as DCGM.</footer>
</main>
</body>
</html>
`))

func percent(part, total int) int {
	if total <= 0 {
		return 0
	}
	value := part * 100 / total
	if value > 100 {
		return 100
	}
	return value
}

func renderHTML(w io.Writer, report Report) error {
	return reportTemplate.Execute(w, struct {
		Report
		Version string
	}{report, versionString()})
}
