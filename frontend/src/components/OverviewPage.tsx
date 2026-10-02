import { Plus, Search } from "lucide-preact";
import { useState } from "preact/hooks";
import { api } from "../api";
import { useData } from "../hooks";
import {
  ago,
  certTone,
  daysLeft,
  mbps,
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
import type { CheckRow, Overview } from "../types";
import { Sparkline } from "./charts";
import { CheckForm } from "./CheckForm";
import { Dot, ErrorNote, Figure, SectionHead } from "./ui";

export function OverviewPage() {
  const { data, error, reload } = useData(api.overview, 10_000);
  const [adding, setAdding] = useState(false);
  const [query, setQuery] = useState("");

  if (!data) return error ? <ErrorNote>{error}</ErrorNote> : <div class="skeleton page-skeleton" />;
  const { counts } = data;
  const failing = counts.down + counts.pending;
  const title =
    counts.total === 0
      ? "Nothing yet"
      : counts.down > 0
        ? `${counts.down} down`
        : counts.pending > 0
          ? `${counts.pending} failing`
          : "All up";
  const q = query.trim().toLowerCase();
  const rows = data.checks.filter(
    (r) =>
      r.check.type !== "push" &&
      (!q ||
        r.check.name.toLowerCase().includes(q) ||
        (r.check.target ?? "").toLowerCase().includes(q) ||
        (r.check.tags ?? []).some((t) => t.includes(q))),
  );
  const groups = groupRows(rows);

  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">
          {counts.total} {counts.total === 1 ? "check" : "checks"}
          {data.queued > 0 && <span class="muted">· {data.queued} notifications held</span>}
        </div>
        <h1 class={`page-title ${counts.down ? "tone-bad" : ""}`}>{title}</h1>
        <div class="figures">
          <Figure
            value={counts.up}
            unit={`/${counts.total - counts.paused}`}
            label="Up"
            tone={failing ? "warn" : ""}
          />
          <Figure value={counts.down} label="Down" tone={counts.down ? "bad" : ""} />
          {counts.cert_days >= 0 && (
            <Figure
              value={counts.cert_days}
              unit="d"
              label={`Certificate · ${counts.cert_check}`}
              tone={certTone(counts.cert_days)}
            />
          )}
          <SpeedFigure data={data} />
        </div>
        <Incidents data={data} />
      </header>
      {error && <ErrorNote>{error}</ErrorNote>}
      <div class="toolbar">
        <label class="search">
          <Search size={15} />
          <input
            class="input"
            placeholder="Filter by name, target or tag"
            value={query}
            onInput={(e) => setQuery(e.currentTarget.value)}
          />
        </label>
        <span class="spacer" />
        <button class="btn btn-primary" onClick={() => setAdding(true)}>
          <Plus size={16} /> Add check
        </button>
      </div>
      {rows.length === 0 && (
        <div class="empty">
          {data.checks.length === 0 ? (
            <>
              No checks yet. Add one, sync them from your proxy or import from Uptime Kuma in{" "}
              <a class="link-btn" href="/settings">
                Settings
              </a>
              .
            </>
          ) : (
            "Nothing matches."
          )}
        </div>
      )}
      <div class="check-groups stagger">
        {groups.map(([group, list], gi) => (
          <section key={group} class="check-group">
            <SectionHead index={gi + 1} title={group || "Checks"}>
              <span class="eyebrow">
                {list.filter((r) => r.status === "up").length}/{list.length} up
              </span>
            </SectionHead>
            <div class="check-list">
              <div class="check-row check-header eyebrow" aria-hidden="true">
                <span>Check</span>
                <span>Latency</span>
                <span class="num">24 h</span>
                <span class="num">7 d</span>
                <span class="num">30 d</span>
                <span>Status</span>
              </div>
              {list.map((r) => (
                <CheckLine key={r.check.id} row={r} />
              ))}
            </div>
          </section>
        ))}
      </div>
      {adding && (
        <CheckForm
          onClose={() => setAdding(false)}
          onSaved={() => {
            setAdding(false);
            reload();
          }}
        />
      )}
    </div>
  );
}

function groupRows(rows: CheckRow[]): [string, CheckRow[]][] {
  const map = new Map<string, CheckRow[]>();
  for (const r of rows) {
    const g = r.check.group ?? "";
    if (!map.has(g)) map.set(g, []);
    map.get(g)!.push(r);
  }
  return [...map.entries()].sort(([a], [b]) => (a === "" ? -1 : b === "" ? 1 : a.localeCompare(b)));
}

function CheckLine(props: { row: CheckRow }) {
  const { check: c, state, status } = props.row;
  const cert = daysLeft(state.cert_expires);
  return (
    <a class={`check-row ${status === "paused" ? "is-paused" : ""}`} href={`/checks/${c.id}`}>
      <span class="check-name">
        <Dot tone={statusTone(status)} />
        <span>
          <span class="check-title">{c.name}</span>
          <span class="check-sub" title={targetText(c)}>
            {TYPE_LABEL[c.type]} · {targetText(c)}
          </span>
        </span>
      </span>
      <span class="check-latency">
        <Sparkline values={props.row.spark} />
        <span class="mono">{ms(state.latency)}</span>
      </span>
      {props.row.uptime.map((u, i) => (
        <span key={i} class={`num mono tone-${uptimeTone(u)}`}>
          {pct(u)}
        </span>
      ))}
      <span class="check-status">
        <span class={`tone-${statusTone(status)}`}>
          {state.flapping ? "Flapping" : STATUS_LABEL[status]}
        </span>
        <span class="muted">
          {status === "up" || status === "down" ? ` ${span(since(state.since))}` : ""}
        </span>
        {cert !== null && c.cert_days !== -1 && (
          <span class={`cert tone-${certTone(cert, c.cert_days)}`} title="Certificate expires">
            {cert}d cert
          </span>
        )}
      </span>
      {state.message && status !== "up" && <span class="check-msg">{state.message}</span>}
    </a>
  );
}

function SpeedFigure(props: { data: Overview }) {
  const t = props.data.speedtest.latest;
  if (!t) return null;
  return (
    <a href="/speedtests" class="figure figure-link" title={t.server}>
      <div class="figure-value">
        {mbps(t.download)}
        <span class="figure-unit">↓</span> {mbps(t.upload)}
        <span class="figure-unit">↑ Mbit/s</span>
      </div>
      <div class="eyebrow figure-label">Speed · {ago(t.time)}</div>
    </a>
  );
}

function Incidents(props: { data: Overview }) {
  const open = props.data.incidents.filter((i) => !i.ended);
  if (!open.length) return null;
  const names = new Map(props.data.checks.map((r) => [r.check.id, r.check.name]));
  return (
    <div class="note note-bad">
      {open.map((i) => (
        <div key={i.id}>
          <a class="link" href={`/checks/${i.check_id}`}>
            <strong>{names.get(i.check_id) ?? "A check"}</strong>
          </a>{" "}
          down for {span(since(i.started))}: <span class="muted">{i.cause}</span>
        </div>
      ))}
    </div>
  );
}
