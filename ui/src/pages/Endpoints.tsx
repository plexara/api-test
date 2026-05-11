import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { portalAPI, type EndpointMeta, type TryItResponse } from "@/lib/api";
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
          {selected ? (
            // key forces a remount when the user switches endpoints so
            // the Try-It form's local state (path params, body, response)
            // doesn't bleed across endpoints — e.g. a leftover POST body
            // getting dispatched to a newly-selected GET endpoint.
            <EndpointDetail key={selected.name} e={selected} />
          ) : (
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
      <div className="pt-2 border-t border-border">
        <TryItPanel e={e} />
      </div>
    </div>
  );
}

// pathParamNames pulls the {name} placeholders out of an endpoint
// template so the form knows which inputs to render. Stable across
// renders because the route's path is stable.
function pathParamNames(path: string): string[] {
  const out: string[] = [];
  const re = /\{([^}/]+)\}/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(path)) !== null) {
    out.push(m[1]);
  }
  return out;
}

type KVRow = { key: string; value: string };

function TryItPanel({ e }: { e: EndpointMeta }) {
  const params = pathParamNames(e.path);
  const [pathValues, setPathValues] = useState<Record<string, string>>({});
  const [query, setQuery] = useState<KVRow[]>([{ key: "", value: "" }]);
  const [headers, setHeaders] = useState<KVRow[]>([{ key: "", value: "" }]);
  const [body, setBody] = useState("");
  const [response, setResponse] = useState<TryItResponse | null>(null);
  const [error, setError] = useState<string | null>(null);

  const run = useMutation({
    mutationFn: () => {
      const queryParams: Record<string, string[]> = {};
      for (const r of query) {
        if (!r.key) continue;
        (queryParams[r.key] ??= []).push(r.value);
      }
      const headerMap: Record<string, string[]> = {};
      for (const r of headers) {
        if (!r.key) continue;
        (headerMap[r.key] ??= []).push(r.value);
      }
      return portalAPI.tryIt(e.group, e.name, {
        path_params: pathValues,
        query_params: Object.keys(queryParams).length ? queryParams : undefined,
        headers: Object.keys(headerMap).length ? headerMap : undefined,
        body: body || undefined,
      });
    },
    onSuccess: (r) => {
      setResponse(r);
      setError(null);
    },
    onError: (err) => {
      setError(err instanceof Error ? err.message : String(err));
      setResponse(null);
    },
  });

  const supportsBody = !["GET", "HEAD", "OPTIONS"].includes(e.method);

  return (
    <div className="space-y-3">
      <div className="text-xs font-medium text-muted-foreground">Try it</div>

      {params.length > 0 && (
        <div className="space-y-2">
          <div className="text-xs text-muted-foreground">Path parameters</div>
          {params.map((name) => (
            <div key={name} className="flex items-center gap-2">
              <label className="text-xs mono w-24 truncate" title={name}>{name}</label>
              <input
                type="text"
                value={pathValues[name] ?? ""}
                onChange={(ev) => setPathValues({ ...pathValues, [name]: ev.target.value })}
                className="flex-1 bg-background border border-input rounded px-2 py-1 text-sm mono"
                placeholder={`{${name}}`}
              />
            </div>
          ))}
        </div>
      )}

      <KVEditor label="Query parameters" rows={query} setRows={setQuery} />
      <KVEditor label="Headers" rows={headers} setRows={setHeaders} />

      {supportsBody && (
        <div className="space-y-1">
          <div className="text-xs text-muted-foreground">Body</div>
          <textarea
            value={body}
            onChange={(ev) => setBody(ev.target.value)}
            placeholder="(JSON body)"
            className="w-full bg-background border border-input rounded px-2 py-1 text-sm mono font-mono"
            rows={3}
          />
        </div>
      )}

      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={() => run.mutate()}
          disabled={run.isPending}
          className="bg-primary text-primary-foreground rounded px-3 py-1.5 text-sm disabled:opacity-50"
        >
          {run.isPending ? "Sending…" : `Send ${e.method}`}
        </button>
        <div className="text-xs text-muted-foreground mono">{e.method} {e.path}</div>
      </div>

      {error && (
        <div className="bg-destructive/10 text-destructive border border-destructive/40 rounded p-2 text-sm">
          {error}
        </div>
      )}

      {response && (
        <div className="space-y-2">
          <div className="flex items-center gap-2 text-xs">
            <span className={`mono ${response.status >= 400 ? "text-destructive" : "text-success"}`}>
              {response.status}
            </span>
            <span className="text-muted-foreground mono">{response.method}</span>
            <span className="text-muted-foreground mono truncate" title={response.dispatched_to}>
              {response.dispatched_to}
            </span>
            {response.body_truncated && (
              // text-destructive is defined in the theme; text-warning
              // is not, and would render as the inherited foreground
              // color — invisible against muted-foreground siblings.
              <span className="text-destructive text-xs ml-auto">body truncated</span>
            )}
          </div>
          <pre className="mono text-xs bg-muted/30 border border-border rounded p-2 overflow-auto max-h-[40vh] whitespace-pre-wrap break-words">
            {formatResponseBody(response)}
          </pre>
          {Object.keys(response.headers).length > 0 && (
            <details className="text-xs">
              <summary className="cursor-pointer text-muted-foreground">Response headers ({Object.keys(response.headers).length})</summary>
              <pre className="mono mt-1 bg-muted/30 border border-border rounded p-2 overflow-auto max-h-[20vh]">
                {Object.entries(response.headers).map(([k, vs]) => `${k}: ${vs.join(", ")}`).join("\n")}
              </pre>
            </details>
          )}
        </div>
      )}
    </div>
  );
}

function formatResponseBody(r: TryItResponse): string {
  if (!r.body) return "(empty body)";
  try {
    return JSON.stringify(JSON.parse(r.body), null, 2);
  } catch {
    return r.body;
  }
}

function KVEditor({ label, rows, setRows }: { label: string; rows: KVRow[]; setRows: (r: KVRow[]) => void }) {
  function update(idx: number, key: "key" | "value", value: string) {
    const next = rows.map((r, i) => (i === idx ? { ...r, [key]: value } : r));
    if (idx === rows.length - 1 && (next[idx].key || next[idx].value)) {
      next.push({ key: "", value: "" });
    }
    setRows(next);
  }
  function remove(idx: number) {
    const next = rows.filter((_, i) => i !== idx);
    setRows(next.length ? next : [{ key: "", value: "" }]);
  }
  return (
    <div className="space-y-1">
      <div className="text-xs text-muted-foreground">{label}</div>
      {rows.map((r, idx) => (
        <div key={idx} className="flex gap-2">
          <input
            type="text"
            value={r.key}
            onChange={(ev) => update(idx, "key", ev.target.value)}
            placeholder="key"
            className="w-1/3 bg-background border border-input rounded px-2 py-1 text-sm mono"
          />
          <input
            type="text"
            value={r.value}
            onChange={(ev) => update(idx, "value", ev.target.value)}
            placeholder="value"
            className="flex-1 bg-background border border-input rounded px-2 py-1 text-sm mono"
          />
          <button
            type="button"
            onClick={() => remove(idx)}
            className="text-muted-foreground hover:text-destructive px-2 text-sm"
            title="remove"
          >
            ×
          </button>
        </div>
      ))}
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
