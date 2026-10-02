import { Gauge, Trash2 } from "lucide-preact";
import { useEffect, useMemo, useState } from "preact/hooks";
import { api } from "../api";
import { useData } from "../hooks";
import { ago, mbps, ms, until } from "../lib";
import type { Settings, Speedtest } from "../types";
import { Legend, LineChart, SERIES_COLORS } from "./charts";
import { Dot, ErrorNote, Field, Figure, SectionHead, useAction } from "./ui";

const RANGES = [
  ["7d", "7 days"],
  ["30d", "30 days"],
  ["90d", "90 days"],
  ["all", "All"],
] as const;

export function SpeedtestPage() {
  const [range, setRange] = useState<string>("30d");
  const { data, error, reload } = useData(() => api.speedtests(range), 0, [range]);
  const action = useAction();

  // Poll while a test runs.
  useEffect(() => {
    if (!data?.running) return;
    const t = setTimeout(reload, 3000);
    return () => clearTimeout(t);
  }, [data]);

  const ok = useMemo(() => (data?.results ?? []).filter((r) => !r.error), [data]);
  const times = useMemo(() => ok.map((r) => new Date(r.time).getTime()), [ok]);
  const speed = useMemo(
    () => [
      { label: "Download", color: SERIES_COLORS[0], values: ok.map((r) => r.download) },
      { label: "Upload", color: SERIES_COLORS[1], values: ok.map((r) => r.upload) },
    ],
    [ok],
  );
  const ping = useMemo(
    () => [{ label: "Ping", color: "var(--accent)", values: ok.map((r) => r.ping) }],
    [ok],
  );

  if (!data) return error ? <ErrorNote>{error}</ErrorNote> : <div class="skeleton page-skeleton" />;
  const latest = ok[ok.length - 1];
  const avg = (f: (r: Speedtest) => number) =>
    ok.length ? ok.reduce((s, r) => s + f(r), 0) / ok.length : 0;

  const run = async () => {
    await action.run(api.runSpeedtest);
    reload();
  };

  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">
          {latest ? `Latest ${ago(latest.time)} · ${latest.server}` : "No results yet"}
        </div>
        <h1 class="page-title">Speed</h1>
        {latest && (
          <div class="figures">
            <Figure value={mbps(latest.download)} unit="Mbit/s" label="Download" />
            <Figure value={mbps(latest.upload)} unit="Mbit/s" label="Upload" />
            <Figure value={ms(latest.ping)} label={`Ping · jitter ${ms(latest.jitter)}`} />
            {latest.isp && <Figure value={latest.isp} label="Provider" />}
          </div>
        )}
        <div class="toolbar">
          <button class="btn btn-primary" onClick={run} disabled={data.running || action.busy}>
            <Gauge size={16} class={data.running ? "spin" : ""} />
            {data.running ? "Running…" : "Run a speedtest"}
          </button>
          {data.next && !data.running && (
            <span class="muted">Next scheduled {until(data.next)}</span>
          )}
          {action.error && <span class="form-error">{action.error}</span>}
        </div>
      </header>
      {error && <ErrorNote>{error}</ErrorNote>}

      <section class="section">
        <SectionHead index={1} title="Bandwidth">
          <div class="seg" role="tablist">
            {RANGES.map(([r, label]) => (
              <button
                key={r}
                role="tab"
                aria-selected={range === r}
                class={range === r ? "active" : ""}
                onClick={() => setRange(r)}
              >
                {label}
              </button>
            ))}
          </div>
        </SectionHead>
        <Legend
          series={[
            {
              label: "Download",
              color: SERIES_COLORS[0],
              value: ok.length ? `avg ${mbps(avg((r) => r.download))}` : undefined,
            },
            {
              label: "Upload",
              color: SERIES_COLORS[1],
              value: ok.length ? `avg ${mbps(avg((r) => r.upload))}` : undefined,
            },
          ]}
        />
        <LineChart
          times={times}
          series={speed}
          unit="Mbit/s"
          format={(v) => `${mbps(v)} Mbit/s`}
          note={(i) => ok[i].server}
          empty="No speedtests in this range"
        />
        <div class="eyebrow chart-title">Ping</div>
        <LineChart
          times={times}
          series={ping}
          unit="ms"
          format={ms}
          height={140}
          note={(i) => `jitter ${ms(ok[i].jitter)}`}
          empty="No speedtests in this range"
        />
      </section>

      <SpeedSettings />

      <section class="section">
        <SectionHead index={3} title="Results">
          <span class="eyebrow">{data.results.length}</span>
        </SectionHead>
        <Results results={data.results} onDeleted={reload} />
      </section>
    </div>
  );
}

function Results(props: { results: Speedtest[]; onDeleted: () => void }) {
  const [all, setAll] = useState(false);
  const list = [...props.results].reverse();
  const shown = all ? list : list.slice(0, 25);
  if (!list.length) return <div class="empty">No results in this range.</div>;
  const remove = async (id: number) => {
    await api.deleteSpeedtest(id).catch(() => {});
    props.onDeleted();
  };
  return (
    <>
      <div class="table-wrap">
        <table class="table">
          <thead>
            <tr>
              <th>When</th>
              <th class="num">Download</th>
              <th class="num">Upload</th>
              <th class="num">Ping</th>
              <th class="num">Jitter</th>
              <th>Server</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {shown.map((r) => (
              <tr key={r.id}>
                <td>
                  <Dot tone={r.error ? "bad" : ""} />{" "}
                  {new Date(r.time).toLocaleString([], {
                    day: "numeric",
                    month: "short",
                    hour: "2-digit",
                    minute: "2-digit",
                  })}
                  {r.source !== "scheduled" && <span class="muted"> · {r.source}</span>}
                </td>
                {r.error ? (
                  <td colSpan={5} class="tone-bad">
                    {r.error}
                  </td>
                ) : (
                  <>
                    <td class="num mono">{mbps(r.download)}</td>
                    <td class="num mono">{mbps(r.upload)}</td>
                    <td class="num mono">{ms(r.ping)}</td>
                    <td class="num mono">{ms(r.jitter)}</td>
                    <td class="muted">{r.server}</td>
                  </>
                )}
                <td>
                  <button
                    class="icon-btn"
                    title="Delete"
                    aria-label="Delete result"
                    onClick={() => remove(r.id)}
                  >
                    <Trash2 size={14} />
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {!all && list.length > shown.length && (
        <div class="load-more">
          <button class="btn btn-small" onClick={() => setAll(true)}>
            Show all {list.length}
          </button>
        </div>
      )}
    </>
  );
}

function SpeedSettings() {
  const { data } = useData(api.settings);
  const [s, setS] = useState<Settings | null>(null);
  const save = useAction();
  const [saved, setSaved] = useState(false);
  useEffect(() => {
    if (data) setS(data);
  }, [data]);
  if (!s) return null;
  const sp = s.speedtest;
  const set = (patch: Partial<Settings["speedtest"]>) => {
    setS({ ...s, speedtest: { ...sp, ...patch } });
    setSaved(false);
  };
  const num = (v: string) => (v === "" ? 0 : Number(v));
  const submit = async (e: Event) => {
    e.preventDefault();
    const out = await save.run(() => api.saveSettings(s));
    if (out) {
      setS(out);
      setSaved(true);
    }
  };
  return (
    <section class="section">
      <SectionHead index={2} title="Schedule and alerts" />
      <form class="settings-form" onSubmit={submit}>
        <div class="form-grid">
          <Field label="Schedule" hint="Cron: minute hour day month weekday">
            <input
              class="input code"
              value={sp.schedule}
              onInput={(e) => set({ schedule: e.currentTarget.value })}
            />
          </Field>
          <Field label="Server ID" hint="Empty picks the fastest nearby">
            <input
              class="input code"
              value={sp.server_id ?? ""}
              onInput={(e) => set({ server_id: e.currentTarget.value })}
            />
          </Field>
          <Field label="Alert below download" hint="Mbit/s; 0 is off">
            <input
              class="input"
              type="number"
              min={0}
              value={sp.min_download ?? 0}
              onInput={(e) => set({ min_download: num(e.currentTarget.value) })}
            />
          </Field>
          <Field label="Alert below upload" hint="Mbit/s; 0 is off">
            <input
              class="input"
              type="number"
              min={0}
              value={sp.min_upload ?? 0}
              onInput={(e) => set({ min_upload: num(e.currentTarget.value) })}
            />
          </Field>
          <Field label="Alert above ping" hint="ms; 0 is off">
            <input
              class="input"
              type="number"
              min={0}
              value={sp.max_ping ?? 0}
              onInput={(e) => set({ max_ping: num(e.currentTarget.value) })}
            />
          </Field>
        </div>
        <div class="toolbar">
          <label class="check">
            <input
              type="checkbox"
              checked={sp.enabled}
              onChange={(e) => set({ enabled: e.currentTarget.checked })}
            />
            Run on the schedule
          </label>
          <span class="spacer" />
          {saved && <span class="tone-good">Saved</span>}
          {save.error && <span class="form-error">{save.error}</span>}
          <button class="btn" disabled={save.busy}>
            Save
          </button>
        </div>
      </form>
    </section>
  );
}
