package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
)

func testNode(name, product string, capacity int) Node {
	node := Node{Metadata: ObjectMeta{Name: name, Labels: map[string]string{}}}
	node.Status.Capacity = map[string]string{"nvidia.com/gpu": strconv.Itoa(capacity)}
	node.Status.Allocatable = map[string]string{"nvidia.com/gpu": strconv.Itoa(capacity)}
	if product != "" {
		node.Metadata.Labels["nvidia.com/gpu.product"] = product
	}
	return node
}

func testPod(namespace, name, node string, gpus int, phase string) Pod {
	pod := Pod{Metadata: ObjectMeta{Name: name, Namespace: namespace}}
	pod.Spec.NodeName = node
	pod.Spec.Containers = []Container{{Name: "main"}}
	pod.Spec.Containers[0].Resources.Requests = map[string]string{}
	if gpus > 0 {
		pod.Spec.Containers[0].Resources.Requests["nvidia.com/gpu"] = strconv.Itoa(gpus)
	}
	pod.Status.Phase = phase
	return pod
}

func TestIsGPUResource(t *testing.T) {
	yes := []string{"nvidia.com/gpu", "amd.com/gpu", "nvidia.com/mig-1g.5gb", "huawei.com/Ascend910", "gpu.intel.com/i915"}
	no := []string{"cpu", "memory", "nvidia.com/gpu.present", "example.com/device"}
	for _, key := range yes {
		if !isGPUResource(key) {
			t.Errorf("%q should be a GPU resource", key)
		}
	}
	for _, key := range no {
		if isGPUResource(key) {
			t.Errorf("%q should not be a GPU resource", key)
		}
	}
}

func TestResourceCountSumsEveryVendor(t *testing.T) {
	got := resourceCount(map[string]string{
		"cpu":            "8",
		"nvidia.com/gpu": "2",
		"amd.com/gpu":    "1",
		"memory":         "16Gi",
	})
	if got != 3 {
		t.Errorf("resourceCount() = %d, want 3", got)
	}
	if got := resourceCount(map[string]string{"cpu": "8"}); got != 0 {
		t.Errorf("resourceCount() with no GPU = %d, want 0", got)
	}
	if got := resourceCount(map[string]string{"nvidia.com/gpu": "not-a-number"}); got != 0 {
		t.Errorf("resourceCount() with a bad value = %d, want 0", got)
	}
}

func TestNodeCapacityFallsBackToLabel(t *testing.T) {
	node := Node{Metadata: ObjectMeta{Name: "n", Labels: map[string]string{"nvidia.com/gpu.count": "8"}}}
	if got := nodeCapacity(node); got != 8 {
		t.Errorf("nodeCapacity() = %d, want the label count 8", got)
	}
}

func TestNodeProduct(t *testing.T) {
	node := testNode("n", "NVIDIA-A100-SXM4-40GB", 4)
	if got := nodeProduct(node); got != "NVIDIA-A100-SXM4-40GB" {
		t.Errorf("nodeProduct() = %q", got)
	}
	if got := nodeProduct(testNode("n", "", 4)); got != "unknown" {
		t.Errorf("nodeProduct() without a label = %q, want unknown", got)
	}
	if got := nodeProduct(testNode("n", "", 0)); got != "" {
		t.Errorf("nodeProduct() on a CPU node = %q, want empty", got)
	}
}

func TestBuildReportAttributesRequests(t *testing.T) {
	capture := Capture{
		Nodes: []Node{
			testNode("gpu-b", "A100", 4),
			testNode("gpu-a", "A100", 2),
			testNode("cpu", "", 0),
		},
		Pods: []Pod{
			testPod("ml", "train", "gpu-a", 1, "Running"),
			testPod("ml", "serve", "gpu-b", 3, "Running"),
			testPod("web", "nginx", "cpu", 0, "Running"),
		},
	}

	report := buildReport(capture, "")
	if report.Summary.Nodes != 2 {
		t.Fatalf("nodes = %d, want 2 GPU nodes", report.Summary.Nodes)
	}
	if report.Summary.GPUs != 6 || report.Summary.Allocated != 4 || report.Summary.Free != 2 {
		t.Errorf("summary = %+v, want 6 GPUs / 4 allocated / 2 free", report.Summary)
	}
	if report.Nodes[0].Name != "gpu-a" || report.Nodes[1].Name != "gpu-b" {
		t.Errorf("nodes are not sorted: %q, %q", report.Nodes[0].Name, report.Nodes[1].Name)
	}
	if report.Nodes[1].Allocated != 3 || report.Nodes[1].Free != 1 {
		t.Errorf("gpu-b = %+v", report.Nodes[1])
	}
}

func TestBuildReportClampsOvercommit(t *testing.T) {
	capture := Capture{
		Nodes: []Node{testNode("gpu", "A100", 1)},
		Pods:  []Pod{testPod("ml", "greedy", "gpu", 3, "Running")},
	}
	report := buildReport(capture, "")
	node := report.Nodes[0]
	if node.Free != 0 {
		t.Errorf("free = %d, want 0", node.Free)
	}
	if !node.Overcommitted {
		t.Error("expected the node to be marked over-committed")
	}
}

func TestBuildReportFindsUnscheduledAndMisplaced(t *testing.T) {
	capture := Capture{
		Nodes: []Node{testNode("gpu", "A100", 2)},
		Pods: []Pod{
			testPod("ml", "waiting", "", 1, "Pending"),
			testPod("ml", "stranded", "cpu", 1, "Pending"),
			testPod("ml", "missing-node", "not-in-capture", 1, "Running"),
		},
	}
	report := buildReport(capture, "")
	if report.Summary.Unscheduled != 1 || report.Summary.Misplaced != 2 {
		t.Errorf("summary = %+v, want 1 unscheduled and 2 misplaced", report.Summary)
	}
	if report.Unscheduled[0].Name != "waiting" {
		t.Errorf("unscheduled = %+v", report.Unscheduled)
	}
	if report.Misplaced[0].Name != "missing-node" {
		t.Errorf("misplaced should be sorted by namespace/name, got %+v", report.Misplaced)
	}
}

func TestBuildReportNamespaceFilter(t *testing.T) {
	capture := Capture{
		Nodes: []Node{testNode("gpu", "A100", 4)},
		Pods: []Pod{
			testPod("ml", "train", "gpu", 2, "Running"),
			testPod("other", "job", "gpu", 1, "Running"),
		},
	}
	report := buildReport(capture, "ml")
	if report.Summary.Allocated != 2 {
		t.Errorf("allocated = %d, want only the ml pod's 2", report.Summary.Allocated)
	}
}

func TestBuildReportIsDeterministic(t *testing.T) {
	capture := Capture{
		Nodes: []Node{testNode("b", "A100", 2), testNode("a", "V100", 2)},
		Pods: []Pod{
			testPod("z", "last", "a", 1, "Running"),
			testPod("a", "first", "a", 1, "Running"),
		},
	}
	first := buildReport(capture, "")
	second := buildReport(capture, "")
	if first.Nodes[0].Name != second.Nodes[0].Name || first.Nodes[0].Pods[0].Name != second.Nodes[0].Pods[0].Name {
		t.Error("buildReport is not deterministic")
	}
	if first.Nodes[0].Pods[0].Name != "first" {
		t.Errorf("pods are not sorted: %+v", first.Nodes[0].Pods)
	}
}

func TestRenderText(t *testing.T) {
	report := buildReport(Capture{
		Nodes: []Node{testNode("gpu-a", "A100", 4)},
		Pods:  []Pod{testPod("ml", "train", "gpu-a", 2, "Running")},
	}, "")
	var out bytes.Buffer
	if err := renderText(&out, report); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"GPU allocation", "gpu-a", "A100", "TOTAL", "2"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("text output is missing %q", want)
		}
	}
}

func TestRenderJSONRoundTrips(t *testing.T) {
	report := buildReport(Capture{
		Nodes: []Node{testNode("gpu-a", "A100", 4)},
		Pods:  []Pod{testPod("ml", "train", "gpu-a", 2, "Running")},
	}, "")
	var out bytes.Buffer
	if err := renderJSON(&out, report); err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if decoded.Summary.Allocated != 2 {
		t.Errorf("decoded allocated = %d, want 2", decoded.Summary.Allocated)
	}
}

func TestRenderCSV(t *testing.T) {
	report := buildReport(Capture{
		Nodes: []Node{testNode("gpu-a", "A100", 4)},
		Pods:  []Pod{testPod("ml", "train", "gpu-a", 2, "Running")},
	}, "")
	var out bytes.Buffer
	if err := renderCSV(&out, report); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected a header and one row, got %d lines", len(lines))
	}
	if lines[0] != "node,product,capacity,allocated,free,pods,overcommitted" {
		t.Errorf("header = %q", lines[0])
	}
	if lines[1] != "gpu-a,A100,4,2,2,1,false" {
		t.Errorf("row = %q", lines[1])
	}
}

func TestRenderHTMLIsSelfContainedAndEscaped(t *testing.T) {
	report := buildReport(Capture{
		Nodes: []Node{testNode("<script>alert(1)</script>", "A&B", 2)},
		Pods:  []Pod{testPod("ml", "<b>train</b>", "<script>alert(1)</script>", 1, "Running")},
	}, "")
	var out bytes.Buffer
	if err := renderHTML(&out, report); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{"<!DOCTYPE html>", "<style>", "GPU allocation", "Allocation comes from pod requests"} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML is missing %q", want)
		}
	}
	if strings.Contains(html, "<script") {
		t.Error("HTML contains a script tag")
	}
	if strings.Contains(html, "<b>train</b>") {
		t.Error("pod name was not escaped")
	}
	if !strings.Contains(html, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("node name was not escaped")
	}
	if strings.Contains(html, `<link`) || strings.Contains(html, `src="http`) {
		t.Error("HTML loads an external resource")
	}
}

func TestPercentClamps(t *testing.T) {
	cases := []struct{ part, total, want int }{
		{0, 0, 0}, {0, 4, 0}, {2, 4, 50}, {4, 4, 100}, {8, 4, 100},
	}
	for _, c := range cases {
		if got := percent(c.part, c.total); got != c.want {
			t.Errorf("percent(%d, %d) = %d, want %d", c.part, c.total, got, c.want)
		}
	}
}

func TestExampleCaptureLoads(t *testing.T) {
	capture, err := loadCapture("examples/capture.json")
	if err != nil {
		t.Fatal(err)
	}
	report := buildReport(capture, "")
	if report.Summary.Nodes != 3 {
		t.Errorf("example has %d GPU nodes, want 3", report.Summary.Nodes)
	}
	if report.Summary.GPUs != 10 || report.Summary.Allocated != 6 || report.Summary.Free != 4 {
		t.Errorf("example summary = %+v, want 10 / 6 / 4", report.Summary)
	}
	if report.Summary.Unscheduled != 1 {
		t.Errorf("example unscheduled = %d, want 1", report.Summary.Unscheduled)
	}
}

func TestVersionString(t *testing.T) {
	if strings.TrimSpace(versionString()) == "" {
		t.Error("versionString() is empty")
	}
}

func TestCaptureFileIsValidJSON(t *testing.T) {
	data, err := os.ReadFile("examples/capture.json")
	if err != nil {
		t.Fatal(err)
	}
	var capture Capture
	if err := json.Unmarshal(data, &capture); err != nil {
		t.Fatalf("examples/capture.json is not valid JSON: %v", err)
	}
	if len(capture.Nodes) == 0 || len(capture.Pods) == 0 {
		t.Error("example capture is missing nodes or pods")
	}
}
