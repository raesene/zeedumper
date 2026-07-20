# zeedumper

`zeedumper` connects to a Kubernetes cluster using your kubeconfig and dumps
component **z-pages** — `flagz`, `statusz`, and `configz` —
retrieving them through the **API server proxy**. The only credentials in play
are those in your kubeconfig; the tool is read-only and never mutates the
cluster.

It presents the results as structured text, JSON, or a self-contained HTML page.

## Supported components

| Component                 | Pages                         | How it's reached                                    |
| ------------------------- | ----------------------------- | --------------------------------------------------- |
| `kube-apiserver`          | `flagz`, `statusz`            | API server's own endpoints (`/flagz`)               |
| `kube-controller-manager` | `flagz`, `statusz`, `configz` | node agent → `https://127.0.0.1:10257`              |
| `kube-scheduler`          | `flagz`, `statusz`, `configz` | node agent → `https://127.0.0.1:10259`              |
| `kube-proxy`              | `flagz`, `statusz`, `configz` | node agent → `http://127.0.0.1:10249`               |
| `kubelet`                 | `flagz`, `statusz`, `configz` | node proxy `/api/v1/nodes/<node>:10250/proxy/...`; `statusz` via node agent |

`kube-apiserver` and the kubelet's `flagz`/`configz` are reached through the API
server proxy, so those are pure read-only requests with your kubeconfig
credentials.

The kubelet's `statusz` is the exception. When the API server proxies to the
kubelet, the kubelet runs its own authorization for the connecting identity
(`kube-apiserver-kubelet-client`), and it authorizes `/statusz` as the resource
`nodes/statusz`. The built-in `system:kubelet-api-admin` ClusterRole — which
kubeadm binds to that identity — grants `nodes/configz`/`nodes/proxy` (so
`configz`/`flagz` work) but **not `nodes/statusz`**, so a proxied `statusz`
request comes back `Forbidden`. zeedumper therefore fetches the kubelet's
`statusz` through the **node-agent strategy** instead (see below), where the
agent pod connects to the kubelet directly on loopback as its own ServiceAccount.
With `--no-node-pods`, the kubelet's `statusz` falls back to the proxy and is
reported as a per-page `Forbidden` error; `flagz` and `configz` still succeed.

## Effective configuration (configz defaults filling)

The `configz` endpoint returns the running configuration as JSON, but Go's
`omitempty` serialization silently drops fields whose effective value is a Go
zero value (`false`, `0`, `""`, `nil`). For the kubelet, this hides 46 of 125
fields — including security-relevant settings like `readOnlyPort: 0` (disabled),
`protectKernelDefaults: false`, and `serverTLSBootstrap: false`.

zeedumper detects the cluster's Kubernetes version and automatically fills in
these missing fields with their known zero-value defaults, so the output
shows the **complete effective configuration**. In HTML output, filled-in fields
are visually annotated (italic with a "(default)" marker) so you can distinguish
endpoint-reported values from inferred defaults.

This feature currently supports **kubelet** and **kube-proxy** on Kubernetes
**v1.36**. For unsupported versions or components, configz is displayed as-is
without filling.

> **Note:** `flagz` and `statusz` are recent, feature-gated endpoints
> (KEP-4827). On clusters where they are disabled, or where RBAC blocks access,
> the affected page is reported as a per-page error and the rest of the dump
> still succeeds.

## The node-agent strategy

`kube-controller-manager`, `kube-scheduler`, and `kube-proxy` bind their serving
ports to `127.0.0.1`, so they are **not reachable through the API server
proxy**. The kubelet's `statusz` is reachable through the proxy but blocked by
the kubelet's own authorization (see *Supported components* above). To dump
these, zeedumper temporarily:

1. creates a `ServiceAccount` (in `--namespace`, default `kube-system`),
2. grants it the permissions each target needs:
   - the built-in `system:monitoring` ClusterRole — which grants `GET` on the
     `/flagz` and `/statusz` non-resource URLs the loopback components serve,
   - **only when the kubelet is dumped**, a dedicated ClusterRole granting `get`
     on the `nodes/statusz` resource (the kubelet authorizes `/statusz` as a
     resource, not a non-resource URL, so `system:monitoring` does not cover it
     and no built-in role grants it), and
   - **only when `kube-scheduler`/`kube-controller-manager` `configz` is
     dumped**, a dedicated ClusterRole granting `get` on the `/configz`
     non-resource URL (these components delegate authorization for `/configz`,
     and `system:monitoring` deliberately omits it; `kube-proxy` serves
     `configz` unauthenticated so it needs no grant),
3. schedules a short-lived **host-network** pod (tolerating all taints) on each
   eligible node, which `curl`s the loopback endpoints — and the kubelet's
   `https://127.0.0.1:10250/statusz` — using the ServiceAccount's projected
   token,
4. collects the output from the pod logs, and
5. **deletes the pod(s), ClusterRoleBinding(s), the kubelet-statusz and
   `/configz` ClusterRoles, and the ServiceAccount** — including if the run
   errors or is interrupted.

Only the resources actually needed for the requested components are created: a
kubelet-only dump creates the `nodes/statusz` ClusterRole and binding but not
the `system:monitoring` binding or the `/configz` ClusterRole, and vice versa.

The kubelet runs on **every** node, so a kubelet dump schedules an agent pod on
every node (like `kube-proxy`), not just the control plane. Its `flagz` and
`configz` still come from the API proxy; only `statusz` is fetched by the pod.

Before creating any RBAC, zeedumper runs a **pre-pull check**: a throwaway pod
(no RBAC, no host network) that does nothing but pull the agent image on a
target node. If the image cannot be pulled — wrong name, unreachable registry,
air-gapped node — the check fails in seconds with the real reason, the
node-agent components are reported with that error, and no ServiceAccount or
ClusterRoleBinding is created.

This is the one part of the tool that mutates the cluster. Disable it with
`--no-node-pods` (the loopback components then fall back to the API proxy, which
typically reports a connection error, and the kubelet's `statusz` falls back to
the proxy's `Forbidden` error). The agent image is configurable with
`--node-pod-image` (default `curlimages/curl:latest`); the node must be able to
pull it.

Beyond the proxy permissions, the node-agent strategy additionally requires
`create`/`delete` on `serviceaccounts`, `pods`, `clusterroles`, and
`clusterrolebindings`, plus the ability to bind the `system:monitoring` role.
Creating the kubelet-statusz and `/configz` ClusterRoles is also subject to
RBAC's escalation-prevention rule, so your identity must itself hold `get` on
`nodes/statusz` and on the `/configz` non-resource URL (or the `escalate` verb
on `clusterroles`).

## Install

### go install

Requires Go 1.26 or newer.

```sh
# Latest tagged release
go install github.com/raesene/zeedumper/cmd/zeedumper@latest

# A specific version
go install github.com/raesene/zeedumper/cmd/zeedumper@v0.1.0
```

This installs the `zeedumper` binary into `$(go env GOPATH)/bin` (or `$GOBIN`
if set). Make sure that directory is on your `PATH`:

```sh
export PATH="$(go env GOPATH)/bin:$PATH"
```

> Builds installed this way report `version` as `dev` — the release version is
> only stamped into the prebuilt binaries below (via GoReleaser ldflags).

### Prebuilt binaries

Download a prebuilt archive for your platform from the
[releases page](https://github.com/raesene/zeedumper/releases) (linux, macOS,
and Windows on amd64/arm64), then verify and extract:

```sh
sha256sum -c checksums.txt        # optional integrity check
tar xzf zeedumper_*_linux_amd64.tar.gz
./zeedumper version
```

### Build from source

```sh
git clone https://github.com/raesene/zeedumper
cd zeedumper
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
  and `pods` in `--namespace` plus `create`/`delete` on `clusterroles` and
  `clusterrolebindings`, with permission to bind the `system:monitoring`
  ClusterRole and (for the kubelet's `statusz`) to grant `nodes/statusz` and
  (for scheduler/controller-manager `configz`) to grant the `/configz`
  non-resource URL — i.e. your identity holds those permissions or the
  `escalate` verb.

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
