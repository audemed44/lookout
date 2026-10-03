import { ArrowLeft, LogOut } from "lucide-preact";
import { useEffect, useState } from "preact/hooks";
import { api, setUnauthorizedHandler } from "./api";
import { CheckPage } from "./components/CheckPage";
import { HeartbeatsPage } from "./components/HeartbeatsPage";
import { Login } from "./components/Login";
import { NotificationsPage } from "./components/NotificationsPage";
import { OverviewPage } from "./components/OverviewPage";
import { SettingsPage } from "./components/SettingsPage";
import { SpeedtestPage } from "./components/SpeedtestPage";
import { StatusPage } from "./components/StatusPage";
import { onLinkClick, useRoute, type Route } from "./router";
import type { Session } from "./types";

export function App() {
  const route = useRoute();
  if (route.page === "status") return <StatusPage />;
  return <Signed route={route} />;
}

function Signed(props: { route: Route }) {
  const [session, setSession] = useState<Session | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    setUnauthorizedHandler(() => setSession({ authenticated: false }));
    api
      .session()
      .then(setSession)
      .catch((e: Error) => setError(e.message));
  }, []);

  if (error) return <div class="boot">Can't reach Lookout: {error}</div>;
  if (!session) return <div class="boot" />;
  if (!session.authenticated) return <Login onDone={setSession} />;
  return (
    <Shell
      foyerURL={session.foyer_url}
      route={props.route}
      onSignOut={() => setSession({ authenticated: false })}
    />
  );
}

const NAV: { page: Route["page"]; href: string; label: string }[] = [
  { page: "home", href: "/", label: "Checks" },
  { page: "heartbeats", href: "/heartbeats", label: "Heartbeats" },
  { page: "speedtests", href: "/speedtests", label: "Speed" },
  { page: "notifications", href: "/notifications", label: "Notify" },
  { page: "settings", href: "/settings", label: "Settings" },
];

function Shell(props: { route: Route; foyerURL?: string; onSignOut: () => void }) {
  const { route } = props;
  const signOut = async () => {
    await api.logout().catch(() => {});
    props.onSignOut();
  };
  const current = route.page === "check" ? "home" : route.page;
  return (
    <div class="shell" onClick={onLinkClick}>
      <header class="topbar">
        {props.foyerURL && (
          <a class="home-link" href={props.foyerURL} title="Back to Foyer">
            <ArrowLeft size={14} />
            <span class="home-link-text">Foyer</span>
          </a>
        )}
        <a class="brand" href="/">
          <span class="brand-mark" aria-hidden="true" />
          Lookout
        </a>
        <span class="spacer" />
        <nav class="topnav" aria-label="Pages">
          {NAV.map((n) => (
            <a key={n.page} class={current === n.page ? "active" : ""} href={n.href}>
              {n.label}
            </a>
          ))}
        </nav>
        <button class="icon-btn" onClick={signOut} title="Sign out" aria-label="Sign out">
          <LogOut size={16} />
        </button>
      </header>
      <main>
        {route.page === "home" && <OverviewPage />}
        {route.page === "check" && <CheckPage key={route.id} id={route.id} />}
        {route.page === "heartbeats" && <HeartbeatsPage />}
        {route.page === "speedtests" && <SpeedtestPage />}
        {route.page === "notifications" && <NotificationsPage />}
        {route.page === "settings" && <SettingsPage />}
      </main>
    </div>
  );
}
