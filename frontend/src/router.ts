import { useEffect, useState } from "preact/hooks";

/**
 * Path routes (the server answers every non-API path with the app):
 *   /                    overview: every check
 *   /checks/<id>         one check: latency, uptime, incidents
 *   /heartbeats          push checks and their ping URLs
 *   /speedtests          results and charts
 *   /notifications       history, targets, routes, senders
 *   /settings            maintenance, discovery, imports, configuration
 *   /status              the public status page (no sign-in)
 */
export type Route =
  | { page: "home" }
  | { page: "check"; id: number }
  | { page: "heartbeats" }
  | { page: "speedtests" }
  | { page: "notifications" }
  | { page: "settings" }
  | { page: "status" };

export function parseRoute(path: string): Route {
  const parts = path.split("/").filter(Boolean);
  if (parts[0] === "checks" && /^\d+$/.test(parts[1] ?? "")) {
    return { page: "check", id: Number(parts[1]) };
  }
  switch (parts[0]) {
    case "heartbeats":
    case "speedtests":
    case "notifications":
    case "settings":
    case "status":
      return { page: parts[0] };
  }
  return { page: "home" };
}

const listeners = new Set<() => void>();

export function navigate(url: string, replace = false) {
  if (url === window.location.pathname) return;
  if (replace) history.replaceState(null, "", url);
  else history.pushState(null, "", url);
  window.scrollTo(0, 0);
  listeners.forEach((fn) => fn());
}

/** Lets plain <a href="/…"> links navigate without a page load. */
export function onLinkClick(e: MouseEvent) {
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) {
    return;
  }
  const a = (e.target as Element).closest("a");
  if (
    !a ||
    a.target ||
    a.hasAttribute("download") ||
    a.origin !== window.location.origin ||
    a.pathname.startsWith("/api/")
  ) {
    return;
  }
  e.preventDefault();
  navigate(a.pathname);
}

export function useRoute(): Route {
  const [route, setRoute] = useState(() => parseRoute(window.location.pathname));
  useEffect(() => {
    const update = () => setRoute(parseRoute(window.location.pathname));
    listeners.add(update);
    window.addEventListener("popstate", update);
    return () => {
      listeners.delete(update);
      window.removeEventListener("popstate", update);
    };
  }, []);
  return route;
}
