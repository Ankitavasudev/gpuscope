# k8s-gpu-monitor

Report how many GPUs each Kubernetes node advertises, how many pods have asked
for, and where the requests actually landed. It reads a saved capture, a pair of
`kubectl` outputs, or the current cluster — no GPU, device plugin, or exporter
required.

```console
$ k8s-gpu-monitor --from examples/capture.json
GPU allocation — examples/capture.json
Generated 2026-11-04 23:54 IST

NODE                     PRODUCT                  CAP  ALLOC  FREE  PODS
------------------------------------------------------------------------
gpu-node-1               NVIDIA-A100-SXM4-40GB      4      3     1     2
gpu-node-2               NVIDIA-A100-SXM4-40GB      4      2     2     2
gpu-node-3               Tesla-V100-SXM2-32GB       2      1     1     1
------------------------------------------------------------------------
TOTAL                                              10      6     4     6

Unscheduled GPU pods (1)
  ml-training/train-gpt-v3                 requests 2  phase Pending
```

## What it measures

This is an **allocation** report, not a utilisation monitor. It reads
`status.allocatable` (falling back to `status.capacity`) from Nodes and
`resources.requests` (falling back to `limits`) from Pods. That data is always
present in the API, so the report works on any cluster.

Actual GPU utilisation and memory use need a device exporter such as
[NVIDIA DCGM](https://github.com/NVIDIA/dcgm-exporter). This tool does not
invent those numbers. If you need them, scrape DCGM separately.

The report flags three states:

| State | Meaning |
| --- | --- |
| Allocated | The pod requests a GPU and sits on a node that advertises one. |
| Unscheduled | The pod requests a GPU but has no `spec.nodeName` yet. |
| Misplaced | The pod requests a GPU but its node advertises none in this capture. |

## Install

```bash
go install github.com/Ankitavasudev/k8s-gpu-monitor@latest
```

Requires Go 1.21 or newer.

## Usage

```bash
# A saved capture with nodes and pods arrays
k8s-gpu-monitor --from examples/capture.json

# Two raw kubectl outputs
kubectl get nodes -o json > nodes.json
kubectl get pods -A -o json > pods.json
k8s-gpu-monitor --nodes nodes.json --pods pods.json

# The current kubectl context
k8s-gpu-monitor

# Only one namespace
k8s-gpu-monitor --namespace ml-training

# Machine-readable output
k8s-gpu-monitor --from examples/capture.json -o json
k8s-gpu-monitor --from examples/capture.json -o csv

# Self-contained HTML report
k8s-gpu-monitor --from examples/capture.json -o html > gpu-report.html
```

`--from` also accepts a plain `NodeList` or `PodList` when you only want one
side of the picture, but a combined capture is the normal input.

## Capture format

A capture is one JSON file with `nodes` and `pods` arrays in the Kubernetes API
shape. Only the fields the report needs are read, so extra fields are ignored:

```json
{
  "nodes": [
    {
      "metadata": {
        "name": "gpu-node-1",
        "labels": { "nvidia.com/gpu.product": "NVIDIA-A100-SXM4-40GB" }
      },
      "status": {
        "allocatable": { "nvidia.com/gpu": "4" }
      }
    }
  ],
  "pods": [
    {
      "metadata": { "name": "train-bert", "namespace": "ml-training" },
      "spec": {
        "nodeName": "gpu-node-1",
        "containers": [
          {
            "name": "trainer",
            "resources": { "requests": { "nvidia.com/gpu": "2" } }
          }
        ]
      },
      "status": { "phase": "Running" }
    }
  ]
}
```

## Supported GPU resources

The counter recognises the extended resource names vendors publish:

- `nvidia.com/gpu` and `nvidia.com/mig-*`
- `amd.com/gpu`
- `huawei.com/Ascend*`
- `gpu.intel.com/*`
- any resource whose name is exactly `gpu`

Labels such as `nvidia.com/gpu.present` and `nvidia.com/gpu.product` are not
counted. The node product name comes from `nvidia.com/gpu.product`,
`amd.com/gpu.product`, `gpu.product`, or `hardware.gpu.product`.

## Output

| Format | Use |
| --- | --- |
| `text` (default) | Terminal summary, sorted by node name |
| `json` | Full report with per-node pod lists |
| `csv` | One row per GPU node, for spreadsheets |
| `html` | Single file with inline CSS, no external assets |

All output is deterministic for the same capture: nodes sort by name, pods by
`namespace/name`.

## Demo

A generated report for the example capture is published at
<https://ankitavasudev.github.io/k8s-gpu-monitor/>.

## Limitations

- Counts GPUs, not GPU memory or compute.
- Does not query Prometheus, DCGM, or any vendor API.
- Live mode shells out to `kubectl` and uses whatever context is selected.
- MIG profiles are counted as whole devices, because that is how they are
  requested; the report does not model slice geometry.

## License

MIT
