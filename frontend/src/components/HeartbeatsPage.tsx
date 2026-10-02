import { Plus } from "lucide-preact";
import { useState } from "preact/hooks";
import { api } from "../api";
import { useData } from "../hooks";
import {
  ago,
  heartbeatDue,
  pct,
  pingURL,
  span,
  STATUS_LABEL,
  statusTone,
  until,
  uptimeTone,
} from "../lib";
import type { Check, CheckRow } from "../types";
import { CheckForm } from "./CheckForm";
import { CopyField, Dot, ErrorNote, Figure } from "./ui";

export function HeartbeatsPage() {
  const { data, error, reload } = useData(api.checks, 10_000);
  const [adding, setAdding] = useState(false);
  if (!data) return error ? <ErrorNote>{error}</ErrorNote> : <div class="skeleton page-skeleton" />;
  const beats = data.filter((r) => r.check.type === "push");
  const late = beats.filter((r) => r.status === "down").length;

  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">Push checks</div>
        <h1 class="page-title">Heartbeats</h1>
        <p class="page-lede muted">
          Backups, cron jobs and scripts ping their URL when they run. A heartbeat goes down when a
          ping is late, when the job reports a failure, or when it started and didn't finish.
        </p>
        <div class="figures">
          <Figure value={beats.length - late} unit={`/${beats.length}`} label="On time" />
          <Figure value={late} label="Late or failed" tone={late ? "bad" : ""} />
        </div>
      </header>
      {error && <ErrorNote>{error}</ErrorNote>}
      <div class="toolbar">
        <span class="spacer" />
        <button class="btn btn-primary" onClick={() => setAdding(true)}>
          <Plus size={16} /> Add heartbeat
        </button>
      </div>
      {beats.length === 0 ? (
        <div class="empty">No heartbeats yet.</div>
      ) : (
        <div class="beat-list stagger">
          {beats.map((r) => (
            <Beat key={r.check.id} row={r} />
          ))}
        </div>
      )}
      {adding && (
        <CheckForm
          type="push"
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

function Beat(props: { row: CheckRow }) {
  const { check: c, state, status } = props.row;
  const due = heartbeatDue(props.row);
  return (
    <a class="beat" href={`/checks/${c.id}`}>
      <span class="check-name">
        <Dot tone={statusTone(status)} />
        <span>
          <span class="check-title">{c.name}</span>
          <span class="check-sub">
            every {span(c.period ?? 0)} · grace {span(c.grace ?? 0)}
          </span>
        </span>
      </span>
      <span>
        <span class="eyebrow">Last ping</span>
        <span>{state.last_ping ? ago(state.last_ping) : "never"}</span>
      </span>
      <span>
        <span class="eyebrow">{status === "down" ? "Status" : "Due"}</span>
        <span class={`tone-${statusTone(status)}`}>
          {status === "down"
            ? STATUS_LABEL[status]
            : status === "running"
              ? "running now"
              : until(due)}
        </span>
      </span>
      <span>
        <span class="eyebrow">30 d</span>
        <span class={`mono tone-${uptimeTone(props.row.uptime[2])}`}>
          {pct(props.row.uptime[2])}
        </span>
      </span>
      {state.message && <span class="check-msg muted">{state.message}</span>}
    </a>
  );
}

/** The ping URL with examples for cron and scripts. */
export function PingHelp(props: { check: Check }) {
  const url = pingURL(props.check.token);
  return (
    <div class="ping-help">
      <CopyField value={url} label="Copy ping URL" />
      <div class="ping-examples">
        <div>
          <div class="eyebrow">After a job</div>
          <code>{`your-job && curl -fsS -m 10 --retry 3 ${url} > /dev/null`}</code>
        </div>
        <div>
          <div class="eyebrow">Start, then exit code (catches hung jobs)</div>
          <code>{`curl -fsS -m 10 ${url}/start; your-job; curl -fsS -m 10 ${url}/$?`}</code>
        </div>
        <div>
          <div class="eyebrow">With the job's output</div>
          <code>{`your-job 2>&1 | tail -20 | curl -fsS -m 10 --data-binary @- ${url}`}</code>
        </div>
      </div>
      <p class="field-hint">
        <code>/fail</code> reports a failure; any non-zero <code>/&lt;exit code&gt;</code> does too.
        A POST body (up to 10 KB) is kept as the message.
      </p>
    </div>
  );
}
