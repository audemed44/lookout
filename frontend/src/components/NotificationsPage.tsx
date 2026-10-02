import { Pencil, Plus, Send, Trash2 } from "lucide-preact";
import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { useData } from "../hooks";
import { ago, deliveries, NOTE_STATUS, notifyURL, splitList, TYPE_TONE, until } from "../lib";
import type { Notification, Route, Sender, Settings, Target } from "../types";
import { CopyField, Dialog, Dot, Empty, ErrorNote, Field, SectionHead, useAction } from "./ui";

export function NotificationsPage() {
  const targets = useData(api.targets);
  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">Apprise-compatible</div>
        <h1 class="page-title">Notify</h1>
        <p class="page-lede muted">
          Lookout's own alerts and anything other apps post to <code>/notify/&lt;key&gt;</code> go
          through the same routes. Apps that used apprise-api only need the new URL.
        </p>
      </header>
      <History />
      <Targets data={targets.data} error={targets.error} reload={targets.reload} />
      <Routes targets={targets.data ?? []} />
      <Senders />
      <Delivery />
    </div>
  );
}

function History() {
  const [items, setItems] = useState<Notification[] | null>(null);
  const [queued, setQueued] = useState(0);
  const [more, setMore] = useState(true);
  const action = useAction();
  const load = async (before = 0) => {
    const out = await action.run(() => api.notifications(before));
    if (!out) return;
    setQueued(out.queued);
    setItems((prev) => (before && prev ? [...prev, ...out.items] : out.items));
    setMore(out.items.length === 50);
  };
  useEffect(() => {
    load();
    const t = setInterval(() => document.visibilityState === "visible" && load(), 15_000);
    return () => clearInterval(t);
  }, []);
  const flush = async () => {
    await action.run(api.flush);
    load();
  };

  return (
    <section class="section">
      <SectionHead index={1} title="History">
        {queued > 0 && (
          <button class="btn btn-small" onClick={flush} disabled={action.busy}>
            Send {queued} held now
          </button>
        )}
      </SectionHead>
      {action.error && <ErrorNote>{action.error}</ErrorNote>}
      {items === null ? (
        <div class="skeleton list-skeleton" />
      ) : items.length === 0 ? (
        <Empty>Nothing sent yet.</Empty>
      ) : (
        <div class="list notes">
          {items.map((n) => (
            <NoteRow key={n.id} n={n} />
          ))}
        </div>
      )}
      {items && more && items.length > 0 && (
        <div class="load-more">
          <button
            class="btn btn-small"
            onClick={() => load(items[items.length - 1].id)}
            disabled={action.busy}
          >
            Older
          </button>
        </div>
      )}
    </section>
  );
}

function NoteRow(props: { n: Notification }) {
  const { n } = props;
  const st = NOTE_STATUS[n.status] ?? { label: n.status, tone: "" };
  const ds = deliveries(n);
  return (
    <div class="list-row note-row">
      <Dot tone={TYPE_TONE[n.type] ?? ""} title={n.type} />
      <div class="list-main">
        <span class="list-title">{n.title || n.body}</span>
        {n.title && n.body && n.body !== n.title && <span class="note-body">{n.body}</span>}
        <span class="list-sub">
          {ago(n.time)} · from {n.source}
          {n.tags.length > 0 && <> · {n.tags.join(", ")}</>}
        </span>
        {ds.length > 0 && (
          <span class="deliveries">
            {ds.map((d) => (
              <span
                key={d.target}
                class={`chip ${d.state === "failed" ? "chip-remove" : d.state === "queued" ? "chip-accent" : ""}`}
                title={d.error ?? (d.until ? `until ${new Date(d.until).toLocaleString()}` : "")}
              >
                {d.target}
                {d.state === "queued" && ` · ${d.reason} ${until(d.until)}`}
                {d.state === "failed" && " · failed"}
              </span>
            ))}
          </span>
        )}
        {ds.some((d) => d.error) && (
          <span class="form-error">{ds.find((d) => d.error)!.error}</span>
        )}
        {!ds.length && n.detail && <span class="list-sub">{n.detail}</span>}
      </div>
      <span class="spacer" />
      <span
        class={`chip ${st.tone === "bad" ? "chip-remove" : st.tone === "warn" ? "chip-warn" : ""}`}
      >
        {st.label}
      </span>
    </div>
  );
}

const EXAMPLES = [
  ["Telegram", "tgram://<bot token>/<chat id>"],
  ["ntfy", "ntfys://ntfy.sh/<topic>"],
  ["Discord", "discord://<webhook id>/<webhook token>"],
  ["Webhook (JSON)", "jsons://example.com/hook"],
];

function Targets(props: { data: Target[] | null; error: string; reload: () => void }) {
  const [editing, setEditing] = useState<Partial<Target> | null>(null);
  const [result, setResult] = useState<Record<number, string>>({});
  const test = async (t: Target) => {
    setResult({ ...result, [t.id]: "Sending…" });
    try {
      const out = await api.testTarget(t.id);
      setResult((r) => ({ ...r, [t.id]: out.message }));
    } catch (e) {
      setResult((r) => ({ ...r, [t.id]: `Failed: ${(e as Error).message}` }));
    }
  };
  const remove = async (t: Target) => {
    if (!confirm(`Remove ${t.name}?`)) return;
    await api.deleteTarget(t.id).catch(() => {});
    props.reload();
  };
  return (
    <section class="section">
      <SectionHead index={2} title="Targets">
        <button class="btn btn-small" onClick={() => setEditing({ enabled: true })}>
          <Plus size={14} /> Add target
        </button>
      </SectionHead>
      {props.error && <ErrorNote>{props.error}</ErrorNote>}
      {props.data && props.data.length === 0 && (
        <Empty>
          No targets yet. Add your Telegram bot as <code>{"${TELEGRAM_URL}"}</code> and put{" "}
          <code>TELEGRAM_URL=tgram://…</code> in the stack's .env.
        </Empty>
      )}
      <div class="list">
        {(props.data ?? []).map((t) => (
          <div key={t.id} class="list-row">
            <Dot tone={!t.enabled ? "" : t.missing?.length ? "warn" : "good"} />
            <div class="list-main">
              <span class="list-title">
                {t.name} <span class="chip">{t.scheme}</span>
                {!t.enabled && <span class="chip">off</span>}
              </span>
              <span class="list-sub mono">{t.url}</span>
              {t.missing?.length ? (
                <span class="tone-warn list-sub">{t.missing.join(", ")} isn't set</span>
              ) : null}
              {result[t.id] && <span class="list-sub">{result[t.id]}</span>}
            </div>
            <span class="spacer" />
            <button class="btn btn-small" onClick={() => test(t)}>
              <Send size={13} /> Test
            </button>
            <button class="icon-btn" aria-label="Edit" onClick={() => setEditing(t)}>
              <Pencil size={14} />
            </button>
            <button class="icon-btn" aria-label="Remove" onClick={() => remove(t)}>
              <Trash2 size={14} />
            </button>
          </div>
        ))}
      </div>
      {editing && (
        <TargetForm
          target={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            props.reload();
          }}
        />
      )}
    </section>
  );
}

function TargetForm(props: { target: Partial<Target>; onClose: () => void; onSaved: () => void }) {
  const [t, setT] = useState(props.target);
  const save = useAction();
  const submit = async (e: Event) => {
    e.preventDefault();
    if (await save.run(() => api.saveTarget(t))) props.onSaved();
  };
  return (
    <Dialog
      title={t.id ? `Edit ${props.target.name}` : "Add a target"}
      onClose={props.onClose}
      footer={
        <>
          <button class="btn btn-primary" form="target-form" disabled={save.busy}>
            Save
          </button>
          {save.error && <span class="form-error">{save.error}</span>}
        </>
      }
    >
      <form id="target-form" class="stack-form" onSubmit={submit}>
        <Field label="Name">
          <input
            class="input"
            required
            value={t.name ?? ""}
            onInput={(e) => setT({ ...t, name: e.currentTarget.value })}
          />
        </Field>
        <Field
          label="URL"
          hint={
            t.masked
              ? "Leave as it is to keep the saved URL."
              : "An Apprise URL, or ${VAR} to read it from the environment (recommended: it keeps tokens out of the database and exports)."
          }
        >
          <input
            class="input code"
            required={!t.id}
            placeholder="${TELEGRAM_URL}"
            value={t.url ?? ""}
            onInput={(e) => setT({ ...t, url: e.currentTarget.value })}
          />
        </Field>
        <dl class="examples">
          {EXAMPLES.map(([k, v]) => (
            <div key={k}>
              <dt class="eyebrow">{k}</dt>
              <dd class="mono">{v}</dd>
            </div>
          ))}
        </dl>
        <label class="check">
          <input
            type="checkbox"
            checked={t.enabled ?? true}
            onChange={(e) => setT({ ...t, enabled: e.currentTarget.checked })}
          />
          Enabled
        </label>
      </form>
    </Dialog>
  );
}

const MIN_TYPES = [
  ["", "Everything"],
  ["success", "Recoveries and worse"],
  ["warning", "Warnings and failures"],
  ["failure", "Failures only"],
];

function Routes(props: { targets: Target[] }) {
  const { data, error, reload } = useData(api.routes);
  const [editing, setEditing] = useState<Partial<Route> | null>(null);
  const remove = async (r: Route) => {
    if (!confirm(`Remove the route ${r.name}?`)) return;
    await api.deleteRoute(r.id).catch(() => {});
    reload();
  };
  return (
    <section class="section">
      <SectionHead index={3} title="Routes">
        <button class="btn btn-small" onClick={() => setEditing({ targets: [] })}>
          <Plus size={14} /> Add route
        </button>
      </SectionHead>
      {error && <ErrorNote>{error}</ErrorNote>}
      {data && data.length === 0 && (
        <Empty>No routes: every notification goes to every enabled target, straight away.</Empty>
      )}
      {data && data.length > 0 && (
        <p class="field-hint">
          Checked in order. A notification goes to the targets of every route it matches, up to a
          route marked "stop". Notifications no route matches aren't sent.
        </p>
      )}
      <div class="list">
        {(data ?? []).map((r, i) => (
          <div key={r.id} class="list-row">
            <span class="section-index">{i + 1}</span>
            <div class="list-main">
              <span class="list-title">
                {r.name} {r.digest && <span class="chip chip-accent">digest</span>}
                {r.stop && <span class="chip">stop</span>}
              </span>
              <span class="list-sub">
                {r.tags?.length ? `tagged ${r.tags.join(" or ")}` : "any tag"} ·{" "}
                {MIN_TYPES.find(([k]) => k === (r.min_type ?? ""))?.[1].toLowerCase()} →{" "}
                {r.targets.join(", ")}
              </span>
            </div>
            <span class="spacer" />
            <button class="icon-btn" aria-label="Edit" onClick={() => setEditing(r)}>
              <Pencil size={14} />
            </button>
            <button class="icon-btn" aria-label="Remove" onClick={() => remove(r)}>
              <Trash2 size={14} />
            </button>
          </div>
        ))}
      </div>
      {editing && (
        <RouteForm
          route={editing}
          targets={props.targets}
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

function RouteForm(props: {
  route: Partial<Route>;
  targets: Target[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [r, setR] = useState(props.route);
  const [tags, setTags] = useState((props.route.tags ?? []).join(", "));
  const save = useAction();
  const submit = async (e: Event) => {
    e.preventDefault();
    if (await save.run(() => api.saveRoute({ ...r, tags: splitList(tags) }))) props.onSaved();
  };
  const toggle = (name: string, on: boolean) =>
    setR({
      ...r,
      targets: on ? [...(r.targets ?? []), name] : r.targets!.filter((n) => n !== name),
    });
  return (
    <Dialog
      title={r.id ? `Edit ${props.route.name}` : "Add a route"}
      onClose={props.onClose}
      footer={
        <>
          <button class="btn btn-primary" form="route-form" disabled={save.busy}>
            Save
          </button>
          {save.error && <span class="form-error">{save.error}</span>}
        </>
      }
    >
      <form id="route-form" class="stack-form" onSubmit={submit}>
        <Field label="Name">
          <input
            class="input"
            required
            value={r.name ?? ""}
            onInput={(e) => setR({ ...r, name: e.currentTarget.value })}
          />
        </Field>
        <Field
          label="Tags"
          hint="Any of these; empty matches all. Lookout tags its own: check, heartbeat, speedtest, plus each check's tags; senders add theirs."
        >
          <input class="input" value={tags} onInput={(e) => setTags(e.currentTarget.value)} />
        </Field>
        <Field label="Types">
          <select
            class="input"
            value={r.min_type ?? ""}
            onChange={(e) => setR({ ...r, min_type: e.currentTarget.value })}
          >
            {MIN_TYPES.map(([k, label]) => (
              <option key={k} value={k}>
                {label}
              </option>
            ))}
          </select>
        </Field>
        <div class="field">
          <span class="field-label">Send to</span>
          {props.targets.length === 0 && <span class="field-hint">Add a target first.</span>}
          {props.targets.map((t) => (
            <label key={t.id} class="check">
              <input
                type="checkbox"
                checked={r.targets?.includes(t.name)}
                onChange={(e) => toggle(t.name, e.currentTarget.checked)}
              />
              {t.name}
            </label>
          ))}
        </div>
        <label class="check">
          <input
            type="checkbox"
            checked={!!r.digest}
            onChange={(e) => setR({ ...r, digest: e.currentTarget.checked })}
          />
          Collect into a daily digest instead of sending each one
        </label>
        <label class="check">
          <input
            type="checkbox"
            checked={!!r.stop}
            onChange={(e) => setR({ ...r, stop: e.currentTarget.checked })}
          />
          Stop here: skip the routes after this one when it matches
        </label>
      </form>
    </Dialog>
  );
}

function Senders() {
  const { data, error, reload } = useData(api.senders);
  const [adding, setAdding] = useState(false);
  const remove = async (s: Sender) => {
    if (!confirm(`Remove ${s.name}? Apps posting with its key will get errors.`)) return;
    await api.deleteSender(s.key).catch(() => {});
    reload();
  };
  return (
    <section class="section">
      <SectionHead index={4} title="Senders">
        <button class="btn btn-small" onClick={() => setAdding(true)}>
          <Plus size={14} /> Add sender
        </button>
      </SectionHead>
      <p class="field-hint">
        Each app posts to its own URL, like an Apprise key. Foyer and Hoist send{" "}
        <code>{'{"title", "body", "type"}'}</code> and need nothing else changed.
      </p>
      {error && <ErrorNote>{error}</ErrorNote>}
      {data && data.length === 0 && <Empty>No senders yet.</Empty>}
      <div class="list">
        {(data ?? []).map((s) => (
          <div key={s.key} class="list-row sender-row">
            <div class="list-main">
              <span class="list-title">
                {s.name}
                {s.tags.map((t) => (
                  <span key={t} class="chip">
                    {t}
                  </span>
                ))}
              </span>
              <CopyField value={notifyURL(s.key)} label="Copy notify URL" />
              <span class="list-sub">
                {s.last_used ? `last used ${ago(s.last_used)}` : "not used yet"}
              </span>
            </div>
            <button class="icon-btn" aria-label="Remove" onClick={() => remove(s)}>
              <Trash2 size={14} />
            </button>
          </div>
        ))}
      </div>
      {adding && (
        <SenderForm
          onClose={() => setAdding(false)}
          onSaved={() => {
            setAdding(false);
            reload();
          }}
        />
      )}
    </section>
  );
}

function SenderForm(props: { onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState("");
  const [key, setKey] = useState("");
  const [tags, setTags] = useState("");
  const save = useAction();
  const submit = async (e: Event) => {
    e.preventDefault();
    const out = await save.run(() =>
      api.saveSender({ name, key: key.trim() || undefined, tags: splitList(tags) }),
    );
    if (out) props.onSaved();
  };
  return (
    <Dialog
      title="Add a sender"
      onClose={props.onClose}
      footer={
        <>
          <button class="btn btn-primary" form="sender-form" disabled={save.busy}>
            Add
          </button>
          {save.error && <span class="form-error">{save.error}</span>}
        </>
      }
    >
      <form id="sender-form" class="stack-form" onSubmit={submit}>
        <Field label="Name" hint="Shown as where notifications came from">
          <input
            class="input"
            required
            value={name}
            onInput={(e) => setName(e.currentTarget.value)}
            placeholder="Foyer"
          />
        </Field>
        <Field
          label="Key"
          hint="Empty makes a random one. To switch from apprise-api without touching the app, reuse its key."
        >
          <input class="input code" value={key} onInput={(e) => setKey(e.currentTarget.value)} />
        </Field>
        <Field label="Tags" hint="Added to everything it sends, for routing">
          <input class="input" value={tags} onInput={(e) => setTags(e.currentTarget.value)} />
        </Field>
      </form>
    </Dialog>
  );
}

function Delivery() {
  const { data } = useData(api.settings);
  const [s, setS] = useState<Settings | null>(null);
  const [saved, setSaved] = useState(false);
  const save = useAction();
  useEffect(() => {
    if (data) setS(data);
  }, [data]);
  if (!s) return null;
  const n = s.notify;
  const set = (patch: Partial<Settings["notify"]>) => {
    setS({ ...s, notify: { ...n, ...patch } });
    setSaved(false);
  };
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
      <SectionHead index={5} title="Delivery" />
      <form class="settings-form" onSubmit={submit}>
        <div class="form-grid">
          <Field label="Drop duplicates for" hint="Minutes; 0 is off">
            <input
              class="input"
              type="number"
              min={0}
              value={n.dedupe_minutes}
              onInput={(e) => set({ dedupe_minutes: Number(e.currentTarget.value) })}
            />
          </Field>
          <Field label="Quiet from">
            <input
              class="input"
              type="time"
              value={n.quiet_from}
              onInput={(e) => set({ quiet_from: e.currentTarget.value })}
            />
          </Field>
          <Field label="Quiet until">
            <input
              class="input"
              type="time"
              value={n.quiet_to}
              onInput={(e) => set({ quiet_to: e.currentTarget.value })}
            />
          </Field>
          <Field label="Digest at" hint="When digest routes are sent">
            <input
              class="input"
              type="time"
              value={n.digest_at}
              onInput={(e) => set({ digest_at: e.currentTarget.value })}
            />
          </Field>
        </div>
        <div class="toolbar">
          <label class="check">
            <input
              type="checkbox"
              checked={n.quiet}
              onChange={(e) => set({ quiet: e.currentTarget.checked })}
            />
            Quiet hours: hold everything but failures until they end
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
