export type CheckType = "http" | "tcp" | "ping" | "dns" | "docker" | "tls" | "push";

export type Status =
  "up" | "down" | "pending" | "paused" | "maintenance" | "unknown" | "running" | "asleep";

export interface Check {
  id: number;
  name: string;
  type: CheckType;
  target?: string;
  group?: string;
  description?: string;
  tags?: string[];
  paused?: boolean;
  mute?: boolean;
  interval?: number;
  timeout?: number;
  retries?: number;
  method?: string;
  status?: string;
  keyword?: string;
  invert_keyword?: boolean;
  json_path?: string;
  expect?: string;
  ignore_tls?: boolean;
  cert_days?: number;
  record?: string;
  resolver?: string;
  token?: string;
  period?: number;
  grace?: number;
  source?: string;
  source_key?: string;
}

export interface State {
  status: Status;
  since: string;
  fails: number;
  last?: string;
  latency: number;
  message?: string;
  cert_expires?: string;
  last_ping?: string;
  started?: string;
  flapping?: boolean;
}

export interface View {
  check: Check;
  state: State;
  status: Status;
}

export interface CheckRow extends View {
  /** 24 h, 7 d, 30 d; -1 without data. */
  uptime: number[];
  /** Latest latencies, oldest first; -1 marks a failure. */
  spark: number[];
}

export interface Counts {
  total: number;
  up: number;
  down: number;
  pending: number;
  paused: number;
  other: number;
  cert_days: number;
  cert_check?: string;
}

export interface Incident {
  id: number;
  check_id: number;
  started: string;
  ended?: string;
  cause: string;
}

export interface Speedtest {
  id: number;
  time: string;
  source: string;
  server?: string;
  server_id?: string;
  isp?: string;
  ping: number;
  jitter: number;
  download: number;
  upload: number;
  error?: string;
}

export interface Overview {
  checks: CheckRow[];
  counts: Counts;
  incidents: Incident[];
  speedtest: { latest: Speedtest | null; running: boolean; next?: string };
  queued: number;
}

export interface Result {
  time: string;
  ok: boolean;
  latency: number;
  message?: string;
}

export interface CheckDetail {
  view: View;
  /** 24 h, 7 d, 30 d, 1 y. */
  uptime: number[];
  recent: Result[];
  incidents: Incident[];
}

export interface Point {
  t: string;
  latency: number;
  max: number;
  uptime: number;
  message?: string;
}

export interface Outcome {
  ok: boolean;
  latency: number;
  message: string;
  cert_expires?: string;
}

export interface Target {
  id: number;
  name: string;
  url: string;
  enabled: boolean;
  scheme: string;
  masked: boolean;
  missing?: string[];
}

export interface Route {
  id: number;
  name: string;
  tags?: string[];
  min_type?: string;
  targets: string[];
  digest?: boolean;
  stop?: boolean;
}

export interface Sender {
  key: string;
  name: string;
  tags: string[];
  created: string;
  last_used?: string;
}

export interface Delivery {
  target: string;
  state: "sent" | "failed" | "queued";
  error?: string;
  reason?: string;
  until?: string;
}

export interface Notification {
  id: number;
  time: string;
  source: string;
  type: string;
  title: string;
  body: string;
  tags: string[];
  status: string;
  detail?: string;
}

export interface Window {
  id: number;
  name: string;
  enabled: boolean;
  checks?: string[];
  tags?: string[];
  start?: string;
  end?: string;
  repeat?: "" | "daily" | "weekly";
  weekdays?: number[];
  from?: string;
  minutes?: number;
  active?: boolean;
}

export interface Settings {
  notify: {
    dedupe_minutes: number;
    quiet: boolean;
    quiet_from: string;
    quiet_to: string;
    digest_at: string;
  };
  speedtest: {
    enabled: boolean;
    schedule: string;
    server_id?: string;
    min_download?: number;
    min_upload?: number;
    max_ping?: number;
  };
  discovery: {
    auto: boolean;
    every: number;
    tags?: string[];
    interval: number;
    ignored?: string[];
  };
  status_page: { enabled: boolean; title?: string; tags?: string[] };
  retention: { raw_hours: number; rollup_days: number; notification_days: number };
}

export interface DiscoveryResult {
  time: string;
  routes: number;
  added: string[];
  updated: string[];
  missing: string[];
  error?: string;
}

export interface DiscoveryInfo {
  configured: boolean;
  source?: string;
  last?: DiscoveryResult;
}

export interface Session {
  authenticated: boolean;
  /** Foyer, the homelab's start page (HOMEPAGE_URL). */
  foyer_url?: string;
}

export interface PublicStatus {
  title: string;
  status: Status;
  updated: string;
  checks: {
    name: string;
    group?: string;
    status: Status;
    since: string;
    uptime: number;
    days: number[];
  }[];
}
