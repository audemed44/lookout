# Lookout

Instructions for coding agents working in this repository. `CLAUDE.md`
imports this file.

## Project

Watches a homelab: checks (HTTP, TCP, ping, DNS, Docker, TLS), push
heartbeats, scheduled speedtests, and an Apprise-compatible notifier
(`POST /notify/<key>`). Replaces Uptime Kuma, Speedtest Tracker and
apprise-api. A Go server (`cmd/lookout`, `internal/`) serves a JSON API and
the Preact + TypeScript frontend (`frontend/`), built into `web/dist` and
embedded in the binary. Everything lives in SQLite at `/data/lookout.db`
(`internal/store`).

- `internal/checks`: probes, the scheduler (one goroutine per check),
  the up/down state machine (retries, incidents, flapping, certificate
  warnings), heartbeats and maintenance.
- `internal/notify`: targets as Apprise URLs (`tgram://`, `ntfy://`,
  `discord://`, `json://`), routes, dedupe, quiet hours, digests.
- `internal/speedtest`: cron-scheduled speedtests via speedtest-go.
- `internal/discovery`: checks from the proxy's hosts (Gatehouse's
  discovery API, or Nginx Proxy Manager), and which containers Gatehouse
  has put to sleep. HTTP checks send `X-Gatehouse-Probe` so they never
  wake an app; one that answers asleep is shown asleep, not down, and
  nothing goes in its history.
- `internal/importer`: Uptime Kuma and Speedtest Tracker databases.
- `internal/config`: YAML export/import.

## Constraints

- **Low memory is a feature.** It idles around 13 MB. History is in
  SQLite, never in memory: raw results for `retention.raw_hours`, rolled up
  per hour as they're written (`rollups`), and uptime/longer charts read
  the rollups. Check transports don't keep connections alive.
- Direct dependencies: yaml.v3, modernc.org/sqlite (pure Go, so the build
  stays static and cgo-free) and speedtest-go (no Ookla CLI). Justify any
  new one.
- Every `/api/` call needs the token (bearer or the derived session
  cookie), and state-changing browser requests from another origin are
  refused (`sameOrigin`). `/notify/<key>` and `/ping/<token>` are the only
  unauthenticated writes: the key or token is the credential. The status
  page (`/api/public/status`) is off by default and shows names and status
  only.
- Notification target URLs hold tokens. They're never returned by the API
  unless they're `${VAR}` references, and are only exported in that form.
  Errors from senders must not echo the URL.
- `/notify` must stay compatible with what Foyer and Hoist send to
  apprise-api: JSON or form `title`, `body`, `type`, `tag`; 204 when
  nothing is routed, 424 when delivery failed.
- Ping checks use unprivileged ICMP sockets; no CAP_NET_RAW.
- UI style is Foyer's: Swiss editorial, always dark, heavy Inter headlines,
  tracked uppercase eyebrows, 2px rules over numbered headings, square
  corners, one accent (#2563ff). Chart series colours are validated for
  colour-blind contrast on the black surface (`SERIES_COLORS`). Check
  phone width too.

## Commits

Conventional Commits: `<type>(<scope>): <summary>`, e.g. `feat(checks): ...`.

## Checks before pushing

```sh
go vet ./... && go test -race ./...        # needs web/dist (npm run build)
cd frontend && npm run format:check && npm run typecheck && npm test && npm run build
docker build -t lookout:dev .
```
