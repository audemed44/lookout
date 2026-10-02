import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { ms, splitList, TYPE_LABEL } from "../lib";
import type { Check, CheckType, Outcome } from "../types";
import { Dialog, Field, useAction } from "./ui";

const TYPES: CheckType[] = ["http", "tcp", "ping", "dns", "docker", "tls", "push"];

const TARGET: Record<CheckType, { label: string; placeholder: string; hint?: string }> = {
  http: { label: "URL", placeholder: "https://books.example.com" },
  tcp: { label: "Host and port", placeholder: "nas.lan:22" },
  ping: { label: "Host", placeholder: "192.168.1.1" },
  dns: { label: "Name", placeholder: "example.com" },
  docker: { label: "Container", placeholder: "shelfloom", hint: "Its state and healthcheck" },
  tls: { label: "Host", placeholder: "mail.example.com:993", hint: "Port 443 unless given" },
  push: { label: "", placeholder: "" },
};

/** Whole seconds shown in the unit that reads best. */
type Unit = "s" | "min" | "h";
const UNIT_S: Record<Unit, number> = { s: 1, min: 60, h: 3600 };
function bestUnit(s: number): Unit {
  if (s && s % 3600 === 0) return "h";
  if (s && s % 60 === 0) return "min";
  return "s";
}

function Duration(props: { value: number; onChange: (s: number) => void; units?: Unit[] }) {
  const [unit, setUnit] = useState<Unit>(bestUnit(props.value));
  return (
    <div class="duration">
      <input
        class="input"
        type="number"
        min={1}
        value={props.value ? props.value / UNIT_S[unit] : ""}
        onInput={(e) => props.onChange(Math.round(Number(e.currentTarget.value) * UNIT_S[unit]))}
      />
      <select
        class="input"
        value={unit}
        onChange={(e) => {
          const u = e.currentTarget.value as Unit;
          setUnit(u);
        }}
      >
        {(props.units ?? ["s", "min", "h"]).map((u) => (
          <option key={u} value={u}>
            {u}
          </option>
        ))}
      </select>
    </div>
  );
}

export function CheckForm(props: {
  check?: Check;
  type?: CheckType;
  onClose: () => void;
  onSaved: (c: Check) => void;
}) {
  const editing = !!props.check;
  const [c, setC] = useState<Partial<Check>>(
    () =>
      props.check ?? {
        type: props.type ?? "http",
        interval: 60,
        timeout: 10,
        retries: 1,
        period: 86400,
        grace: 3600,
        cert_days: 14,
      },
  );
  const [tags, setTags] = useState((props.check?.tags ?? []).join(", "));
  const [containers, setContainers] = useState<string[]>([]);
  const [test, setTest] = useState<Outcome | null>(null);
  const save = useAction();
  const tester = useAction();
  const set = (patch: Partial<Check>) => {
    setC((prev) => ({ ...prev, ...patch }));
    setTest(null);
  };
  const type = c.type ?? "http";

  useEffect(() => {
    if (type === "docker" && !containers.length) api.containers().then(setContainers, () => {});
  }, [type]);

  const body = (): Partial<Check> => ({ ...c, tags: splitList(tags) });
  const submit = async (e: Event) => {
    e.preventDefault();
    const out = await save.run(() => (editing ? api.updateCheck(body()) : api.createCheck(body())));
    if (out) props.onSaved(out.check);
  };
  const runTest = async () => {
    setTest(null);
    const out = await tester.run(() => api.testCheck(body()));
    if (out) setTest(out);
  };

  return (
    <Dialog
      title={editing ? `Edit ${props.check!.name}` : "Add a check"}
      onClose={props.onClose}
      wide
      footer={
        <>
          <button class="btn btn-primary" form="check-form" disabled={save.busy}>
            {editing ? "Save" : "Add check"}
          </button>
          {type !== "push" && (
            <button class="btn" type="button" onClick={runTest} disabled={tester.busy}>
              {tester.busy ? "Testing…" : "Test"}
            </button>
          )}
          <span class="spacer" />
          {test && (
            <span class={test.ok ? "tone-good" : "tone-bad"}>
              {test.ok ? "OK" : "Failed"} · {test.message}
              {test.ok && <span class="muted"> · {ms(test.latency)}</span>}
            </span>
          )}
          {tester.error && <span class="form-error">{tester.error}</span>}
        </>
      }
    >
      <form id="check-form" class="check-form" onSubmit={submit}>
        {!editing && (
          <div class="type-picker" role="radiogroup" aria-label="Type">
            {TYPES.map((t) => (
              <button
                key={t}
                type="button"
                role="radio"
                aria-checked={type === t}
                class={`chip chip-pick ${type === t ? "chip-accent" : ""}`}
                onClick={() => set({ type: t })}
              >
                {TYPE_LABEL[t]}
              </button>
            ))}
          </div>
        )}
        <div class="form-grid">
          <Field label="Name">
            <input
              class="input"
              required
              value={c.name ?? ""}
              onInput={(e) => set({ name: e.currentTarget.value })}
              autofocus={!editing}
            />
          </Field>
          {type !== "push" && (
            <Field label={TARGET[type].label} hint={TARGET[type].hint} class="span-2">
              <input
                class="input code"
                required
                list={type === "docker" ? "containers" : undefined}
                placeholder={TARGET[type].placeholder}
                value={c.target ?? ""}
                onInput={(e) => set({ target: e.currentTarget.value })}
              />
              {type === "docker" && (
                <datalist id="containers">
                  {containers.map((n) => (
                    <option key={n} value={n} />
                  ))}
                </datalist>
              )}
            </Field>
          )}

          {type === "http" && (
            <>
              <Field label="Method">
                <select
                  class="input"
                  value={c.method ?? "GET"}
                  onChange={(e) => set({ method: e.currentTarget.value })}
                >
                  {["GET", "HEAD", "POST", "OPTIONS"].map((m) => (
                    <option key={m}>{m}</option>
                  ))}
                </select>
              </Field>
              <Field label="Accepted status" hint="e.g. 200-299, 401">
                <input
                  class="input code"
                  placeholder="200-299"
                  value={c.status ?? ""}
                  onInput={(e) => set({ status: e.currentTarget.value })}
                />
              </Field>
              <Field label="Keyword" hint="Text the page must contain">
                <input
                  class="input"
                  value={c.keyword ?? ""}
                  onInput={(e) => set({ keyword: e.currentTarget.value })}
                />
              </Field>
              <Field label="JSON path" hint="e.g. status or data.items[0].ok">
                <input
                  class="input code"
                  value={c.json_path ?? ""}
                  onInput={(e) => set({ json_path: e.currentTarget.value })}
                />
              </Field>
              <Field label="Expected value">
                <input
                  class="input code"
                  value={c.expect ?? ""}
                  disabled={!c.json_path}
                  onInput={(e) => set({ expect: e.currentTarget.value })}
                />
              </Field>
            </>
          )}

          {type === "dns" && (
            <>
              <Field label="Record">
                <select
                  class="input"
                  value={c.record ?? "A"}
                  onChange={(e) => set({ record: e.currentTarget.value })}
                >
                  {["A", "AAAA", "CNAME", "MX", "TXT", "NS"].map((r) => (
                    <option key={r}>{r}</option>
                  ))}
                </select>
              </Field>
              <Field label="Resolver" hint="Empty uses the container's">
                <input
                  class="input code"
                  placeholder="1.1.1.1"
                  value={c.resolver ?? ""}
                  onInput={(e) => set({ resolver: e.currentTarget.value })}
                />
              </Field>
              <Field label="Expected answer" hint="One of the answers must equal this">
                <input
                  class="input code"
                  value={c.expect ?? ""}
                  onInput={(e) => set({ expect: e.currentTarget.value })}
                />
              </Field>
            </>
          )}

          {type === "push" ? (
            <>
              <Field label="Expected every" hint="How often the job runs">
                <Duration
                  value={c.period ?? 0}
                  units={["min", "h"]}
                  onChange={(v) => set({ period: v })}
                />
              </Field>
              <Field label="Grace" hint="Slack before it counts as late">
                <Duration
                  value={c.grace ?? 0}
                  units={["min", "h"]}
                  onChange={(v) => set({ grace: v })}
                />
              </Field>
            </>
          ) : (
            <>
              <Field label="Every">
                <Duration value={c.interval ?? 60} onChange={(v) => set({ interval: v })} />
              </Field>
              <Field label="Timeout">
                <Duration
                  value={c.timeout ?? 10}
                  units={["s"]}
                  onChange={(v) => set({ timeout: v })}
                />
              </Field>
            </>
          )}
          <Field label="Retries" hint="Failures in a row before it's down">
            <input
              class="input"
              type="number"
              min={0}
              max={20}
              value={c.retries ?? 0}
              onInput={(e) => set({ retries: Number(e.currentTarget.value) })}
            />
          </Field>
          {(type === "http" || type === "tls") && (
            <Field label="Certificate warning" hint="Days before expiry; -1 turns it off">
              <input
                class="input"
                type="number"
                min={-1}
                value={c.cert_days ?? 14}
                onInput={(e) => set({ cert_days: Number(e.currentTarget.value) })}
              />
            </Field>
          )}
          <Field label="Group" hint="Checks are listed by group">
            <input
              class="input"
              value={c.group ?? ""}
              onInput={(e) => set({ group: e.currentTarget.value })}
            />
          </Field>
          <Field label="Tags" hint="For routing notifications and maintenance">
            <input class="input" value={tags} onInput={(e) => setTags(e.currentTarget.value)} />
          </Field>
        </div>
        <div class="checks-row">
          {type === "http" && c.keyword && (
            <label class="check">
              <input
                type="checkbox"
                checked={!!c.invert_keyword}
                onChange={(e) => set({ invert_keyword: e.currentTarget.checked })}
              />
              Fail when the keyword is found
            </label>
          )}
          {(type === "http" || type === "tls") && (
            <label class="check">
              <input
                type="checkbox"
                checked={!!c.ignore_tls}
                onChange={(e) => set({ ignore_tls: e.currentTarget.checked })}
              />
              Accept invalid certificates
            </label>
          )}
          <label class="check">
            <input
              type="checkbox"
              checked={!!c.mute}
              onChange={(e) => set({ mute: e.currentTarget.checked })}
            />
            Don't notify
          </label>
        </div>
        {save.error && <div class="form-error">{save.error}</div>}
      </form>
    </Dialog>
  );
}
