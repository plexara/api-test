import { useQuery } from "@tanstack/react-query";
import { portalAPI, type EndpointMeta } from "@/lib/api";
import { useParams, Link } from "react-router-dom";

export default function Endpoints() {
  const q = useQuery({ queryKey: ["endpoints"], queryFn: portalAPI.endpoints });
  const { name } = useParams<{ name?: string }>();

  if (q.isLoading) return <div className="text-muted-foreground">Loading…</div>;
  if (q.error) return <div className="text-destructive">Failed to load endpoints.</div>;
  const all = q.data?.endpoints ?? [];
  const selected = name ? all.find((e) => e.name === name) : null;

  return (
    <div className="space-y-6 max-w-6xl">
      <div className="flex items-baseline justify-between">
        <h1 className="text-2xl font-semibold">Endpoints</h1>
        <div className="text-xs text-muted-foreground">{all.length} registered</div>
      </div>

      <div className="grid grid-cols-[2fr_3fr] gap-6">
        <div className="bg-card text-card-foreground border border-border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-muted-foreground">
              <tr>
                <th className="text-left px-3 py-2 font-medium">Method</th>
                <th className="text-left px-3 py-2 font-medium">Path</th>
                <th className="text-left px-3 py-2 font-medium">Group</th>
              </tr>
            </thead>
            <tbody>
              {all.map((e) => (
                <tr key={e.name} className={`border-t border-border ${selected?.name === e.name ? "bg-muted/40" : ""}`}>
                  <td className="px-3 py-1.5 mono">
                    <Link to={`/endpoints/${encodeURIComponent(e.name)}`} className="hover:underline">{e.method}</Link>
                  </td>
                  <td className="px-3 py-1.5 mono">
                    <Link to={`/endpoints/${encodeURIComponent(e.name)}`} className="hover:underline">{e.path}</Link>
                  </td>
                  <td className="px-3 py-1.5 text-muted-foreground">{e.group}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <div>
          {selected ? <EndpointDetail e={selected} /> : (
            <div className="text-muted-foreground text-sm">Select an endpoint to view details.</div>
          )}
        </div>
      </div>
    </div>
  );
}

function EndpointDetail({ e }: { e: EndpointMeta }) {
  return (
    <div className="bg-card text-card-foreground border border-border rounded-lg p-4 space-y-3">
      <div>
        <div className="text-xs text-muted-foreground">Endpoint</div>
        <div className="text-lg font-semibold">{e.name}</div>
      </div>
      <div className="grid grid-cols-2 gap-3 text-sm">
        <Field label="Method" value={e.method} />
        <Field label="Group" value={e.group} />
        <Field label="Path" value={e.path} mono />
        <Field label="Auth required" value={e.auth_required ? "yes" : "no"} />
      </div>
      {e.description && (
        <div>
          <div className="text-xs text-muted-foreground mb-1">Description</div>
          <div className="text-sm whitespace-pre-wrap">{e.description}</div>
        </div>
      )}
      <div className="text-xs text-muted-foreground pt-2 border-t border-border">
        Try-It panel arrives in M4 (OpenAPI generator). For now, invoke directly:
        <pre className="mono mt-1 bg-muted/30 p-2 rounded">curl -H "X-API-Key: $KEY" http://localhost:8080{e.path}</pre>
      </div>
    </div>
  );
}

function Field({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className={mono ? "mono" : ""}>{value}</div>
    </div>
  );
}
