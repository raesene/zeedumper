# zeedumper

`zeedumper` connects to a Kubernetes cluster using your kubeconfig and dumps
component **z-pages** — `flagz`, `statusz`, and (for the kubelet) `configz` —
retrieving them through the **API server proxy**. The only credentials in play
are those in your kubeconfig; the tool is read-only and never mutates the
cluster.

It presents the results as structured text, JSON, or a self-contained HTML page.

## Supported components

| Component                 | Pages                      | How it's reached                                    |
| ------------------------- | -------------------------- | --------------------------------------------------- |
| `kube-apiserver`          | `flagz`, `statusz`         | API server's own endpoints (`/flagz`)               |
| `kube-controller-manager` | `flagz`, `statusz`         | node agent → `https://127.0.0.1:10257`              |
| `kube-scheduler`          | `flagz`, `statusz`         | node agent → `https://127.0.0.1:10259`              |
| `kube-proxy`              | `flagz`, `statusz`         | node agent → `http://127.0.0.1:10249`               |
| `kubelet`                 | `flagz`, `statusz`, `configz` | node proxy, `/api/v1/nodes/<node>:10250/proxy/...` |

`kube-apiserver` and `kubelet` are reachable through the API server proxy, so
those are pure read-only requests with your kubeconfig credentials.

> **Note:** `flagz` and `statusz` are recent, feature-gated endpoints
> (KEP-4827). On clusters where they are disabled, or where RBAC blocks access,
> the affected page is reported as a per-page error and the rest of the dump
> still succeeds.

## The node-agent strategy

`kube-controller-manager`, `kube-scheduler`, and `kube-proxy` bind their serving
ports to `127.0.0.1`, so they are **not reachable through the API server
proxy**. To dump them, zeedumper temporarily:

1. creates a `ServiceAccount` (in `--namespace`, default `kube-system`),
2. binds it to the built-in `system:monitoring` ClusterRole — which grants
   `GET` on the `/flagz` and `/statusz` non-resource URLs,
3. schedules a short-lived **host-network** pod (tolerating all taints) on each
   eligible node, which `curl`s the loopback endpoints using the
   ServiceAccount's projected token,
4. collects the output from the pod logs, and
5. **deletes the pod, ClusterRoleBinding, and ServiceAccount** — including if
   the run errors or is interrupted.

Before creating any RBAC, zeedumper runs a **pre-pull check**: a throwaway pod
(no RBAC, no host network) that does nothing but pull the agent image on a
target node. If the image cannot be pulled — wrong name, unreachable registry,
air-gapped node — the check fails in seconds with the real reason, the
node-agent components are reported with that error, and no ServiceAccount or
ClusterRoleBinding is created.

This is the one part of the tool that mutates the cluster. Disable it with
`--no-node-pods` (those three components then fall back to the API proxy, which
typically reports a connection error). The agent image is configurable with
`--node-pod-image` (default `curlimages/curl:latest`); the node must be able to
pull it.

Beyond the proxy permissions, the node-agent strategy additionally requires
`create`/`delete` on `serviceaccounts`, `pods`, and
`clusterrolebindings`, plus the ability to bind the `system:monitoring` role.

## Install

```sh
go install github.com/raesene/zeedumper/cmd/zeedumper@latest
```

Or build from source:

```sh
go build -o zeedumper ./cmd/zeedumper
```

## Usage

```sh
# Dump every component as text (default)
zeedumper

# Only the API server and scheduler, as JSON
zeedumper --components kube-apiserver,kube-scheduler -o json

# Only configz pages, written to a browsable HTML file
zeedumper --pages configz -o html -f dump.html
```

### Flags

| Flag             | Default        | Description                                            |
| ---------------- | -------------- | ------------------------------------------------------ |
| `--kubeconfig`   | auto           | kubeconfig path (else `$KUBECONFIG`, then `~/.kube/config`) |
| `--components`   | all            | comma-separated components to dump                     |
| `--pages`        | all applicable | comma-separated pages (`flagz,statusz,configz`)        |
| `-o, --output`   | `text`         | output format: `text`, `json`, or `html`               |
| `-f, --output-file` | stdout      | write output to a file                                 |
| `--namespace`    | `kube-system`  | namespace for control-plane pods and node-agent resources |
| `--timeout`      | `15s`          | per-page request timeout                               |
| `--no-node-pods` | `false`        | disable the node-agent strategy (see below)            |
| `--node-pod-image` | `curlimages/curl:latest` | image for temporary node-agent pods         |

Every flag can also be supplied via an environment variable prefixed with
`ZEEDUMPER_`, e.g. `ZEEDUMPER_OUTPUT=json`.

## Requirements

Your kubeconfig identity needs:

- `get`/`list` on `nodes`, and `nodes/proxy` (for the kubelet's z-pages),
- `list` on `pods` in `--namespace`,
- and, unless `--no-node-pods` is set, `create`/`delete` on `serviceaccounts`
  and `pods` in `--namespace` plus `create`/`delete` on `clusterrolebindings`,
  with permission to bind the `system:monitoring` ClusterRole.

## Testing

```sh
go test ./...
```

Integration testing uses [kind](https://kind.sigs.k8s.io/):

```sh
kind create cluster --name zeedumper-test
go run ./cmd/zeedumper
kind delete cluster --name zeedumper-test
```
