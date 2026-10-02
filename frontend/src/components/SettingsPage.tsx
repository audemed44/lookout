import { Download, Pencil, Plus, RefreshCw, Trash2, Upload } from "lucide-preact";
import { useEffect, useState } from "preact/hooks";
import { api, type ImportReport, type KumaReport } from "../api";
import { useData } from "../hooks";
import { ago, splitList } from "../lib";
import type { Settings, Window } from "../types";
import { CopyField, Dialog, Dot, Empty, ErrorNote, Field, SectionHead, useAction } from "./ui";

export function SettingsPage() {
  const { data, error, setData } = useData(api.settings);
  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">Lookout</div>
        <h1 class="page-title">Settings</h1>
      </header>
      {error && <ErrorNote>{error}</ErrorNote>}
      <Maintenance />
      {data && <Discovery settings={data} onChange={setData} />}
      {data && <StatusPageSettings settings={data} onChange={setData} />}
      <Imports />
      <Configuration />
      {data && <RetentionSettings settings={data} onChange={setData} />}
    </div>
  );
}

const DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

function windowText(w: Window): string {
  const scope = [...(w.checks ?? []), ...(w.tags ?? []).map((t) => `#${t}`)].join(", ");
  const when =
    w.repeat === "daily"
      ? `every day ${w.from} for ${w.minutes} min`
      : w.repeat === "weekly"
        ? `${(w.weekdays ?? []).map((d) => DAYS[d]).join(", ")} ${w.from} for ${w.minutes} min`
        : `${new Date(w.start!).toLocaleString()} → ${w.end ? new Date(w.end).toLocaleString() : "until turned off"}`;
  return `${when} · ${scope || "all checks"}`;
}

function Maintenance() {
  const { data, error, reload } = useData(api.windows, 30_000);
  const [editing, setEditing] = useState<Partial<Window> | null>(null);
  const remove = async (w: Window) => {
    if (!confirm(`Remove ${w.name}?`)) return;
    await api.deleteWindow(w.id).catch(() => {});
    reload();
  };
  return (
    <section class="section">
      <SectionHead index={1} title="Maintenance">
        <button
          class="btn btn-small"
          onClick={() => setEditing({ enabled: true, repeat: "", minutes: 60, from: "03:00" })}
        >
          <Plus size={14} /> Add window
        </button>
      </SectionHead>
      <p class="field-hint">
        Checks a window covers aren't run and don't notify while it's on. To stop one check for a
        while, pause it from its page.
      </p>
      {error && <ErrorNote>{error}</ErrorNote>}
      {data && data.length === 0 && <Empty>No maintenance windows.</Empty>}
      <div class="list">
        {(data ?? []).map((w) => (
          <div key={w.id} class="list-row">
            <Dot tone={w.active ? "accent" : ""} />
            <div class="list-main">
              <span class="list-title">
                {w.name} {w.active && <span class="chip chip-accent">on now</span>}
                {!w.enabled && <span class="chip">off</span>}
              </span>
              <span class="list-sub">{windowText(w)}</span>
            </div>
            <span class="spacer" />
            <button class="icon-btn" aria-label="Edit" onClick={() => setEditing(w)}>
              <Pencil size={14} />
            </button>
            <button class="icon-btn" aria-label="Remove" onClick={() => remove(w)}>
              <Trash2 size={14} />
            </button>
          </div>
        ))}
      </div>
      {editing && (
        <WindowForm
          window={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            reload();
          }}
        />
      )}
    </section>
  );
}

/** datetime-local wants local time without a zone. */
function toLocalInput(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  return new Date(d.getTime() - d.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
}

function WindowForm(props: { window: Partial<Window>; onClose: () => void; onSaved: () => void }) {
  const [w, setW] = useState(props.window);
  const [checks, setChecks] = useState((props.window.checks ?? []).join(", "));
  const [tags, setTags] = useState((props.window.tags ?? []).join(", "));
  const save = useAction();
  const set = (patch: Partial<Window>) => setW({ ...w, ...patch });
  const submit = async (e: Event) => {
    e.preventDefault();
    const body = {
      ...w,
      checks: checks
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean),
      tags: splitList(tags),
    };
    if (await save.run(() => api.saveWindow(body))) props.onSaved();
  };
  return (
    <Dialog
      title={w.id ? `Edit ${props.window.name}` : "Add a maintenance window"}
      onClose={props.onClose}
      footer={
        <>
          <button class="btn btn-primary" form="window-form" disabled={save.busy}>
            Save
          </button>
          {save.error && <span class="form-error">{save.error}</span>}
        </>
      }
    >
      <form id="window-form" class="stack-form" onSubmit={submit}>
        <Field label="Name">
          <input
            class="input"
            required
            value={w.name ?? ""}
            onInput={(e) => set({ name: e.currentTarget.value })}
          />
        </Field>
        <Field label="Repeats">
          <select
            class="input"
            value={w.repeat ?? ""}
            onChange={(e) => set({ repeat: e.currentTarget.value as Window["repeat"] })}
          >
            <option value="">Once</option>
            <option value="daily">Every day</option>
            <option value="weekly">Every week</option>
          </select>
        </Field>
        {w.repeat ? (
          <div class="form-grid">
            <Field label="From">
              <input
                class="input"
                type="time"
                value={w.from ?? ""}
                onInput={(e) => set({ from: e.currentTarget.value })}
              />
            </Field>
            <Field label="For (minutes)">
              <input
                class="input"
                type="number"
                min={1}
                value={w.minutes ?? 60}
                onInput={(e) => set({ minutes: Number(e.currentTarget.value) })}
              />
            </Field>
          </div>
        ) : (
          <div class="form-grid">
            <Field label="Start">
              <input
                class="input"
                type="datetime-local"
                required
                value={toLocalInput(w.start)}
                onInput={(e) => set({ start: new Date(e.currentTarget.value).toISOString() })}
              />
            </Field>
            <Field label="End" hint="Empty: until turned off">
              <input
                class="input"
                type="datetime-local"
                value={toLocalInput(w.end)}
                onInput={(e) =>
                  set({
                    end: e.currentTarget.value
                      ? new Date(e.currentTarget.value).toISOString()
                      : undefined,
                  })
                }
              />
            </Field>
          </div>
        )}
        {w.repeat === "weekly" && (
          <div class="weekdays">
            {DAYS.map((d, i) => (
              <label key={d} class="check">
                <input
                  type="checkbox"
                  checked={(w.weekdays ?? []).includes(i)}
                  onChange={(e) =>
                    set({
                      weekdays: e.currentTarget.checked
                        ? [...(w.weekdays ?? []), i].sort()
                        : (w.weekdays ?? []).filter((x) => x !== i),
                    })
                  }
                />
                {d}
              </label>
            ))}
          </div>
        )}
        <Field label="Checks" hint="Names, comma-separated">
          <input class="input" value={checks} onInput={(e) => setChecks(e.currentTarget.value)} />
        </Field>
        <Field label="Tags" hint="And checks with any of these tags. Neither: all checks.">
          <input class="input" value={tags} onInput={(e) => setTags(e.currentTarget.value)} />
        </Field>
        <label class="check">
          <input
            type="checkbox"
            checked={w.enabled ?? true}
            onChange={(e) => set({ enabled: e.currentTarget.checked })}
          />
          Enabled
        </label>
      </form>
    </Dialog>
  );
}

function useSettingsForm(settings: Settings, onChange: (s: Settings) => void) {
  const [s, setS] = useState(settings);
  const [saved, setSaved] = useState(false);
  const save = useAction();
  useEffect(() => setS(settings), [settings]);
  const submit = async (e: Event) => {
    e.preventDefault();
    const out = await save.run(() => api.saveSettings(s));
    if (out) {
      onChange(out);
      setSaved(true);
    }
  };
  const update = (next: Settings) => {
    setS(next);
    setSaved(false);
  };
  return { s, update, submit, save, saved };
}

function SaveBar(props: {
  saved: boolean;
  error: string;
  busy: boolean;
  children?: preact.ComponentChildren;
}) {
  return (
    <div class="toolbar">
      {props.children}
      <span class="spacer" />
      {props.saved && <span class="tone-good">Saved</span>}
      {props.error && <span class="form-error">{props.error}</span>}
      <button class="btn" disabled={props.busy}>
        Save
      </button>
    </div>
  );
}

function Discovery(props: { settings: Settings; onChange: (s: Settings) => void }) {
  const info = useData(api.discovery);
  const sync = useAction();
  const form = useSettingsForm(props.settings, props.onChange);
  const d = form.s.discovery;
  const set = (patch: Partial<Settings["discovery"]>) =>
    form.update({ ...form.s, discovery: { ...d, ...patch } });
  const [tags, setTags] = useState((d.tags ?? []).join(", "));
  const runSync = async () => {
    await sync.run(api.sync);
    info.reload();
  };
  const unignore = async (domain: string) => {
    const out = await api.ignore(domain, false).catch(() => null);
    if (out) props.onChange(out);
  };
  const last = info.data?.last;
  return (
    <section class="section">
      <SectionHead index={2} title="Proxy discovery">
        {info.data?.configured && (
          <button class="btn btn-small" onClick={runSync} disabled={sync.busy}>
            <RefreshCw size={14} class={sync.busy ? "spin" : ""} /> Sync now
          </button>
        )}
      </SectionHead>
      <p class="field-hint">
        Adds an HTTP check for every domain your proxy (Gatehouse or Nginx Proxy Manager) serves
        (any answer under 500 counts as up, so login pages pass). Deleting a discovered check
        ignores its domain. Apps Gatehouse has put to sleep show as asleep, not down.
      </p>
      {info.data && !info.data.configured && (
        <div class="note">
          Set <code>LOOKOUT_GATEHOUSE_URL</code> (e.g. <code>http://gatehouse:8081</code>) and{" "}
          <code>LOOKOUT_GATEHOUSE_TOKEN</code> in the stack's .env to turn this on, or{" "}
          <code>LOOKOUT_NPM_URL</code>, <code>LOOKOUT_NPM_EMAIL</code> and{" "}
          <code>LOOKOUT_NPM_PASSWORD</code> for Nginx Proxy Manager.
        </div>
      )}
      {sync.error && <ErrorNote>{sync.error}</ErrorNote>}
      {last && (
        <div class={`note ${last.error ? "note-bad" : ""}`}>
          {last.error ? (
            <>
              Last sync {ago(last.time)} failed: {last.error}
            </>
          ) : (
            <>
              Last sync {ago(last.time)}: {last.routes} domains
              {last.added.length > 0 && <>, added {last.added.join(", ")}</>}
              {last.updated.length > 0 && <>, updated {last.updated.join(", ")}</>}
            </>
          )}
          {last.missing.length > 0 && (
            <div class="tone-warn">No longer in the proxy: {last.missing.join(", ")}</div>
          )}
        </div>
      )}
      <form class="settings-form" onSubmit={form.submit}>
        <div class="form-grid">
          <Field label="Sync every" hint="Minutes">
            <input
              class="input"
              type="number"
              min={5}
              value={d.every}
              onInput={(e) => set({ every: Number(e.currentTarget.value) })}
            />
          </Field>
          <Field label="Check interval" hint="Seconds, for new checks">
            <input
              class="input"
              type="number"
              min={10}
              value={d.interval}
              onInput={(e) => set({ interval: Number(e.currentTarget.value) })}
            />
          </Field>
          <Field label="Tags for new checks">
            <input
              class="input"
              value={tags}
              onInput={(e) => {
                setTags(e.currentTarget.value);
                set({ tags: splitList(e.currentTarget.value) });
              }}
            />
          </Field>
        </div>
        {(form.s.discovery.ignored ?? []).length > 0 && (
          <div class="field">
            <span class="field-label">Ignored domains</span>
            <div class="chips">
              {form.s.discovery.ignored!.map((dom) => (
                <button
                  key={dom}
                  type="button"
                  class="chip chip-button"
                  title="Watch it again on the next sync"
                  onClick={() => unignore(dom)}
                >
                  {dom} ×
                </button>
              ))}
            </div>
          </div>
        )}
        <SaveBar saved={form.saved} error={form.save.error} busy={form.save.busy}>
          <label class="check">
            <input
              type="checkbox"
              checked={d.auto}
              onChange={(e) => set({ auto: e.currentTarget.checked })}
            />
            Sync automatically
          </label>
        </SaveBar>
      </form>
    </section>
  );
}

function StatusPageSettings(props: { settings: Settings; onChange: (s: Settings) => void }) {
  const form = useSettingsForm(props.settings, props.onChange);
  const p = form.s.status_page;
  const [tags, setTags] = useState((p.tags ?? []).join(", "));
  const set = (patch: Partial<Settings["status_page"]>) =>
    form.update({ ...form.s, status_page: { ...p, ...patch } });
  return (
    <section class="section">
      <SectionHead index={3} title="Status page" />
      <p class="field-hint">
        A read-only page anyone who can reach Lookout can open without the token: check names,
        status and 30 days of uptime. No targets or messages.
      </p>
      {props.settings.status_page.enabled && (
        <CopyField value={`${window.location.origin}/status`} label="Copy status page URL" />
      )}
      <form class="settings-form" onSubmit={form.submit}>
        <div class="form-grid">
          <Field label="Title">
            <input
              class="input"
              placeholder="Status"
              value={p.title ?? ""}
              onInput={(e) => set({ title: e.currentTarget.value })}
            />
          </Field>
          <Field label="Only checks tagged" hint="Empty shows every check">
            <input
              class="input"
              value={tags}
              onInput={(e) => {
                setTags(e.currentTarget.value);
                set({ tags: splitList(e.currentTarget.value) });
              }}
            />
          </Field>
        </div>
        <SaveBar saved={form.saved} error={form.save.error} busy={form.save.busy}>
          <label class="check">
            <input
              type="checkbox"
              checked={p.enabled}
              onChange={(e) => set({ enabled: e.currentTarget.checked })}
            />
            Turn on{" "}
            <a class="link-btn" href="/status" target="_blank">
              /status
            </a>
          </label>
        </SaveBar>
      </form>
    </section>
  );
}

function FilePick(props: {
  accept?: string;
  label: string;
  onFile: (f: File) => void;
  busy: boolean;
}) {
  return (
    <label class={`btn ${props.busy ? "is-busy" : ""}`}>
      <Upload size={15} class={props.busy ? "spin" : ""} />{" "}
      {props.busy ? "Importing…" : props.label}
      <input
        type="file"
        accept={props.accept}
        hidden
        disabled={props.busy}
        onChange={(e) => {
          const f = e.currentTarget.files?.[0];
          e.currentTarget.value = "";
          if (f) props.onFile(f);
        }}
      />
    </label>
  );
}

function Imports() {
  const kuma = useAction();
  const st = useAction();
  const [history, setHistory] = useState(true);
  const [kumaRep, setKumaRep] = useState<KumaReport | null>(null);
  const [stRep, setStRep] = useState("");
  return (
    <section class="section">
      <SectionHead index={4} title="Import" />
      <div class="import-grid">
        <div class="import-card">
          <h3>Uptime Kuma</h3>
          <p class="field-hint">
            Upload <code>kuma.db</code> from Kuma's data folder. Stop Kuma first, or copy it with{" "}
            <code>sqlite3 kuma.db ".backup kuma-copy.db"</code>, so recent changes aren't left in
            its WAL. HTTP, keyword, JSON, port, ping, DNS, Docker and push monitors come over; push
            URLs keep their tokens, so jobs only change the host.
          </p>
          <label class="check">
            <input
              type="checkbox"
              checked={history}
              onChange={(e) => setHistory(e.currentTarget.checked)}
            />
            Bring the uptime history too (hourly)
          </label>
          <FilePick
            label="Upload kuma.db"
            busy={kuma.busy}
            onFile={async (f) => {
              setKumaRep(null);
              const out = await kuma.run(() => api.importKuma(f, history));
              if (out) setKumaRep(out);
            }}
          />
          {kuma.error && <span class="form-error">{kuma.error}</span>}
          {kumaRep && (
            <div class="note">
              Imported {kumaRep.imported.length}
              {kumaRep.imported.length > 0 && `: ${kumaRep.imported.join(", ")}`}
              {kumaRep.hours > 0 && ` with ${kumaRep.hours} hours of history`}.
              {kumaRep.existing.length > 0 && (
                <div class="muted">Already here: {kumaRep.existing.join(", ")}</div>
              )}
              {kumaRep.skipped.length > 0 && (
                <ul>
                  {kumaRep.skipped.map((s) => (
                    <li key={s} class="tone-warn">
                      {s}
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )}
        </div>
        <div class="import-card">
          <h3>Speedtest Tracker</h3>
          <p class="field-hint">
            Upload <code>database.sqlite</code> from its config folder. Completed results come over;
            importing twice skips what's already here.
          </p>
          <FilePick
            label="Upload database.sqlite"
            busy={st.busy}
            onFile={async (f) => {
              setStRep("");
              const out = await st.run(() => api.importSpeedtests(f));
              if (out) {
                setStRep(
                  `Imported ${out.imported} results` +
                    (out.existing ? `, ${out.existing} already here` : "") +
                    (out.skipped ? `, skipped ${out.skipped} failed ones` : "") +
                    ".",
                );
              }
            }}
          />
          {st.error && <span class="form-error">{st.error}</span>}
          {stRep && <div class="note">{stRep}</div>}
        </div>
      </div>
    </section>
  );
}

function Configuration() {
  const imp = useAction();
  const [replace, setReplace] = useState(false);
  const [report, setReport] = useState<ImportReport | null>(null);
  return (
    <section class="section">
      <SectionHead index={5} title="Configuration" />
      <p class="field-hint">
        Checks, maintenance windows, targets, routes and settings as YAML, to keep in git. Target
        URLs are only exported when they're <code>{"${VAR}"}</code> references. A file at{" "}
        <code>/data/lookout.yaml</code> (or <code>LOOKOUT_CONFIG</code>) is merged in at start.
      </p>
      <div class="toolbar">
        <a class="btn" href="/api/config" download="lookout.yaml">
          <Download size={15} /> Export YAML
        </a>
        <FilePick
          label="Import YAML"
          accept=".yaml,.yml"
          busy={imp.busy}
          onFile={async (f) => {
            setReport(null);
            const text = await f.text();
            if (replace && !confirm("Remove everything the file doesn't list?")) return;
            const out = await imp.run(() => api.importConfig(text, replace));
            if (out) setReport(out);
          }}
        />
        <label class="check">
          <input
            type="checkbox"
            checked={replace}
            onChange={(e) => setReplace(e.currentTarget.checked)}
          />
          Replace: remove what the file doesn't list
        </label>
      </div>
      {imp.error && <ErrorNote>{imp.error}</ErrorNote>}
      {report && (
        <div class="note">
          Added {report.added.length}, updated {report.updated.length}
          {report.removed.length > 0 && `, removed ${report.removed.join(", ")}`}.
          {report.warnings.map((w) => (
            <div key={w} class="tone-warn">
              {w}
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

function RetentionSettings(props: { settings: Settings; onChange: (s: Settings) => void }) {
  const form = useSettingsForm(props.settings, props.onChange);
  const r = form.s.retention;
  const set = (patch: Partial<Settings["retention"]>) =>
    form.update({ ...form.s, retention: { ...r, ...patch } });
  return (
    <section class="section">
      <SectionHead index={6} title="History" />
      <p class="field-hint">
        Every result is kept for a while, then only hourly summaries, which is what uptime and the
        longer charts use.
      </p>
      <form class="settings-form" onSubmit={form.submit}>
        <div class="form-grid">
          <Field label="Every result for" hint="Hours">
            <input
              class="input"
              type="number"
              min={1}
              value={r.raw_hours}
              onInput={(e) => set({ raw_hours: Number(e.currentTarget.value) })}
            />
          </Field>
          <Field label="Hourly summaries for" hint="Days">
            <input
              class="input"
              type="number"
              min={1}
              value={r.rollup_days}
              onInput={(e) => set({ rollup_days: Number(e.currentTarget.value) })}
            />
          </Field>
          <Field label="Notifications for" hint="Days">
            <input
              class="input"
              type="number"
              min={1}
              value={r.notification_days}
              onInput={(e) => set({ notification_days: Number(e.currentTarget.value) })}
            />
          </Field>
        </div>
        <SaveBar saved={form.saved} error={form.save.error} busy={form.save.busy} />
      </form>
    </section>
  );
}
