import { ArrowLeft, Pause, Pencil, Play, RefreshCw, Trash2 } from "lucide-preact";
import { useMemo, useState } from "preact/hooks";
import { api } from "../api";
import { useData } from "../hooks";
import {
  ago,
  certTone,
  daysLeft,
  ms,
  pct,
  since,
  span,
  STATUS_LABEL,
  statusTone,
  targetText,
  TYPE_LABEL,
  uptimeTone,
} from "../lib";
import { navigate } from "../router";
import type { CheckDetail, Point } from "../types";
import { LineChart, UptimeBars } from "./charts";
import { CheckForm } from "./CheckForm";
import { PingHelp } from "./HeartbeatsPage";
import { Dot, ErrorNote, Figure, SectionHead, useAction } from "./ui";

const RANGES = ["24h", "7d", "30d", "90d"] as const;

export function CheckPage(props: { id: number }) {
  const { data, error, reload } = useData(() => api.check(props.id), 15_000, [props.id]);
  const [editing, setEditing] = useState(false);
  const action = useAction();

  if (!data) return error ? <ErrorNote>{error}</ErrorNote> : <div class="skeleton page-skeleton" />;
  const { check: c, state, status } = data.view;
  const cert = daysLeft(state.cert_expires);
  const push = c.type === "push";

  const runNow = () => action.run(() => api.runCheck(c.id)).then(reload);
  const togglePause = () => action.run(() => api.pauseCheck(c.id, !c.paused)).then(reload);
  const remove = async () => {
    const extra = c.source === "npm" ? " It won't be re-added from the proxy." : "";
    if (!confirm(`Delete ${c.name} and its history?${extra}`)) return;
    try {
      await api.deleteCheck(c.id);
      navigate(push ? "/heartbeats" : "/");
    } catch (e) {
      action.setError((e as Error).message);
    }
  };

  return (
    <div class="page">
      <header class="page-head">
        <a class="eyebrow back" href={push ? "/heartbeats" : "/"}>
          <ArrowLeft size={13} /> {push ? "Heartbeats" : "Checks"}
          {c.group && <span class="muted">· {c.group}</span>}
        </a>
        <h1 class="page-title page-title-small">
          <Dot tone={statusTone(status)} />
          {c.name}
        </h1>
        <div class="check-meta">
          <span class="chip">{TYPE_LABEL[c.type]}</span>
          <span class="mono muted">{targetText(c)}</span>
          {(c.tags ?? []).map((t) => (
            <span key={t} class="chip">
              {t}
            </span>
          ))}
          {c.source && <span class="chip">from {c.source === "npm" ? "proxy" : c.source}</span>}
          {c.mute && <span class="chip chip-warn">muted</span>}
        </div>
        <div class="figures">
          <Figure
            value={state.flapping ? "Flapping" : STATUS_LABEL[status]}
            label={state.since ? `for ${span(since(state.since))}` : "status"}
            tone={statusTone(status) === "good" ? "" : statusTone(status)}
          />
          {push ? (
            <Figure value={state.last_ping ? ago(state.last_ping) : "Never"} label="Last ping" />
          ) : (
            <Figure value={ms(state.latency)} label={`Latency · ${ago(state.last)}`} />
          )}
          {["24 h", "7 d", "30 d", "1 y"].map((label, i) => (
            <Figure
              key={label}
              value={pct(data.uptime[i])}
              label={`Uptime ${label}`}
              tone={uptimeTone(data.uptime[i]) === "good" ? "" : uptimeTone(data.uptime[i])}
            />
          ))}
          {cert !== null && (
            <Figure
              value={cert}
              unit="d"
              label="Certificate left"
              tone={certTone(cert, c.cert_days)}
              title={`Expires ${new Date(state.cert_expires!).toLocaleString()}`}
            />
          )}
        </div>
        {state.message && status !== "up" && (
          <div class={`note ${status === "down" ? "note-bad" : "note-warn"}`}>{state.message}</div>
        )}
        <div class="toolbar">
          {!push && (
            <button class="btn" onClick={runNow} disabled={action.busy || c.paused}>
              <RefreshCw size={15} class={action.busy ? "spin" : ""} /> Check now
            </button>
          )}
          <button class="btn" onClick={togglePause} disabled={action.busy}>
            {c.paused ? <Play size={15} /> : <Pause size={15} />}
            {c.paused ? "Resume" : "Pause"}
          </button>
          <button class="btn" onClick={() => setEditing(true)}>
            <Pencil size={15} /> Edit
          </button>
          <span class="spacer" />
          <button class="btn btn-ghost btn-danger" onClick={remove} disabled={action.busy}>
            <Trash2 size={15} /> Delete
          </button>
          {action.error && <span class="form-error">{action.error}</span>}
        </div>
      </header>

      {push && (
        <section class="section">
          <SectionHead title="Ping URL" />
          <PingHelp check={c} />
        </section>
      )}

      <History id={c.id} push={push} />

      <section class="section">
        <SectionHead index={2} title="Incidents">
          <span class="eyebrow">{data.incidents.length}</span>
        </SectionHead>
        {data.incidents.length === 0 ? (
          <div class="empty">No incidents recorded.</div>
        ) : (
          <div class="list">
            {data.incidents.map((i) => (
              <div key={i.id} class="list-row">
                <Dot tone={i.ended ? "" : "bad"} />
                <div class="list-main">
                  <span class="list-title">
                    {new Date(i.started).toLocaleString()}
                    <span class="muted">
                      {" "}
                      · {i.ended ? `down ${span(since(i.started) - since(i.ended))}` : "ongoing"}
                    </span>
                  </span>
                  <span class="list-sub">{i.cause}</span>
                </div>
              </div>
            ))}
          </div>
        )}
      </section>

      <Recent data={data} index={3} />

      {editing && (
        <CheckForm
          check={c}
          onClose={() => setEditing(false)}
          onSaved={() => {
            setEditing(false);
            reload();
          }}
        />
      )}
    </div>
  );
}

function History(props: { id: number; push: boolean }) {
  const [range, setRange] = useState<(typeof RANGES)[number]>("24h");
  const { data, error } = useData(() => api.history(props.id, range), 60_000, [props.id, range]);
  const days = useData(() => api.history(props.id, "30d"), 300_000, [props.id]);
  const points = data?.points ?? [];
  const times = useMemo(() => points.map((p) => new Date(p.t).getTime()), [points]);
  const series = useMemo(
    () => [
      {
        label: props.push ? "Run time" : "Latency",
        color: "var(--accent)",
        values: points.map((p) => (p.latency > 0 ? p.latency : null)),
      },
    ],
    [points],
  );
  const strip = useMemo(() => points.map((p) => 1 - p.uptime / 100), [points]);
  return (
    <section class="section">
      <SectionHead index={1} title={props.push ? "Pings" : "Latency"}>
        <div class="seg" role="tablist">
          {RANGES.map((r) => (
            <button
              key={r}
              role="tab"
              aria-selected={range === r}
              class={range === r ? "active" : ""}
              onClick={() => setRange(r)}
            >
              {r}
            </button>
          ))}
        </div>
      </SectionHead>
      {error && <ErrorNote>{error}</ErrorNote>}
      {!data ? (
        <div class="skeleton chart-skeleton" />
      ) : (
        <LineChart
          times={times}
          series={series}
          unit="ms"
          format={ms}
          strip={{ values: strip, label: range === "24h" ? "Failed" : "Some checks failed" }}
          note={(i) =>
            points[i].message ??
            (range !== "24h" && points[i].uptime < 100
              ? `${points[i].uptime.toFixed(1)}% up`
              : undefined)
          }
          empty="No results in this range yet"
        />
      )}
      {days.data && <DailyBars points={days.data.points} />}
    </section>
  );
}

/** 30 days of uptime, one cell per day. */
function DailyBars(props: { points: Point[] }) {
  const cells = useMemo(() => {
    const today = new Date();
    today.setHours(0, 0, 0, 0);
    const out: { label: string; value: number }[] = [];
    for (let d = 29; d >= 0; d--) {
      const start = today.getTime() - d * 86_400_000;
      const hours = props.points.filter((p) => {
        const t = new Date(p.t).getTime();
        return t >= start && t < start + 86_400_000;
      });
      const value = hours.length ? hours.reduce((s, p) => s + p.uptime, 0) / hours.length : -1;
      out.push({
        label: new Date(start).toLocaleDateString([], {
          weekday: "short",
          day: "numeric",
          month: "short",
        }),
        value,
      });
    }
    return out;
  }, [props.points]);
  return (
    <div class="daily">
      <div class="eyebrow">Last 30 days</div>
      <UptimeBars cells={cells} />
    </div>
  );
}

function Recent(props: { data: CheckDetail; index: number }) {
  const recent = props.data.recent;
  return (
    <section class="section">
      <SectionHead index={props.index} title="Latest results" />
      {recent.length === 0 ? (
        <div class="empty">No results yet.</div>
      ) : (
        <div class="list results">
          {recent.map((r) => (
            <div key={r.time} class="list-row">
              <Dot tone={r.ok ? "good" : "bad"} />
              <span class="mono muted result-time">
                {new Date(r.time).toLocaleTimeString([], {
                  hour: "2-digit",
                  minute: "2-digit",
                  second: "2-digit",
                })}
              </span>
              <span class="mono result-latency">{ms(r.latency)}</span>
              <span class="result-msg">{r.message}</span>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
