import { useQuery } from "@tanstack/react-query";
import { portalAPI, type AuditEvent } from "@/lib/api";
import { Link } from "react-router-dom";

export default function Dashboard() {
  const q = useQuery({ queryKey: ["dashboard"], queryFn: portalAPI.dashboard, refetchInterval: 5_000 });

  if (q.isLoading) return <div className="text-muted-foreground">Loading…</div>;
  if (q.error) return <div className="text-destructive">Failed to load dashboard.</div>;
  const d = q.data!;
  const errorCount = Number(d.total) - Number(d.success_count);
  const errorRate = d.total > 0 ? errorCount / d.total : 0;

  return (
    <div className="space-y-6 max-w-5xl">
      <div className="flex items-baseline justify-between">
        <h1 className="text-2xl font-semibold">Dashboard</h1>
        <div className="text-xs text-muted-foreground">
          last 1h · {new Date(d.window_to).toLocaleTimeString()}
        </div>
      </div>

      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        <Stat label="Total calls" value={d.total} />
        <Stat label="Successes"   value={d.success_count} />
        <Stat label="Errors"      value={errorCount} accent={errorCount > 0 ? "danger" : undefined} />
        <Stat label="Error rate"  value={`${(errorRate * 100).toFixed(1)}%`} />
      </div>

      <div>
        <div className="flex items-baseline justify-between mb-2">
          <h2 className="text-lg font-medium">Recent activity</h2>
          <Link to="/audit" className="text-sm text-muted-foreground hover:text-foreground">View all →</Link>
        </div>
        <div className="bg-card text-card-foreground border border-border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-muted-foreground">
              <tr>
                <th className="text-left px-3 py-2 font-medium">Time</th>
                <th className="text-left px-3 py-2 font-medium">Method</th>
                <th className="text-left px-3 py-2 font-medium">Path</th>
                <th className="text-left px-3 py-2 font-medium">User</th>
                <th className="text-right px-3 py-2 font-medium">Status</th>
                <th className="text-right px-3 py-2 font-medium">ms</th>
              </tr>
            </thead>
            <tbody>
              {(d.recent ?? []).map((e) => (
                <tr key={e.id} className="border-t border-border">
                  <td className="px-3 py-1.5 text-muted-foreground mono">{new Date(e.timestamp).toLocaleTimeString()}</td>
                  <td className="px-3 py-1.5 mono">{e.method}</td>
                  <td className="px-3 py-1.5 mono truncate max-w-xs">{e.path}</td>
                  <td className="px-3 py-1.5 text-muted-foreground" title={e.user_subject || ""}>
                    {displayUser(e)}
                  </td>
                  <td className="px-3 py-1.5 text-right mono">
                    <span className={statusColor(e.status)}>{e.status}</span>
                  </td>
                  <td className="px-3 py-1.5 text-right mono">{e.duration_ms}</td>
                </tr>
              ))}
              {(!d.recent || d.recent.length === 0) && (
                <tr><td colSpan={6} className="px-3 py-6 text-muted-foreground text-center">No events in the last hour.</td></tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}

function Stat({ label, value, accent }: { label: string; value: number | string; accent?: "danger" }) {
  return (
    <div className="bg-card text-card-foreground border border-border rounded-lg p-3">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className={`text-2xl font-semibold ${accent === "danger" ? "text-destructive" : ""}`}>{value}</div>
    </div>
  );
}

function displayUser(e: AuditEvent): string {
  if (e.user_email) return e.user_email;
  if (e.api_key_name) return e.api_key_name;
  const sub = e.user_subject ?? "";
  return sub || "-";
}

function statusColor(status: number): string {
  if (status >= 500) return "text-destructive";
  if (status >= 400) return "text-destructive/80";
  if (status >= 300) return "text-muted-foreground";
  return "text-success";
}
