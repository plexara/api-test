import { useQuery } from "@tanstack/react-query";
import { portalAPI } from "@/lib/api";
import { useState } from "react";
import { JsonView } from "@/components/JsonView";

export default function Audit() {
  const [filter, setFilter] = useState({ method: "", path: "", success: "" });
  const [selected, setSelected] = useState<string | null>(null);

  const qs = new URLSearchParams();
  if (filter.method) qs.set("method", filter.method);
  if (filter.path) qs.set("path", filter.path);
  if (filter.success) qs.set("success", filter.success);
  qs.set("limit", "100");

  const q = useQuery({
    queryKey: ["audit", qs.toString()],
    queryFn: () => portalAPI.audit(qs.toString()),
    refetchInterval: 5_000,
  });

  return (
    <div className="space-y-4 max-w-6xl">
      <div className="flex items-baseline justify-between">
        <h1 className="text-2xl font-semibold">Audit</h1>
        <div className="text-xs text-muted-foreground">
          {q.data ? `${q.data.events.length} of ${q.data.total}` : "…"}
        </div>
      </div>

      <div className="flex gap-2 flex-wrap">
        <FilterInput placeholder="method" value={filter.method} onChange={(v) => setFilter({ ...filter, method: v })} />
        <FilterInput placeholder="path contains…" value={filter.path} onChange={(v) => setFilter({ ...filter, path: v })} />
        <select
          value={filter.success}
          onChange={(e) => setFilter({ ...filter, success: e.target.value })}
          className="bg-background border border-input rounded px-2 py-1 text-sm"
        >
          <option value="">all</option>
          <option value="true">success only</option>
          <option value="false">errors only</option>
        </select>
      </div>

      {/*
        Fixed-width left column + flexible right column on lg+; stacked on
        narrow viewports. `grid-cols-[420px_minmax(0,1fr)]` pins the event
        list to a stable width so long JSON payloads on the right
        (headers/body) can't push the list to reflow. `minmax(0,1fr)` is
        the critical part: without the 0 floor, grid children inherit
        `min-width: auto` (i.e. "wide enough to contain longest token"),
        which makes the right pane fight for space whenever a header
        value is long. `table-fixed` on the inner table forces declared
        column widths instead of letting the browser size them to content.
      */}
      <div className="grid grid-cols-1 lg:grid-cols-[420px_minmax(0,1fr)] gap-4">
        <div className="bg-card text-card-foreground border border-border rounded-lg overflow-hidden">
          <table className="w-full text-sm table-fixed">
            <colgroup>
              <col className="w-[88px]" />
              <col className="w-[64px]" />
              <col />
              <col className="w-[56px]" />
            </colgroup>
            <thead className="bg-muted/50 text-muted-foreground">
              <tr>
                <th className="text-left px-3 py-2 font-medium">Time</th>
                <th className="text-left px-3 py-2 font-medium">Method</th>
                <th className="text-left px-3 py-2 font-medium">Path</th>
                <th className="text-right px-3 py-2 font-medium">Status</th>
              </tr>
            </thead>
            <tbody>
              {(q.data?.events ?? []).map((e) => (
                <tr
                  key={e.id}
                  onClick={() => setSelected(e.id)}
                  className={`border-t border-border cursor-pointer hover:bg-muted/40 ${selected === e.id ? "bg-muted/60" : ""}`}
                >
                  <td className="px-3 py-1.5 text-muted-foreground mono text-xs whitespace-nowrap">{new Date(e.timestamp).toLocaleTimeString()}</td>
                  <td className="px-3 py-1.5 mono whitespace-nowrap">{e.method}</td>
                  <td className="px-3 py-1.5 mono truncate" title={e.path}>{e.path}</td>
                  <td className="px-3 py-1.5 text-right mono whitespace-nowrap">
                    <span className={statusColor(e.status)}>{e.status}</span>
                  </td>
                </tr>
              ))}
              {q.data?.events.length === 0 && (
                <tr><td colSpan={4} className="px-3 py-6 text-muted-foreground text-center">No events match.</td></tr>
              )}
            </tbody>
          </table>
        </div>
        <div className="min-w-0">
          {selected ? <EventDetail id={selected} /> : <div className="text-muted-foreground text-sm">Click a row to inspect the request.</div>}
        </div>
      </div>
    </div>
  );
}

function EventDetail({ id }: { id: string }) {
  const q = useQuery({ queryKey: ["audit-event", id], queryFn: () => portalAPI.auditEvent(id) });
  if (q.isLoading) return <div className="text-muted-foreground text-sm">Loading…</div>;
  if (q.error || !q.data) return <div className="text-destructive text-sm">Failed to load event.</div>;
  const e = q.data;
  return (
    <div className="bg-card text-card-foreground border border-border rounded-lg p-4 space-y-3 text-sm min-w-0">
      <div className="flex items-baseline justify-between gap-3 min-w-0">
        <div className="font-semibold mono truncate min-w-0" title={`${e.method} ${e.path}`}>{e.method} {e.path}</div>
        <span className={`mono shrink-0 ${statusColor(e.status)}`}>{e.status}</span>
      </div>
      <div className="grid grid-cols-2 gap-2 text-xs min-w-0">
        <Field label="Timestamp" value={new Date(e.timestamp).toLocaleString()} />
        <Field label="Duration" value={`${e.duration_ms}ms`} />
        <Field label="Request ID" value={e.request_id || "-"} />
        <Field label="Auth" value={e.auth_type || "-"} />
        <Field label="User" value={e.user_email || e.user_subject || "-"} />
        <Field label="Remote" value={e.remote_addr || "-"} />
        <Field label="Bytes in" value={String(e.bytes_in)} />
        <Field label="Bytes out" value={String(e.bytes_out)} />
      </div>
      {e.payload && (
        <div className="space-y-2">
          {e.payload.request_headers && <JsonView label="request_headers" value={e.payload.request_headers} />}
          {e.payload.request_query && <JsonView label="request_query" value={e.payload.request_query} />}
          {e.payload.request_body && <JsonView label="request_body" value={tryParseJSON(e.payload.request_body)} />}
          {e.payload.response_headers && <JsonView label="response_headers" value={e.payload.response_headers} />}
          {e.payload.response_body && <JsonView label="response_body" value={tryParseJSON(e.payload.response_body)} />}
        </div>
      )}
    </div>
  );
}

function tryParseJSON(s: string): unknown {
  try {
    return JSON.parse(s);
  } catch {
    return s;
  }
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-muted-foreground">{label}</div>
      <div className="mono truncate" title={value}>{value}</div>
    </div>
  );
}

function FilterInput({ placeholder, value, onChange }: { placeholder: string; value: string; onChange: (v: string) => void }) {
  return (
    <input
      type="text"
      placeholder={placeholder}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className="bg-background border border-input rounded px-2 py-1 text-sm w-40"
    />
  );
}

function statusColor(status: number): string {
  if (status >= 500) return "text-destructive";
  if (status >= 400) return "text-destructive/80";
  if (status >= 300) return "text-muted-foreground";
  return "text-success";
}
