# k8s-gpu-monitor

A lightweight GPU resource monitor for Kubernetes clusters. Tracks GPU utilization, memory usage, and pod allocation across nodes with support for NVIDIA, AMD, and Huawei Ascend GPUs.

## Features

- Real-time GPU utilization monitoring
- Per-pod GPU memory tracking
- Multi-vendor support (NVIDIA, AMD, Ascend)
- JSON/CSV export for analysis
- Alert thresholds for GPU memory
- Web UI dashboard

## Installation

```bash
go install github.com/Ankitavasudev/k8s-gpu-monitor@latest
```

## Usage

```bash
# Monitor all GPU nodes
k8s-gpu-monitor watch

# Monitor specific namespace
k8s-gpu-monitor watch --namespace ml-training

# Export to JSON
k8s-gpu-monitor export --format json --output gpu-stats.json

# Export to CSV
k8s-gpu-monitor export --format csv --output gpu-stats.csv

# Set memory alert threshold
k8s-gpu-monitor watch --alert-threshold 90
```

## Example Output

```
NODE                  GPU     UTIL%   MEM_USED   MEM_TOTAL   PODS
gpu-node-1            A100    87%     31.2 GiB   40.0 GiB    4
gpu-node-2            A100    65%     26.1 GiB   40.0 GiB    3
gpu-node-3            V100    45%     13.5 GiB   32.0 GiB    2
```

## Supported GPU Vendors

| Vendor | Products | Metrics |
|--------|----------|---------|
| NVIDIA | Tesla, A100, V100, T4 | Utilization, Memory, Temperature |
| AMD | MI250X, MI100 | Utilization, Memory |
| Huawei | Ascend 910B | Utilization, Memory |

## Building

```bash
git clone https://github.com/Ankitavasudev/k8s-gpu-monitor.git
cd k8s-gpu-monitor
go build -o k8s-gpu-monitor .
```

## License

MIT
