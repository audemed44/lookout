import { api } from "../api";
import { useData } from "../hooks";
import { pct, STATUS_LABEL, statusTone, uptimeTone } from "../lib";
import type { PublicStatus } from "../types";
import { UptimeBars } from "./charts";
import { Dot } from "./ui";

/** The public status page: no sign-in, names and uptime only. */
export function StatusPage() {
  const { data, error } = useData(api.publicStatus, 60_000);
  if (error) return <div class="boot">{error}</div>;
  if (!data) return <div class="boot" />;
  const down = data.checks.filter((c) => c.status === "down").length;
  const headline =
    data.status === "up"
      ? "All systems up"
      : down > 0
        ? `${down} ${down === 1 ? "service is" : "services are"} down`
        : "Some services are degraded";
  const groups = new Map<string, PublicStatus["checks"]>();
  for (const c of data.checks) {
    const g = c.group ?? "";
    if (!groups.has(g)) groups.set(g, []);
    groups.get(g)!.push(c);
  }
  return (
    <div class="shell status-shell">
      <header class="topbar">
        <span class="brand">
          <span class="brand-mark" aria-hidden="true" />
          {data.title}
        </span>
      </header>
      <div class="page">
        <header class="page-head">
          <div class="eyebrow eyebrow-accent">
            Updated{" "}
            {new Date(data.updated).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}
          </div>
          <h1 class={`page-title page-title-small tone-${statusTone(data.status)}`}>
            <Dot tone={statusTone(data.status)} />
            {headline}
          </h1>
        </header>
        {[...groups.entries()].map(([group, list]) => (
          <section key={group} class="section">
            {group && (
              <div class="section-head">
                <h2 class="section-name">{group}</h2>
              </div>
            )}
            <div class="status-list">
              {list.map((c) => (
                <div key={c.name} class="status-row">
                  <div class="status-row-head">
                    <Dot tone={statusTone(c.status)} />
                    <span class="check-title">{c.name}</span>
                    <span class="spacer" />
                    <span class={`tone-${statusTone(c.status)}`}>{STATUS_LABEL[c.status]}</span>
                    <span class={`mono tone-${uptimeTone(c.uptime)}`}>{pct(c.uptime)}</span>
                  </div>
                  <UptimeBars
                    cells={c.days.map((v, i) => {
                      const d = new Date();
                      d.setDate(d.getDate() - (c.days.length - 1 - i));
                      return {
                        label: d.toLocaleDateString([], {
                          weekday: "short",
                          day: "numeric",
                          month: "short",
                        }),
                        value: v,
                      };
                    })}
                  />
                </div>
              ))}
            </div>
          </section>
        ))}
        <div class="eyebrow status-foot">
          <span>30 days ago</span>
          <span class="spacer" />
          <span>Today</span>
        </div>
      </div>
    </div>
  );
}
