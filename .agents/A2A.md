# A2A.md
Agent-to-Agent protocol exposure. Reference for `internal/server/` and
`internal/server/a2a/`.

## How baifo exposes A2A

The HTTP daemon (`baifo server`) mounts the A2A protocol under a single
prefix:

```
/api/a2a/<agent-id>
```

Today only the root is exposed (`agent-id = "root"`), but the
implementation is multi-agent capable: the daemon iterates
`Core.Agents()` and mounts one handler per entry. Adding another agent
(static or dynamic) is a matter of expanding what `Core.Agents()`
returns.

Endpoints:

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/a2a/<agent-id>` | JSON-RPC entry point. SSE streaming supported. |
| `GET` | `/api/a2a/.well-known/agent-card.json` | All agent cards as a JSON array. Accepts `?agent=<id>` to return a single card. |
| `GET` | `/api/a2a/<agent-id>/.well-known/agent-card.json` | Per-agent card. |
| `GET` | `/healthz` | Daemon health probe (not part of A2A). |

The exact path prefix is `a2a.PathPrefix = "/api/a2a/"` in
`internal/server/a2a/handler.go`.

## Daemon entry point

The server starts in two ways, both gated on `a2a.enabled: true` in
`baifo.yaml`:

```
baifo server [--config-dir <path>]   # headless daemon, A2A only
baifo                                 # TUI; also hosts A2A in-process
```

`baifo server` refuses to boot when `a2a.enabled` is false (it has
nothing to serve). The interactive TUI (`baifo`) additionally spins up
the same server in a background goroutine when `a2a.enabled` is true,
so a single `baifo` process both chats and serves; it shuts the server
down on exit. When `a2a.enabled` is false the TUI runs with no server,
as before. There is no in-TUI toggle: enable/disable lives in
`baifo.yaml` (editable via `/settings edit`) and takes effect on
the next start.

Implementation in `cmd/baifo/server_cmd.go` (headless) and
`cmd/baifo/tui.go` (in-process). Construction goes through
`server.New(core, cfg)` where `core` satisfies the `server.Core`
interface (implicitly implemented by `*app.App`):

```go
type Core interface {
    Agents() []a2a.AgentEntry
    SessionService() session.Service
    MemoryService() memory.Service
    RootName() string
    RootBuildError() error
}
```

**Decoupling note:** `internal/server` does not import `internal/app`.
The dependency flows `app → server.Core` (implicit). This enables a
future split where the daemon and the core live in different
processes.

`Server.Rebuild()` is called once, inside `srv.Run()`, before the
listen loop starts. Re-mounting handlers on `App.ReloadFromDisk` is
not yet wired: a config reload does not update live A2A routes until
the daemon is restarted.

## Why root only (today)

The Worker Manager keeps workers alive only while baifo is running
interactively. There is no "warm pool" of pre-spawned statics ready to
answer A2A calls. Spinning up a worker on every A2A request would
mean re-loading skills/MCPs and re-creating an ADK runner per call,
which is slow and bypasses the supervision model the rest of baifo
relies on.

## Agent card

Generated automatically from the root config. The exact JSON shape is
produced by ADK's `adka2a` server library; an illustrative example:

```json
{
  "name": "baifo",
  "description": "Multidisciplinary local assistant.",
  "url": "http://127.0.0.1:7777/api/a2a/root",
  "version": "1.0.0",
  "protocolVersion": "0.2.5",
  "preferredTransport": "JSONRPC",
  "defaultInputModes": ["text/plain"],
  "defaultOutputModes": ["text/plain"],
  "capabilities": { "streaming": true },
  "skills": [
    { "id": "spawn_dynamic_agent", "name": "spawn_dynamic_agent",
      "description": "..." },
    { "id": "search_memory", "name": "search_memory", "description": "..." }
  ]
}
```

`skills` is built by `adka2a.BuildAgentSkills(rootAgent)`, the same
call upstream uses. When bearer auth is active (`a2a.credentials.token`
set), the card also carries `securitySchemes` (one `bearer` entry of
type `http`, scheme `bearer`) and a matching `security` requirement, so
a client knows to send `Authorization: Bearer` before it gets a 401. The
token value itself is never written into the card. When auth is off both
fields are omitted (the JSON above is the no-auth shape). The discovery
endpoints stay open either way so a client can read the card without a
credential and discover that it needs one. Enforcement happens at the
HTTP layer (see below); `applySecurity` in `internal/server/a2a/handler.go`
adds the annotation, gated on `requireAuth` which `server.New` sets from
`cfg.AuthToken != ""`.

## Transport

Backed by:

- `github.com/a2aproject/a2a-go`, A2A protocol primitives.
- `google.golang.org/adk/server/adka2a`, ADK integration (executor).

The server is a thin handler that:

1. Reads the live `*agent.Instance` for each entry in
   `Core.Agents()`.
2. Creates an `adka2a.Executor` wired to the shared
   `session.Service` and `memory.Service`.
3. Mounts the JSON-RPC handler at `/api/a2a/<agent-id>`.

Sessions opened via A2A share the same SQLite-backed session service as
the in-process Facade, but each A2A call uses a separate session ID.

(Note: `Core` does not currently expose an `ArtifactService`. Earlier
docs mentioned one; that is stale.)

## Defaults

- `a2a.host: 127.0.0.1`
- `a2a.port: 7777`
- `a2a.public_url: ""` → defaults to `http://<host>:<port>`.
- `a2a.enabled: false` → no server. When true, the server is hosted by
  both `baifo server` (headless) and the interactive `baifo` TUI
  (in-process, background goroutine, stopped on exit).
- `a2a.credentials.token: ""` → no authentication. When set, bearer
  auth is required (see below).

## Authentication

Opt-in bearer-token auth, driven entirely by config:

```yaml
a2a:
  credentials:
    token: "${secret:A2A_TOKEN}"   # or a literal string
```

- **Empty token** → the server is unauthenticated (historical
  behaviour; fine on `127.0.0.1`).
- **Non-empty token** → every request under `/api/a2a/` must carry
  `Authorization: Bearer <token>`. A missing credential gets `401`
  with `WWW-Authenticate: Bearer realm="baifo"`. A wrong credential
  gets `401` with `WWW-Authenticate: Bearer realm="baifo", error="invalid_token", error_description="the bearer token is invalid"`
  so clients can distinguish the two cases.
  `/healthz` stays open so liveness probes work without a credential.

The token value supports `${secret:NAME}` expansion: `cmd/baifo` resolves
it via `App.ExpandSecretString` at boot (literal strings pass through),
so the real token never lives in `baifo.yaml` in plaintext. The
comparison is constant-time (`crypto/subtle`). Implementation:
`withBearerAuth` in `internal/server/auth.go`, applied to the A2A
handler in `server.New`.

Not yet implemented: token rotation, multiple tokens (one per agent for
the multi-static model), and a `WARN` when binding to a non-loopback
interface without a token.

## CORS

`Access-Control-Allow-Origin: *` is set on the agent-card endpoints
(`/api/a2a/.well-known/agent-card.json` and
`/api/a2a/<id>/.well-known/agent-card.json`) so browser-based discovery
tools work. The JSON-RPC endpoint (`POST /api/a2a/<id>`) does not set
CORS headers; A2A clients are server-side.

## Streaming

SSE responses on `POST /api/a2a/<agent-id>` mirror the ADK event
stream. `http.Server.WriteTimeout` is set to `0` in
`internal/server/server.go` so the streaming response is not aborted
mid-flight.

## Healthz

`GET /healthz` returns:

```json
{
  "status": "ok",
  "root_name": "baifo",
  "exposed_agents": ["root"]
}
```

`root_build_error` is added to the payload only when the root agent
failed to build; it is absent from a healthy response.

Used by container orchestrators, supervisors, and the TUI's "A2A
status" indicator.

## Discovery from another agent

Example: a Magec instance discovering baifo.

```bash
curl http://localhost:7777/api/a2a/.well-known/agent-card.json
```

Returns a JSON array with one card today. To fetch a single card by ID:

```bash
curl http://localhost:7777/api/a2a/.well-known/agent-card.json?agent=root
```

The remote operator then references the URL in their own A2A client config.

## Self-call protection

baifo's root agent does **not** have an `a2a_call` tool that lets it
loop back into itself. That avoids accidental infinite recursion.
