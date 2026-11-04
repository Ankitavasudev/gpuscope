package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Capture is the saved form of the two API responses the tool needs. Keeping it
// in one file makes a report reproducible: the same capture always produces the
// same numbers, on any machine, without a cluster.
type Capture struct {
	Nodes []Node `json:"nodes"`
	Pods  []Pod  `json:"pods"`
}

type ObjectMeta struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Labels    map[string]string `json:"labels"`
}

type Node struct {
	Metadata ObjectMeta `json:"metadata"`
	Status   struct {
		Capacity    map[string]string `json:"capacity"`
		Allocatable map[string]string `json:"allocatable"`
	} `json:"status"`
}

type Pod struct {
	Metadata ObjectMeta `json:"metadata"`
	Spec     struct {
		NodeName   string      `json:"nodeName"`
		Containers []Container `json:"containers"`
	} `json:"spec"`
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

type Container struct {
	Name      string `json:"name"`
	Resources struct {
		Requests map[string]string `json:"requests"`
		Limits   map[string]string `json:"limits"`
	} `json:"resources"`
}

// isGPUResource matches the extended resource names vendors use for GPUs.
// Only the resource name is inspected, never the vendor prefix alone, because
// labels such as nvidia.com/gpu.present also contain "gpu" but are booleans,
// not countable resources.
func isGPUResource(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	// Intel publishes one resource per generation, so the vendor prefix is the
	// only stable part: gpu.intel.com/i915, gpu.intel.com/xe.
	if strings.HasPrefix(k, "gpu.intel.com/") {
		return true
	}
	name := k
	if slash := strings.LastIndex(k, "/"); slash >= 0 {
		name = k[slash+1:]
	}
	switch {
	case name == "gpu":
		return true
	case strings.HasPrefix(name, "mig-"):
		return true
	case strings.HasPrefix(name, "ascend"):
		return true
	}
	return false
}

// resourceCount sums every GPU extended resource in a requests/limits map.
func resourceCount(resources map[string]string) int {
	total := 0
	for key, value := range resources {
		if !isGPUResource(key) {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil && n > 0 {
			total += n
		}
	}
	return total
}

func nodeCapacity(node Node) int {
	capacity := resourceCount(node.Status.Allocatable)
	if capacity == 0 {
		capacity = resourceCount(node.Status.Capacity)
	}
	// Some device plugins only publish the count as a label.
	if label := node.Metadata.Labels["nvidia.com/gpu.count"]; label != "" {
		if n, err := strconv.Atoi(label); err == nil && n > capacity {
			capacity = n
		}
	}
	if label := node.Metadata.Labels["amd.com/gpu.count"]; label != "" {
		if n, err := strconv.Atoi(label); err == nil && n > capacity {
			capacity = n
		}
	}
	return capacity
}

func nodeProduct(node Node) string {
	for _, key := range []string{
		"nvidia.com/gpu.product",
		"amd.com/gpu.product",
		"gpu.product",
		"hardware.gpu.product",
	} {
		if value := strings.TrimSpace(node.Metadata.Labels[key]); value != "" {
			return value
		}
	}
	if nodeCapacity(node) > 0 {
		return "unknown"
	}
	return ""
}

// loadNodes accepts a NodeList (kubectl get nodes -o json), a single Node, or
// the nodes array of a combined capture.
func loadNodes(path string) ([]Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	nodes, err := parseNodes(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return nodes, nil
}

func parseNodes(data []byte) ([]Node, error) {
	var list struct {
		Items []Node `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err == nil && list.Items != nil {
		return list.Items, nil
	}
	var wrapped struct {
		Nodes []Node `json:"nodes"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Nodes != nil {
		return wrapped.Nodes, nil
	}
	var single Node
	if err := json.Unmarshal(data, &single); err == nil && single.Metadata.Name != "" {
		return []Node{single}, nil
	}
	return nil, errors.New("does not look like a NodeList, a Node, or a capture with a nodes array")
}

// loadPods accepts a PodList (kubectl get pods -A -o json), a single Pod, or
// the pods array of a combined capture.
func loadPods(path string) ([]Pod, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pods, err := parsePods(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return pods, nil
}

func parsePods(data []byte) ([]Pod, error) {
	var list struct {
		Items []Pod `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err == nil && list.Items != nil {
		return list.Items, nil
	}
	var wrapped struct {
		Pods []Pod `json:"pods"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Pods != nil {
		return wrapped.Pods, nil
	}
	var single Pod
	if err := json.Unmarshal(data, &single); err == nil && single.Metadata.Name != "" {
		return []Pod{single}, nil
	}
	return nil, errors.New("does not look like a PodList, a Pod, or a capture with a pods array")
}

func loadCapture(path string) (Capture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Capture{}, err
	}
	var capture Capture
	if err := json.Unmarshal(data, &capture); err != nil {
		return Capture{}, fmt.Errorf("%s is not a capture: %w", path, err)
	}
	if capture.Nodes == nil && capture.Pods == nil {
		return Capture{}, fmt.Errorf("%s is not a capture: expected nodes and pods arrays", path)
	}
	return capture, nil
}

func gpuRequests(pod Pod) (int, []string) {
	total := 0
	var containers []string
	for _, container := range pod.Spec.Containers {
		requested := resourceCount(container.Resources.Requests)
		if requested == 0 {
			requested = resourceCount(container.Resources.Limits)
		}
		if requested > 0 {
			total += requested
			containers = append(containers, container.Name)
		}
	}
	return total, containers
}
