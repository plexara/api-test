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
        <h1 className="text-2xl font-semibold tracking-tight">Audit</h1>
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
        narrow viewports. `minmax(0,1fr)` on the right cell is required:
        without the 0 floor, grid children inherit `min-width: auto` and
        the right column fights the left when JSON values are long.
        `table-fixed` + an explicit `<colgroup>` pin the inner table so
        the cells honor the declared widths.
      */}
      <div className="grid grid-cols-1 lg:grid-cols-[460px_minmax(0,1fr)] gap-4">
        <div className="bg-card text-card-foreground border border-border rounded-lg overflow-hidden">
          <table className="w-full text-sm table-fixed">
            {/*
              Column widths are sized empirically (measured via getBoundingClientRect):
              - Time: 76px fits "23:59:59" in mono text-xs with breathing room.
                We deliberately use 24-hour format so a 12-hour "PM" suffix
                can't push the column wider than the colgroup declares.
              - Method: 88px fits the widest pill — DELETE (~78px rendered)
                and OPTIONS (~80px rendered) — including px-1.5 padding,
                tracking-wider, and ring. 72px was too narrow and DELETE
                pills overflowed the Path column by ~7px.
              - Status: 64px fits a 3-digit pill ("500") right-aligned.
              - Path takes the remainder and truncates with a title tooltip.
            */}
            <colgroup>
              <col className="w-[76px]" />
              <col className="w-[88px]" />
              <col />
              <col className="w-[64px]" />
            </colgroup>
            <thead className="bg-muted/40 text-muted-foreground border-b border-border">
              <tr className="text-xs uppercase tracking-wider">
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
                  className={`border-t border-border/60 cursor-pointer transition-colors hover:bg-muted/40 ${selected === e.id ? "bg-muted/60" : ""}`}
                >
                  <td className="px-3 py-2 text-muted-foreground mono text-xs whitespace-nowrap">
                    {formatTime(e.timestamp)}
                  </td>
                  <td className="px-3 py-2 whitespace-nowrap">
                    <MethodPill method={e.method} />
                  </td>
                  <td className="px-3 py-2 mono text-xs truncate" title={e.path}>{e.path}</td>
                  <td className="px-3 py-2 text-right whitespace-nowrap">
                    <StatusPill status={e.status} />
                  </td>
                </tr>
              ))}
              {q.data?.events.length === 0 && (
                <tr><td colSpan={4} className="px-3 py-6 text-muted-foreground text-center">No events match.</td></tr>
              )}
            </tbody>
          </table>
        </div>
        {/*
          Sticky detail panel. Click an event at the bottom of a long
          list and you should NOT have to scroll back to the top to see
          the detail. `lg:sticky lg:top-6 lg:self-start` pins the panel
          to the top of the viewport (top-6 = 1.5rem, matching the
          parent <main className="p-6"> padding). `lg:self-start` is
          required: without it the right grid track stretches to the
          left track's height and `sticky` has nothing to anchor to.
          `lg:max-h-[calc(100vh-3rem)] lg:overflow-y-auto` lets the
          detail itself scroll when its JSON payloads exceed the
          viewport — so long bodies don't push the sticky panel off
          screen.
        */}
        <div className="min-w-0 lg:sticky lg:top-6 lg:self-start lg:max-h-[calc(100vh-3rem)] lg:overflow-y-auto">
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
    <div className="bg-card text-card-foreground border border-border rounded-lg p-4 space-y-4 text-sm min-w-0">
      <div className="flex items-center justify-between gap-3 min-w-0">
        <div className="flex items-center gap-2 min-w-0">
          <MethodPill method={e.method} />
          <code className="mono text-sm font-medium truncate min-w-0" title={e.path}>{e.path}</code>
        </div>
        <StatusPill status={e.status} />
      </div>
      <div className="grid grid-cols-2 gap-x-4 gap-y-2 text-xs min-w-0">
        <Field label="Timestamp" value={new Date(e.timestamp).toLocaleString()} />
        <Field label="Duration" value={`${e.duration_ms}ms`} />
        <Field label="Request ID" value={e.request_id || "-"} mono />
        <Field label="Auth" value={e.auth_type || "-"} />
        <Field label="User" value={e.user_email || e.user_subject || "-"} />
        <Field label="Remote" value={e.remote_addr || "-"} mono />
        <Field label="Bytes in" value={String(e.bytes_in)} mono />
        <Field label="Bytes out" value={String(e.bytes_out)} mono />
      </div>
      {e.payload && (
        <div className="space-y-3 pt-2 border-t border-border">
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

// formatTime renders an ISO timestamp as locale-independent 24-hour HH:MM:SS.
// Built explicitly (not via toLocaleTimeString) so a user with an en-US
// locale doesn't get "12:34:56 PM" that overflows the 76px Time column.
function formatTime(iso: string): string {
  const d = new Date(iso);
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  const ss = String(d.getSeconds()).padStart(2, "0");
  return `${hh}:${mm}:${ss}`;
}

function tryParseJSON(s: string): unknown {
  try {
    return JSON.parse(s);
  } catch {
    return s;
  }
}

function Field({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="min-w-0">
      <div className="text-muted-foreground text-[10px] uppercase tracking-wider mb-0.5">{label}</div>
      <div className={`truncate ${mono ? "mono" : ""}`} title={value}>{value}</div>
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

// Method/status pills are pulled into local helpers (no Discovery import)
// to avoid a cross-page coupling. The HSL palette is the same one
// Discovery uses, replicated here to keep both pages visually consistent;
// if a third surface ever needs the colors, factor into ui/src/lib/.
const METHOD_PILL: Record<string, string> = {
  GET:     "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 ring-1 ring-emerald-500/30",
  POST:    "bg-sky-500/15     text-sky-700     dark:text-sky-300     ring-1 ring-sky-500/30",
  PUT:     "bg-amber-500/15   text-amber-700   dark:text-amber-300   ring-1 ring-amber-500/30",
  PATCH:   "bg-violet-500/15  text-violet-700  dark:text-violet-300  ring-1 ring-violet-500/30",
  DELETE:  "bg-rose-500/15    text-rose-700    dark:text-rose-300    ring-1 ring-rose-500/30",
  OPTIONS: "bg-slate-500/15   text-slate-700   dark:text-slate-300   ring-1 ring-slate-500/30",
  HEAD:    "bg-slate-500/15   text-slate-700   dark:text-slate-300   ring-1 ring-slate-500/30",
};

function MethodPill({ method }: { method: string }) {
  const cls = METHOD_PILL[method.toUpperCase()] ?? METHOD_PILL.OPTIONS;
  return (
    <span className={`mono text-[10px] font-semibold uppercase tracking-wider px-1.5 py-0.5 rounded ${cls}`}>
      {method}
    </span>
  );
}

function StatusPill({ status }: { status: number }) {
  let cls = "bg-slate-500/15 text-slate-700 dark:text-slate-300 ring-1 ring-slate-500/30";
  if (status >= 500)      cls = "bg-rose-500/15    text-rose-700    dark:text-rose-300    ring-1 ring-rose-500/30";
  else if (status >= 400) cls = "bg-amber-500/15   text-amber-700   dark:text-amber-300   ring-1 ring-amber-500/30";
  else if (status >= 300) cls = "bg-sky-500/15     text-sky-700     dark:text-sky-300     ring-1 ring-sky-500/30";
  else if (status >= 200) cls = "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 ring-1 ring-emerald-500/30";
  return (
    <span className={`mono text-[10px] font-semibold tabular-nums px-1.5 py-0.5 rounded ${cls}`}>
      {status}
    </span>
  );
}
