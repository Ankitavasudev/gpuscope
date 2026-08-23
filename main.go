package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type GPUInfo struct {
	Node      string  `json:"node"`
	GPUType   string  `json:"gpu_type"`
	Index     int     `json:"index"`
	UtilPct   float64 `json:"util_pct"`
	MemUsed   float64 `json:"mem_used_gi"`
	MemTotal  float64 `json:"mem_total_gi"`
	PodName   string  `json:"pod_name,omitempty"`
	Namespace string  `json:"namespace,omitempty"`
}

type GPUMonitor struct {
	clientset *kubernetes.Clientset
}

func NewGPUMonitor(kubeconfig string) (*GPUMonitor, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("building config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating clientset: %w", err)
	}

	return &GPUMonitor{clientset: clientset}, nil
}

func (m *GPUMonitor) GetGPUs(ctx context.Context) ([]GPUInfo, error) {
	nodes, err := m.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: "nvidia.com/gpu.present=true",
	})
	if err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}

	var gpus []GPUInfo
	for _, node := range nodes.Items {
		gpuCount := node.Labels["nvidia.com/gpu.count"]
		gpuType := node.Labels["nvidia.com/gpu.product"]

		var count int
		fmt.Sscanf(gpuCount, "%d", &count)

		for i := 0; i < count; i++ {
			gpus = append(gpus, GPUInfo{
				Node:    node.Name,
				GPUType: gpuType,
				Index:   i,
			})
		}
	}

	return gpus, nil
}

func (m *GPUMonitor) ExportJSON(gpus []GPUInfo) error {
	data, err := json.MarshalIndent(gpus, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling JSON: %w", err)
	}
	return os.WriteFile("gpu-stats.json", data, 0644)
}

func (m *GPUMonitor) ExportCSV(gpus []GPUInfo) error {
	f, err := os.Create("gpu-stats.csv")
	if err != nil {
		return fmt.Errorf("creating file: %w", err)
	}
	defer f.Close()

	fmt.Fprintln(f, "node,gpu_type,index,util_pct,mem_used_gi,mem_total_gi,pod,namespace")
	for _, gpu := range gpus {
		fmt.Fprintf(f, "%s,%s,%d,%.1f,%.1f,%.1f,%s,%s\n",
			gpu.Node, gpu.GPUType, gpu.Index,
			gpu.UtilPct, gpu.MemUsed, gpu.MemTotal,
			gpu.PodName, gpu.Namespace)
	}
	return nil
}

func main() {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.Getenv("HOME") + "/.kube/config"
	}

	monitor, err := NewGPUMonitor(kubeconfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	for {
		gpus, err := monitor.GetGPUs(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting GPUs: %v\n", err)
			time.Sleep(5 * time.Second)
			continue
		}

		fmt.Printf("\033[2J\033[H")
		fmt.Printf("Kubernetes GPU Monitor - %s\n", time.Now().Format("15:04:05"))
		fmt.Println("================================================")
		fmt.Printf("%-20s %-10s %-6s %-10s %-10s %-10s\n",
			"NODE", "GPU", "UTIL%", "MEM_USED", "MEM_TOTAL", "PODS")
		fmt.Println("------------------------------------------------")

		for _, gpu := range gpus {
			fmt.Printf("%-20s %-10s %-5.0f%% %-10.1f %-10.1f\n",
				gpu.Node, gpu.GPUType, gpu.UtilPct,
				gpu.MemUsed, gpu.MemTotal)
		}

		time.Sleep(2 * time.Second)
	}
}
