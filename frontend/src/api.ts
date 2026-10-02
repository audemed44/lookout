import type {
  Check,
  CheckDetail,
  CheckRow,
  DiscoveryInfo,
  DiscoveryResult,
  Notification,
  Outcome,
  Overview,
  Point,
  PublicStatus,
  Route,
  Sender,
  Session,
  Settings,
  Speedtest,
  Target,
  View,
  Window,
} from "./types";

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

/** Called when the session has expired, so the app can show the sign-in. */
let onUnauthorized = () => {};
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, { credentials: "same-origin", ...init });
  if (!res.ok) {
    let message = `HTTP ${res.status}`;
    try {
      message = (await res.json()).error ?? message;
    } catch {
      // not JSON
    }
    if (res.status === 401 && !path.startsWith("/api/session")) onUnauthorized();
    throw new ApiError(message, res.status);
  }
  if (res.status === 204) return undefined as T;
  const type = res.headers.get("Content-Type") ?? "";
  return (type.includes("json") ? res.json() : res.text()) as Promise<T>;
}

const json = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

const upload = (file: File, extra: Record<string, string> = {}): RequestInit => {
  const form = new FormData();
  form.set("file", file);
  for (const [k, v] of Object.entries(extra)) form.set(k, v);
  return { method: "POST", body: form };
};

export interface KumaReport {
  imported: string[];
  existing: string[];
  skipped: string[];
  hours: number;
}

export interface ImportReport {
  added: string[];
  updated: string[];
  removed: string[];
  warnings: string[];
}

export const api = {
  session: () => request<Session>("/api/session"),
  login: (token: string) => request<Session>("/api/session", json("POST", { token })),
  logout: () => request<void>("/api/session", { method: "DELETE" }),

  overview: () => request<Overview>("/api/overview"),
  checks: () => request<CheckRow[]>("/api/checks"),
  check: (id: number) => request<CheckDetail>(`/api/checks/${id}`),
  history: (id: number, range: string) =>
    request<{ range: string; points: Point[] }>(`/api/checks/${id}/history?range=${range}`),
  createCheck: (c: Partial<Check>) => request<View>("/api/checks", json("POST", c)),
  updateCheck: (c: Partial<Check>) => request<View>(`/api/checks/${c.id}`, json("PUT", c)),
  deleteCheck: (id: number) => request<void>(`/api/checks/${id}`, { method: "DELETE" }),
  runCheck: (id: number) => request<Outcome>(`/api/checks/${id}/run`, { method: "POST" }),
  testCheck: (c: Partial<Check>) => request<Outcome>("/api/checks/test", json("POST", c)),
  pauseCheck: (id: number, paused: boolean) =>
    request<View>(`/api/checks/${id}/pause`, json("POST", { paused })),
  containers: () => request<string[]>("/api/containers"),

  windows: () => request<Window[]>("/api/maintenance"),
  saveWindow: (w: Partial<Window>) =>
    w.id
      ? request<Window>(`/api/maintenance/${w.id}`, json("PUT", w))
      : request<Window>("/api/maintenance", json("POST", w)),
  deleteWindow: (id: number) => request<void>(`/api/maintenance/${id}`, { method: "DELETE" }),

  notifications: (before = 0) =>
    request<{ items: Notification[]; queued: number }>(
      `/api/notifications?limit=50${before ? `&before=${before}` : ""}`,
    ),
  flush: () => request<void>("/api/notifications/flush", { method: "POST" }),
  targets: () => request<Target[]>("/api/targets"),
  saveTarget: (t: Partial<Target>) =>
    t.id
      ? request<Target>(`/api/targets/${t.id}`, json("PUT", t))
      : request<Target>("/api/targets", json("POST", t)),
  deleteTarget: (id: number) => request<void>(`/api/targets/${id}`, { method: "DELETE" }),
  testTarget: (id: number) =>
    request<{ message: string }>(`/api/targets/${id}/test`, { method: "POST" }),
  routes: () => request<Route[]>("/api/routes"),
  saveRoute: (r: Partial<Route>) =>
    r.id
      ? request<Route>(`/api/routes/${r.id}`, json("PUT", r))
      : request<Route>("/api/routes", json("POST", r)),
  deleteRoute: (id: number) => request<void>(`/api/routes/${id}`, { method: "DELETE" }),
  senders: () => request<Sender[]>("/api/senders"),
  saveSender: (s: Partial<Sender>) => request<Sender>("/api/senders", json("POST", s)),
  deleteSender: (key: string) =>
    request<void>(`/api/senders/${encodeURIComponent(key)}`, { method: "DELETE" }),

  speedtests: (range: string) =>
    request<{ results: Speedtest[]; running: boolean; started?: string; next?: string }>(
      `/api/speedtests?range=${range}`,
    ),
  runSpeedtest: () => request<{ message: string }>("/api/speedtests/run", { method: "POST" }),
  deleteSpeedtest: (id: number) => request<void>(`/api/speedtests/${id}`, { method: "DELETE" }),

  settings: () => request<Settings>("/api/settings"),
  saveSettings: (s: Settings) => request<Settings>("/api/settings", json("PUT", s)),
  discovery: () => request<DiscoveryInfo>("/api/discovery"),
  sync: () => request<DiscoveryResult>("/api/discovery/sync", { method: "POST" }),
  ignore: (domain: string, ignore: boolean) =>
    request<Settings>("/api/discovery/ignore", json("POST", { domain, ignore })),
  importKuma: (file: File, history: boolean) =>
    request<KumaReport>("/api/import/kuma", upload(file, { history: String(history) })),
  importSpeedtests: (file: File) =>
    request<{ imported: number; existing: number; skipped: number }>(
      "/api/import/speedtest-tracker",
      upload(file),
    ),
  importConfig: (yaml: string, replace: boolean) =>
    request<ImportReport>(`/api/config?replace=${replace}`, {
      method: "POST",
      headers: { "Content-Type": "application/yaml" },
      body: yaml,
    }),

  publicStatus: () => request<PublicStatus>("/api/public/status"),
};
