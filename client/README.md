# localops/client

The LocalOps Go SDK — a standalone module you drop into any local Go
service to report tasks and watcher check-ins to a running
[LocalOps](../server) instance. See the root [README](../README.md) for
the full system design; this covers just the SDK.

Zero risk to production: when unconfigured (the default), every call is a
no-op. Nothing here ever returns an error to your business logic, panics,
or blocks meaningfully.

## Install

This package lives inside the single `github.com/Mahaveer86619/LocalOps`
module (no separate go.mod/go.sum of its own), so a consumer just needs
that module in its build graph. Importing only `.../client` pulls in
nothing but the Go standard library - none of the server's dependencies
(Echo, SQLite, gopsutil) come along for the ride, single module or not.

While developing against a local checkout (LocalOps as a sibling
directory of the consuming project):

```
go mod edit -replace github.com/Mahaveer86619/LocalOps=../LocalOps
go get github.com/Mahaveer86619/LocalOps
```

Then import the subpackage normally:

```go
import "github.com/Mahaveer86619/LocalOps/client"
```

Once this repo is pushed to GitHub, drop the `replace` and
`go get github.com/Mahaveer86619/LocalOps@latest` instead - same import
path either way.

## Configure

Reads `PROFILE` / `LOCALOPS_ENABLED`, `LOCALOPS_URL`, `LOCALOPS_SERVER`
from the environment, optionally seeded from a `.env` file first (see
[`.env.example`](.env.example)):

```go
func main() {
    client.LoadEnvFile("")           // loads ./.env if present; no-op otherwise
    client.Init(client.ConfigFromEnv())
    ...
}
```

Real environment variables always win over the `.env` file. In production,
just don't set any of them — `client.Init` builds a no-op client and
nothing else changes.

## Use

```go
task := client.Start(ctx, client.Task{
    Type:        "sync",
    Description: "v2sync retry run",
    Details:     map[string]any{"sync_stage": "v2"},
})
defer task.Complete()

task.Update(20, "Fetching pending records")
task.Heartbeat()

select {
case <-task.Context().Done():
    return task.Context().Err() // cancelled locally, or remotely via LocalOps
default:
}

if err != nil {
    task.Fail(err)
    return
}
```

`task.Context()` is derived from the ctx passed to `Start`; LocalOps
polling a `cancel`/`stop` control action cancels it the same way the
parent context cancelling would. When the client is disabled, no polling
goroutine is ever started.

Watchers (standing conditions, not tasks — see root README §13) from Go
code:

```go
client.CheckIn(ctx, "flipkart-vpn-ping", "down", "ping timeout", nil)
```

A non-Go checker doesn't need this package at all — see
[`scripts/ping-watch.sh`](../scripts/ping-watch.sh) and
[`scripts/notify.sh`](../scripts/notify.sh) for the plain-bash equivalent.
