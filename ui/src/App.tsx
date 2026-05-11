import { Outlet, NavLink, useNavigate } from "react-router-dom";
import { useEffect } from "react";
import { useAuth } from "./stores/auth";
import ThemeToggle from "./components/ThemeToggle";
import { SidebarBrand, SponsoredBy } from "./components/Brand";
import { Activity, Network, ShieldCheck, KeyRound, Settings, Info, LogOut, BookOpen } from "lucide-react";

function prettySubject(s: string | undefined): string {
  if (!s) return "";
  if (s.startsWith("apikey:")) return s.slice("apikey:".length);
  if (/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(s)) return "";
  return s;
}

const NAV: { to: string; label: string; icon: typeof Activity }[] = [
  { to: "/",          label: "Dashboard", icon: Activity },
  { to: "/endpoints", label: "Endpoints", icon: Network },
  { to: "/discovery", label: "Discovery", icon: BookOpen },
  { to: "/audit",     label: "Audit",     icon: ShieldCheck },
  { to: "/keys",      label: "API Keys",  icon: KeyRound },
  { to: "/config",    label: "Config",    icon: Settings },
  { to: "/about",     label: "About",     icon: Info },
];

export default function App() {
  const { identity, status, refresh, signOut } = useAuth();
  const navigate = useNavigate();

  useEffect(() => {
    if (status === "idle") void refresh();
  }, [status, refresh]);

  useEffect(() => {
    if (status === "anonymous") navigate("/login", { replace: true });
  }, [status, navigate]);

  if (status === "idle" || status === "loading") {
    return (
      <div className="grid place-items-center min-h-full bg-background text-muted-foreground">
        Loading…
      </div>
    );
  }

  return (
    // h-full (not min-h-full) so <main> is height-constrained: with
    // min-h-full the outer grid grows with its content and the
    // document does the scrolling, which means main's overflow-auto
    // never fires and position: sticky inside any page has no scroll
    // context to anchor to. h-full pins the chrome to the viewport so
    // main owns the scroll, sticky works, and pages get the
    // h-[calc(100vh-...)] sizing they already assume.
    <div className="grid grid-cols-[14rem_1fr] h-full bg-background text-foreground">
      <aside className="border-r border-border bg-card p-4 flex flex-col overflow-y-auto">
        <SidebarBrand />
        <nav className="flex flex-col gap-1">
          {NAV.map((it) => (
            <NavLink
              key={it.to}
              to={it.to}
              end={it.to === "/"}
              className={({ isActive }) =>
                `flex items-center gap-2 rounded px-2 py-1.5 text-sm transition-colors ${
                  isActive
                    ? "bg-primary text-primary-foreground"
                    : "text-foreground/80 hover:bg-muted hover:text-foreground"
                }`
              }
            >
              <it.icon className="size-4" /> {it.label}
            </NavLink>
          ))}
        </nav>
        <div className="mt-auto space-y-3">
          <ThemeToggle />
          <div className="text-xs text-muted-foreground">
            <div className="mb-2 truncate" title={identity?.subject ?? ""}>
              <div className="font-medium text-foreground truncate">
                {identity?.name || identity?.email || prettySubject(identity?.subject) || "?"}
              </div>
              {identity?.email && identity.name && (
                <div className="truncate">{identity.email}</div>
              )}
              <div>{identity?.auth_type}</div>
            </div>
            <button
              onClick={() => void signOut()}
              className="flex items-center gap-1.5 text-muted-foreground hover:text-foreground"
            >
              <LogOut className="size-3.5" /> Sign out
            </button>
          </div>
          <SponsoredBy />
        </div>
      </aside>
      <main className="p-6 overflow-auto">
        <Outlet />
      </main>
    </div>
  );
}
