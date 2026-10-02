# Lookout

Watch your homelab from one small binary: uptime checks, heartbeats from
cron jobs and backups, scheduled speedtests, and a notifier other apps can
post to. A Go binary that idles at about 13 MB of RAM, replacing
[Uptime Kuma](https://github.com/louislam/uptime-kuma),
[Speedtest Tracker](https://github.com/alexjustesen/speedtest-tracker) and
[apprise-api](https://github.com/caronc/apprise-api).

- **Checks**: HTTP(S) (status codes, a keyword, or a value at a JSON path),
  TCP port, ping (ICMP, no root needed), DNS (record type, resolver,
  expected answer), Docker container state and healthcheck, and TLS
  certificates on any port. Per-check interval, timeout and retries before
  "down"; notifications on down and recovery, with incidents recorded.
- **Certificates**: every HTTPS check reads the certificate actually
  served and warns before it expires (14, 7, 3 and 1 days by default), and
  says when it was renewed. An independent check on your proxy's renewals.
- **Heartbeats**: jobs ping `/ping/<token>` (healthchecks.io style:
  `/start`, `/fail`, `/<exit code>`, a POST body kept as the message). A
  heartbeat goes down when a ping is late, the job reports a failure, or it
  started and didn't finish.
- **Proxy discovery**: a check for every domain Nginx Proxy Manager serves,
  kept in sync. Deleting one means "don't watch this domain".
- **Speedtests**: on a cron schedule against Ookla's servers (no CLI),
  with charts, history and alerts below your thresholds.
- **Notifications**: an Apprise-compatible `POST /notify/<key>`, so apps
  that used apprise-api only change the URL. Targets are Apprise URLs
  (Telegram, ntfy, Discord, JSON webhook), ideally read from the
  environment. Routes by tag and severity, a daily digest for the
  unimportant, quiet hours, duplicate suppression, flap suppression, and a
  history of what was sent where.
- **Maintenance**: one-off, daily or weekly windows for some checks or
  tags; pause any check.
- **Status page**: an optional read-only `/status` with 30 days of uptime.
- **Foyer**: a card in the
  [Foyer widget format](https://github.com/audemed44/foyer/blob/main/docs/app-widgets.md)
  with checks up, failing checks, the nearest certificate expiry and the
  latest speedtest, plus a button to run one.
- **Imports**: monitors (and their hourly uptime) from Uptime Kuma's
  `kuma.db`, results from Speedtest Tracker's `database.sqlite`.
- **Configuration as YAML**: export and import everything, or drop a
  `lookout.yaml` in the data folder to have it merged in at start.

History is kept in SQLite: every result for two days, then hourly
summaries for 400 days, which is what uptime and the longer charts use.

## Install

See [docker-compose.example.yml](docker-compose.example.yml). Set
`LOOKOUT_TOKEN` and open the UI.

| Variable | |
|---|---|
| `LOOKOUT_TOKEN` | Required. You sign in with it; Foyer sends it as a bearer token. |
| `TZ` | For quiet hours, digests and the speedtest schedule. |
| `LOOKOUT_NPM_URL`, `LOOKOUT_NPM_EMAIL`, `LOOKOUT_NPM_PASSWORD` | Nginx Proxy Manager's admin API (e.g. `http://npm:81`), for discovery. |
| `LOOKOUT_CONFIG` | A YAML file merged in at start (default `/data/lookout.yaml`). |
| `LOOKOUT_DOCKER_SOCKET` | Default `/var/run/docker.sock`; container checks need it. |
| `LOOKOUT_PORT`, `LOOKOUT_DATA_DIR` | Default `8080`, `/data`. |

Ping checks use unprivileged ICMP sockets, which Docker allows by default.
Outside Docker, allow them with `sysctl net.ipv4.ping_group_range="0 2147483647"`.

## Notifications

1. **Targets**: add one per destination. Put the secret part in the
   stack's `.env` (`TELEGRAM_URL=tgram://<bot token>/<chat id>`) and give
   the target the URL `${TELEGRAM_URL}`, so the token stays out of the
   database and the exported YAML. URLs typed in directly work too, and
   are never shown again.

   | Service | URL |
   |---|---|
   | Telegram | `tgram://<bot token>/<chat id>[/<chat id>…]`, `<chat id>:<topic>` for a forum topic |
   | ntfy | `ntfy://<topic>` (ntfy.sh), `ntfys://[user:pass@]host/<topic>[?token=…]` |
   | Discord | `discord://<webhook id>/<webhook token>` |
   | Webhook | `json://host/path`, `jsons://…` (Apprise's JSON body) |

2. **Routes** (optional): without any, everything goes to every target.
   With routes, a notification goes to the targets of each route it
   matches, by tag and minimum type (info < success < warning < failure),
   until one marked *stop*. A *digest* route holds what it matches until
   the digest time and sends it as one message.
3. **Senders**: each app posts to its own `/notify/<key>`. To switch an app
   from apprise-api, add a sender with the same key as before, or a new
   one, and change the host in the app's URL:

   ```yaml
   # Foyer
   alerts:
     apprise_url: http://lookout:8080/notify/<key>
   # Hoist
   updates:
     notify: http://lookout:8080/notify/<key>
   ```

   The body is Apprise's: `{"title", "body", "type", "tag"}` as JSON or a
   form. Lookout answers 204 when no route takes it (as Apprise does for an
   empty key) and 424 when delivery failed.

Lookout's own notifications are tagged `check`, `heartbeat` or
`speedtest`, plus the check's tags, so routes can tell them apart.

## Heartbeats

```sh
# after the job
your-job && curl -fsS -m 10 --retry 3 https://lookout.example.com/ping/<token>
# start and exit code, which also catches a job that hangs
curl -fsS -m 10 https://lookout.example.com/ping/<token>/start
your-job; curl -fsS -m 10 https://lookout.example.com/ping/<token>/$?
```

Monitors imported from Uptime Kuma keep their push tokens, so jobs only
need the host changed.

## Foyer

```yaml
      - name: Lookout
        url: https://lookout.example.com
        widget:
          type: app
          url: http://lookout:8080/api/foyer/widget
          key: ${LOOKOUT_TOKEN}
```

## Moving from Uptime Kuma, Speedtest Tracker and apprise-api

1. Start Lookout, add your Telegram target and a sender per app.
2. **Settings → Import**: upload `kuma.db` (stop Kuma first, or copy it with
   `sqlite3 kuma.db ".backup kuma-copy.db"`) and Speedtest Tracker's
   `database.sqlite`. Imports can be repeated; what's already here is
   skipped. Monitors that need things Lookout doesn't do (request
   headers or bodies, basic auth, MQTT, databases…) are listed as skipped.
3. Point Foyer and Hoist at `/notify/<key>`, replace Foyer's Uptime Kuma
   and Speedtest widgets with the Lookout card, and move push URLs to the
   new host.
4. Remove the three old containers.

## Development

```sh
cd frontend && npm install && npm run build && cd ..
LOOKOUT_TOKEN=dev LOOKOUT_DATA_DIR=./data go run ./cmd/lookout
# or, with live reload of the UI on :5173:
cd frontend && npm run dev
```

Checks before pushing are in [AGENTS.md](AGENTS.md).
