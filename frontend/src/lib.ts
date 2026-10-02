import type { Check, CheckType, Delivery, Notification, Status, View } from "./types";

export type Tone = "good" | "warn" | "bad" | "accent" | "";

export function ago(iso: string | undefined, now = Date.now()): string {
  if (!iso) return "";
  const s = Math.max(0, (now - new Date(iso).getTime()) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 48 * 3600) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

/** "in 3h", "in 12m"; for times ahead. */
export function until(iso: string | undefined, now = Date.now()): string {
  if (!iso) return "";
  const s = Math.max(0, (new Date(iso).getTime() - now) / 1000);
  if (s < 60) return "in a moment";
  if (s < 3600) return `in ${Math.round(s / 60)}m`;
  if (s < 48 * 3600) return `in ${Math.round(s / 3600)}h`;
  return `in ${Math.round(s / 86400)}d`;
}

/** A span in seconds as "45 s", "12 min", "3.5 h", "4 days". */
export function span(seconds: number): string {
  if (seconds < 60) return `${Math.round(seconds)} s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)} min`;
  if (seconds < 48 * 3600) return `${(seconds / 3600).toFixed(1).replace(/\.0$/, "")} h`;
  return `${Math.floor(seconds / 86400)} days`;
}

export function since(iso: string | undefined, now = Date.now()): number {
  return iso ? Math.max(0, (now - new Date(iso).getTime()) / 1000) : 0;
}

/** Latency in ms as "84 ms", "1.2 s". */
export function ms(v: number): string {
  if (!v || v < 0) return "—";
  if (v < 10) return `${v.toFixed(1)} ms`;
  if (v < 1000) return `${Math.round(v)} ms`;
  return `${(v / 1000).toFixed(1)} s`;
}

export function pct(v: number | undefined): string {
  if (v === undefined || v < 0) return "—";
  if (v === 100) return "100%";
  if (v >= 99.9) return `${v.toFixed(2)}%`;
  return `${v.toFixed(1)}%`;
}

export function uptimeTone(v: number | undefined): Tone {
  if (v === undefined || v < 0) return "";
  if (v >= 99.5) return "good";
  if (v >= 95) return "warn";
  return "bad";
}

export function statusTone(s: Status): Tone {
  switch (s) {
    case "up":
      return "good";
    case "running":
    case "asleep":
      return "accent";
    case "down":
      return "bad";
    case "pending":
      return "warn";
  }
  return "";
}

export const STATUS_LABEL: Record<Status, string> = {
  up: "Up",
  down: "Down",
  pending: "Retrying",
  paused: "Paused",
  maintenance: "Maintenance",
  unknown: "Waiting",
  running: "Running",
  asleep: "Asleep",
};

export const TYPE_LABEL: Record<CheckType, string> = {
  http: "HTTP",
  tcp: "TCP port",
  ping: "Ping",
  dns: "DNS",
  docker: "Container",
  tls: "TLS certificate",
  push: "Heartbeat",
};

/** Days until an ISO time; negative once past. */
export function daysLeft(iso: string | undefined, now = Date.now()): number | null {
  if (!iso) return null;
  return Math.floor((new Date(iso).getTime() - now) / 86_400_000);
}

export function certTone(days: number | null, warn = 14): Tone {
  if (days === null) return "";
  if (days <= 7) return "bad";
  if (days <= warn) return "warn";
  return "";
}

export function pingURL(token: string | undefined): string {
  return `${window.location.origin}/ping/${token ?? ""}`;
}

export function notifyURL(key: string): string {
  return `${window.location.origin}/notify/${key}`;
}

/** What a check watches, in one line. */
export function targetText(c: Check): string {
  switch (c.type) {
    case "push":
      return `every ${span(c.period ?? 0)}`;
    case "dns":
      return `${c.record ?? "A"} ${c.target ?? ""}`;
    default:
      return c.target ?? "";
  }
}

/** Heartbeats are late after period + grace. */
export function heartbeatDue(v: View): string | undefined {
  const ref = v.state.last_ping ?? v.state.since;
  if (!ref || v.check.type !== "push") return undefined;
  const ms = new Date(ref).getTime() + ((v.check.period ?? 0) + (v.check.grace ?? 0)) * 1000;
  return new Date(ms).toISOString();
}

export function deliveries(n: Notification): Delivery[] {
  if (!n.detail?.startsWith("[")) return [];
  try {
    return JSON.parse(n.detail) as Delivery[];
  } catch {
    return [];
  }
}

export const NOTE_STATUS: Record<string, { label: string; tone: Tone }> = {
  sent: { label: "Sent", tone: "good" },
  failed: { label: "Failed", tone: "bad" },
  partial: { label: "Partly sent", tone: "warn" },
  queued: { label: "Held", tone: "accent" },
  suppressed: { label: "Duplicate", tone: "" },
  no_route: { label: "No route", tone: "warn" },
};

export const TYPE_TONE: Record<string, Tone> = {
  failure: "bad",
  warning: "warn",
  success: "good",
  info: "accent",
};

export function splitList(s: string): string[] {
  return s
    .split(/[,\s]+/)
    .map((x) => x.trim())
    .filter(Boolean);
}

export function mbps(v: number): string {
  return v >= 100 ? v.toFixed(0) : v.toFixed(1);
}
