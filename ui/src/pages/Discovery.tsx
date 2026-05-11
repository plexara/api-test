// Discovery page: native OpenAPI 3.1 reference view.
//
// We deliberately do NOT iframe /docs (Redoc). Reasons, in order of
// importance:
//   1. Design-system parity. The portal's CSS variables (light/dark)
//      cannot reach into a same-origin iframe to restyle the embedded
//      Redoc bundle; the operator sees a light Redoc against a dark
//      shell. (Redoc itself supports a theme config, but plumbing it
//      through a build-time-rendered docs HTML is a heavier lift than
//      rendering the OpenAPI document directly here.)
//   2. In-portal search/filter — the iframe can't participate in the
//      portal's tag rail or query bar.
//   3. Clipboard copy works without sandbox gymnastics.
//
// /docs is still linked from the page header for operators who want the
// canonical Redoc view; the link opens in a new tab.

import { useEffect, useMemo, useRef, useState } from "react";
import { Search, ChevronDown, ChevronRight, Lock, Copy, Check, ExternalLink } from "lucide-react";

type OpenAPIDoc = {
  openapi?: string;
  info?: { title?: string; version?: string; description?: string };
  tags?: Array<{ name: string; description?: string }>;
  paths?: Record<string, Record<string, Operation>>;
  components?: { schemas?: Record<string, Schema>; securitySchemes?: Record<string, unknown> };
};

type Operation = {
  tags?: string[];
  summary?: string;
  description?: string;
  operationId?: string;
  parameters?: Parameter[];
  requestBody?: { description?: string; required?: boolean; content?: Record<string, MediaType> };
  responses?: Record<string, ResponseObject>;
  security?: Array<Record<string, string[]>>;
  deprecated?: boolean;
};

type Parameter = { name: string; in: "query" | "header" | "path" | "cookie"; required?: boolean; description?: string; schema?: Schema };
type MediaType = { schema?: Schema; example?: unknown; examples?: Record<string, { value?: unknown; summary?: string }> };
type ResponseObject = { description?: string; content?: Record<string, MediaType>; headers?: Record<string, unknown> };
type Schema = {
  type?: string | string[];
  format?: string;
  description?: string;
  enum?: unknown[];
  default?: unknown;
  example?: unknown;
  examples?: unknown[];
  items?: Schema;
  properties?: Record<string, Schema>;
  required?: string[];
  additionalProperties?: boolean | Schema;
  $ref?: string;
  oneOf?: Schema[];
  anyOf?: Schema[];
  allOf?: Schema[];
  nullable?: boolean;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  pattern?: string;
  // OpenAPI 3.1 / JSON Schema allows arbitrary extension keywords.
  [key: string]: unknown;
};

const METHODS = ["get", "post", "put", "patch", "delete", "options", "head"] as const;
type Method = (typeof METHODS)[number];

// Method palette is fixed in HSL so the badges stay legible against both
// the light card background and the dark slate. Each color stays inside
// the WCAG AA contrast band for foreground/background pairs we ship.
const METHOD_COLOR: Record<Method, string> = {
  get:     "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 ring-1 ring-emerald-500/30",
  post:    "bg-sky-500/15     text-sky-700     dark:text-sky-300     ring-1 ring-sky-500/30",
  put:     "bg-amber-500/15   text-amber-700   dark:text-amber-300   ring-1 ring-amber-500/30",
  patch:   "bg-violet-500/15  text-violet-700  dark:text-violet-300  ring-1 ring-violet-500/30",
  delete:  "bg-rose-500/15    text-rose-700    dark:text-rose-300    ring-1 ring-rose-500/30",
  options: "bg-slate-500/15   text-slate-700   dark:text-slate-300   ring-1 ring-slate-500/30",
  head:    "bg-slate-500/15   text-slate-700   dark:text-slate-300   ring-1 ring-slate-500/30",
};

export default function Discovery() {
  const [doc, setDoc] = useState<OpenAPIDoc | null>(null);
  const [status, setStatus] = useState<"loading" | "ok" | "missing" | "error">("loading");
  const [query, setQuery] = useState("");
  const [activeTag, setActiveTag] = useState<string | "all">("all");

  useEffect(() => {
    const ctrl = new AbortController();
    fetch("/openapi.json", { credentials: "include", signal: ctrl.signal })
      .then(async (r) => {
        if (!r.ok) {
          setStatus(r.status === 404 ? "missing" : "error");
          return;
        }
        setDoc(await r.json());
        setStatus("ok");
      })
      .catch((err) => {
        if (err?.name === "AbortError") return;
        setStatus("error");
      });
    return () => ctrl.abort();
  }, []);

  const groups = useMemo(() => groupOperations(doc), [doc]);
  const tags = useMemo(() => ["all" as const, ...groups.map((g) => g.tag)], [groups]);
  const filtered = useMemo(() => filterGroups(groups, query, activeTag), [groups, query, activeTag]);

  if (status === "loading") {
    return <DiscoveryShell status="loading" info={doc?.info}><div className="px-6 py-12 text-sm text-muted-foreground">Loading OpenAPI document…</div></DiscoveryShell>;
  }
  if (status === "missing") {
    return (
      <DiscoveryShell status="missing">
        <div className="px-6 py-8 max-w-3xl text-sm text-muted-foreground space-y-2">
          <div className="text-foreground font-medium">OpenAPI surface not available.</div>
          <p>
            This deployment of api-test is running without the OpenAPI generator surface
            mounted. The Discovery page expects <code className="mono">/openapi.json</code> to
            be reachable; the request returned 404.
          </p>
        </div>
      </DiscoveryShell>
    );
  }
  if (status === "error" || !doc) {
    return (
      <DiscoveryShell status="error">
        <div className="px-6 py-8 max-w-3xl text-sm text-destructive">
          Failed to load <code className="mono">/openapi.json</code>.
        </div>
      </DiscoveryShell>
    );
  }

  return (
    <DiscoveryShell info={doc.info}>
      <div className="grid grid-cols-[260px_minmax(0,1fr)] min-h-0 flex-1">
        {/* Sticky tag rail. */}
        <aside aria-label="Operation groups" className="border-r border-border bg-muted/20 overflow-y-auto px-3 py-4 space-y-1 text-sm">
          {tags.map((t) => {
            const count = t === "all"
              ? groups.reduce((n, g) => n + g.operations.length, 0)
              : groups.find((g) => g.tag === t)?.operations.length ?? 0;
            return (
              <button
                key={t}
                onClick={() => setActiveTag(t)}
                aria-pressed={activeTag === t}
                className={[
                  // min-w-0 + truncate inside is required: without it the long
                  // tag name forces the button wider than the column, pushing
                  // the count badge off-screen.
                  "w-full text-left px-2 py-1.5 rounded transition-colors flex items-center justify-between gap-2 min-w-0",
                  activeTag === t
                    ? "bg-primary/15 text-foreground ring-1 ring-primary/30"
                    : "hover:bg-muted text-muted-foreground hover:text-foreground",
                ].join(" ")}
              >
                <span className="truncate min-w-0">{t === "all" ? "All operations" : t}</span>
                <span className="mono text-xs text-muted-foreground shrink-0">{count}</span>
              </button>
            );
          })}
        </aside>

        {/* Main scroll area. */}
        <main className="overflow-y-auto">
          <div className="px-6 py-4 sticky top-0 z-10 bg-background/85 backdrop-blur border-b border-border">
            <div className="relative max-w-2xl">
              <Search className="size-4 absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground" />
              <input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Filter by path, summary, or operationId…"
                className="w-full pl-8 pr-3 py-2 bg-background border border-input rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-ring"
              />
            </div>
          </div>
          <div className="px-6 py-4 space-y-8">
            {filtered.length === 0 && (
              <div className="text-sm text-muted-foreground">No operations match.</div>
            )}
            {filtered.map((g) => (
              <section key={g.tag} className="space-y-3">
                <header className="flex items-baseline gap-3">
                  <h2 className="text-lg font-semibold tracking-tight">{g.tag}</h2>
                  <span className="mono text-xs text-muted-foreground">{g.operations.length}</span>
                  {g.description && (
                    <span className="text-xs text-muted-foreground truncate min-w-0">{g.description}</span>
                  )}
                </header>
                <div className="space-y-2">
                  {g.operations.map((op) => (
                    <OperationCard
                      key={`${op.method}-${op.path}`}
                      method={op.method}
                      path={op.path}
                      op={op.op}
                      schemas={doc.components?.schemas ?? {}}
                    />
                  ))}
                </div>
              </section>
            ))}
          </div>
        </main>
      </div>
    </DiscoveryShell>
  );
}

// DiscoveryShell holds the page chrome (title row + downloads + bordered
// frame) and slots the variable content underneath. Pulled out so the
// loading / missing / error states get the same chrome as the live view.
function DiscoveryShell({
  info,
  status = "ok",
  children,
}: {
  info?: OpenAPIDoc["info"];
  status?: "ok" | "loading" | "missing" | "error";
  children: React.ReactNode;
}) {
  const subtitle = (() => {
    if (status === "loading") return "Fetching /openapi.json…";
    if (status === "missing") return "/openapi.json is not mounted on this deployment.";
    if (status === "error")   return "Could not load /openapi.json.";
    // Avoid concatenating with an em-dash: many published OpenAPI titles
    // already contain em-dashes, which would render "Foo — bar — /openapi.json".
    if (info?.title) return `${info.title}  /  /openapi.json`;
    return "OpenAPI 3.1 reference  /  /openapi.json";
  })();
  // /openapi.json, /openapi.yaml, and /docs are all mounted together
  // (pkg/httpsrv/openapi.go); a missing/errored /openapi.json means the
  // sibling links also 404. Hide them in those states so we don't offer
  // dead links the user just told us are broken.
  const showDownloads = status === "ok";
  return (
    <div className="flex flex-col h-[calc(100vh-3rem)] -mx-6 -my-6">
      <div className="px-6 py-3 border-b border-border bg-card flex items-center justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-lg font-semibold flex items-baseline gap-2">
            <span>Discovery</span>
            {info?.version && (
              <span className="mono text-xs text-muted-foreground">v{info.version}</span>
            )}
          </h1>
          <div className="text-xs text-muted-foreground truncate">{subtitle}</div>
        </div>
        {showDownloads && (
          <div className="flex gap-2 text-xs shrink-0">
            <a
              href="/docs"
              target="_blank"
              rel="noopener noreferrer"
              className="bg-background border border-input rounded px-2 py-1 hover:bg-muted inline-flex items-center gap-1"
            >
              Redoc <ExternalLink className="size-3" />
            </a>
            <a
              href="/openapi.json"
              className="bg-background border border-input rounded px-2 py-1 hover:bg-muted"
              download
            >
              JSON
            </a>
            <a
              href="/openapi.yaml"
              className="bg-background border border-input rounded px-2 py-1 hover:bg-muted"
              download
            >
              YAML
            </a>
          </div>
        )}
      </div>
      {children}
    </div>
  );
}

// OperationCard renders one HTTP operation as a collapsible card. Closed
// state shows method+path+summary; open state shows parameters, request
// body, responses, and a JSON example pulled from the schema.
function OperationCard({
  method,
  path,
  op,
  schemas,
}: {
  method: Method;
  path: string;
  op: Operation;
  schemas: Record<string, Schema>;
}) {
  const [open, setOpen] = useState(false);
  const needsAuth = (op.security?.length ?? 0) > 0;

  return (
    <div className="bg-card border border-border rounded-md overflow-hidden">
      <button
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        className="w-full flex items-center gap-3 px-3 py-2 hover:bg-muted/40 text-left min-w-0"
      >
        {open
          ? <ChevronDown className="size-4 text-muted-foreground shrink-0" />
          : <ChevronRight className="size-4 text-muted-foreground shrink-0" />}
        <span className={`mono text-xs font-semibold uppercase px-2 py-0.5 rounded ${METHOD_COLOR[method]} shrink-0`}>
          {method}
        </span>
        <code className="mono text-sm text-foreground truncate min-w-0">{path}</code>
        {needsAuth && (
          <span title="Auth required" className="text-muted-foreground shrink-0">
            <Lock className="size-3.5" />
          </span>
        )}
        {op.deprecated && (
          <span className="text-xs uppercase tracking-wide text-amber-600 dark:text-amber-400 shrink-0">deprecated</span>
        )}
        {op.summary && (
          <span className="text-sm text-muted-foreground truncate min-w-0 ml-auto pl-3">
            {op.summary}
          </span>
        )}
      </button>

      {open && (
        <div className="border-t border-border px-4 py-3 space-y-4 text-sm">
          {op.description && (
            <p className="text-muted-foreground whitespace-pre-wrap">{op.description}</p>
          )}

          {op.parameters && op.parameters.length > 0 && (
            <Section title="Parameters">
              <ParameterTable params={op.parameters} schemas={schemas} />
            </Section>
          )}

          {op.requestBody && (
            <Section title="Request body">
              <RequestBodyView body={op.requestBody} schemas={schemas} />
            </Section>
          )}

          {op.responses && (
            <Section title="Responses">
              <ResponsesView responses={op.responses} schemas={schemas} />
            </Section>
          )}
        </div>
      )}
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="space-y-2">
      <div className="text-xs uppercase tracking-wider text-muted-foreground">{title}</div>
      {children}
    </div>
  );
}

function ParameterTable({ params, schemas }: { params: Parameter[]; schemas: Record<string, Schema> }) {
  return (
    <div className="border border-border rounded overflow-hidden">
      <table className="w-full text-xs table-fixed">
        <colgroup>
          <col className="w-[160px]" />
          <col className="w-[80px]" />
          <col className="w-[120px]" />
          <col />
        </colgroup>
        <thead className="bg-muted/40 text-muted-foreground">
          <tr>
            <th className="text-left px-3 py-1.5 font-medium">Name</th>
            <th className="text-left px-3 py-1.5 font-medium">In</th>
            <th className="text-left px-3 py-1.5 font-medium">Type</th>
            <th className="text-left px-3 py-1.5 font-medium">Description</th>
          </tr>
        </thead>
        <tbody>
          {params.map((p) => (
            <tr key={`${p.in}-${p.name}`} className="border-t border-border align-top">
              <td className="px-3 py-1.5 mono whitespace-nowrap">
                {p.name}
                {p.required && <span className="text-rose-600 dark:text-rose-400 ml-1">*</span>}
              </td>
              <td className="px-3 py-1.5 text-muted-foreground">{p.in}</td>
              <td className="px-3 py-1.5 mono text-muted-foreground truncate" title={schemaType(p.schema, schemas)}>{schemaType(p.schema, schemas)}</td>
              <td className="px-3 py-1.5 text-muted-foreground break-words">{p.description ?? p.schema?.description ?? ""}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function RequestBodyView({ body, schemas }: { body: NonNullable<Operation["requestBody"]>; schemas: Record<string, Schema> }) {
  const entries = Object.entries(body.content ?? {});
  if (entries.length === 0) {
    return <div className="text-muted-foreground text-xs italic">(no body schema)</div>;
  }
  return (
    <div className="space-y-2">
      {body.description && <p className="text-muted-foreground">{body.description}</p>}
      {entries.map(([ct, mt]) => (
        <MediaTypeView key={ct} contentType={ct} mt={mt} schemas={schemas} />
      ))}
    </div>
  );
}

function ResponsesView({ responses, schemas }: { responses: Record<string, ResponseObject>; schemas: Record<string, Schema> }) {
  const entries = Object.entries(responses);
  return (
    <div className="space-y-2">
      {entries.map(([code, resp]) => (
        <ResponseRow key={code} code={code} resp={resp} schemas={schemas} />
      ))}
    </div>
  );
}

function ResponseRow({ code, resp, schemas }: { code: string; resp: ResponseObject; schemas: Record<string, Schema> }) {
  const [open, setOpen] = useState(false);
  const hasBody = Object.keys(resp.content ?? {}).length > 0;
  const head = (
    <>
      {hasBody && (open
        ? <ChevronDown className="size-3.5 text-muted-foreground shrink-0" />
        : <ChevronRight className="size-3.5 text-muted-foreground shrink-0" />)}
      <span className={`mono text-xs font-semibold ${statusBadgeColor(code)}`}>{code}</span>
      <span className="text-muted-foreground truncate min-w-0">{resp.description ?? ""}</span>
    </>
  );
  return (
    <div className="border border-border rounded overflow-hidden">
      {hasBody ? (
        // Disclosure: button + aria-expanded so screen readers announce
        // expanded/collapsed state.
        <button
          onClick={() => setOpen(!open)}
          aria-expanded={open}
          className="w-full flex items-center gap-3 px-3 py-1.5 text-left hover:bg-muted/40 cursor-pointer min-w-0"
        >
          {head}
        </button>
      ) : (
        // No body: render a non-interactive row so keyboard users don't
        // tab through a button that does nothing.
        <div className="w-full flex items-center gap-3 px-3 py-1.5 min-w-0">{head}</div>
      )}
      {open && hasBody && (
        <div className="border-t border-border px-3 py-2 space-y-2">
          {Object.entries(resp.content ?? {}).map(([ct, mt]) => (
            <MediaTypeView key={ct} contentType={ct} mt={mt} schemas={schemas} />
          ))}
        </div>
      )}
    </div>
  );
}

function MediaTypeView({ contentType, mt, schemas }: { contentType: string; mt: MediaType; schemas: Record<string, Schema> }) {
  const resolved = resolveSchema(mt.schema, schemas);
  const example = pickExample(mt) ?? exampleFromSchema(resolved, schemas);
  return (
    <div className="space-y-2">
      <div className="mono text-xs text-muted-foreground">{contentType}</div>
      {resolved && <SchemaView schema={resolved} schemas={schemas} />}
      {example !== undefined && (
        <CodeBlock title="Example" code={typeof example === "string" ? example : JSON.stringify(example, null, 2)} />
      )}
    </div>
  );
}

function SchemaView({ schema, schemas, depth = 0 }: { schema: Schema; schemas: Record<string, Schema>; depth?: number }) {
  const resolved = resolveSchema(schema, schemas);
  if (!resolved) return null;
  if (resolved.type === "object" || resolved.properties) {
    const required = new Set(resolved.required ?? []);
    return (
      <div className="border border-border rounded overflow-hidden">
        <table className="w-full text-xs table-fixed">
          <colgroup>
            <col className="w-[200px]" />
            <col className="w-[140px]" />
            <col />
          </colgroup>
          <thead className="bg-muted/40 text-muted-foreground">
            <tr>
              <th className="text-left px-3 py-1.5 font-medium">Field</th>
              <th className="text-left px-3 py-1.5 font-medium">Type</th>
              <th className="text-left px-3 py-1.5 font-medium">Description</th>
            </tr>
          </thead>
          <tbody>
            {Object.entries(resolved.properties ?? {}).map(([name, prop]) => {
              const r = resolveSchema(prop, schemas);
              return (
                <tr key={name} className="border-t border-border align-top">
                  <td className="px-3 py-1.5 mono whitespace-nowrap">
                    {name}
                    {required.has(name) && <span className="text-rose-600 dark:text-rose-400 ml-1">*</span>}
                  </td>
                  <td className="px-3 py-1.5 mono text-muted-foreground truncate" title={schemaType(r, schemas)}>{schemaType(r, schemas)}</td>
                  <td className="px-3 py-1.5 text-muted-foreground break-words">{r?.description ?? ""}</td>
                </tr>
              );
            })}
            {!resolved.properties && (
              <tr><td colSpan={3} className="px-3 py-2 text-muted-foreground italic">(no declared fields)</td></tr>
            )}
          </tbody>
        </table>
      </div>
    );
  }
  if (resolved.type === "array" && resolved.items) {
    return (
      <div className="space-y-2">
        <div className="mono text-xs text-muted-foreground">array&lt;{schemaType(resolved.items, schemas)}&gt;</div>
        {depth < 3 && <SchemaView schema={resolved.items} schemas={schemas} depth={depth + 1} />}
      </div>
    );
  }
  // Scalar — type + optional enum/format/constraints.
  return (
    <div className="text-xs text-muted-foreground space-x-2">
      <span className="mono">{schemaType(resolved, schemas)}</span>
      {resolved.enum && <span>enum: [{resolved.enum.map((e) => JSON.stringify(e)).join(", ")}]</span>}
      {resolved.format && <span>format: {resolved.format}</span>}
    </div>
  );
}

function CodeBlock({ title, code }: { title?: string; code: string }) {
  const [copied, setCopied] = useState(false);
  // Track the active reset timer so an unmount mid-window doesn't fire
  // setCopied on a dead component. React strict-mode double-mount would
  // otherwise queue two timers from one click.
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => () => {
    if (timerRef.current) clearTimeout(timerRef.current);
  }, []);
  async function copy() {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      if (timerRef.current) clearTimeout(timerRef.current);
      timerRef.current = setTimeout(() => setCopied(false), 1500);
    } catch { /* clipboard unavailable on http:// origins */ }
  }
  return (
    <div className="relative group">
      {title && <div className="text-xs text-muted-foreground mb-1">{title}</div>}
      <pre className="mono text-xs bg-muted/30 border border-border rounded p-3 overflow-auto max-h-[40vh] whitespace-pre-wrap break-words">{code}</pre>
      <button
        onClick={copy}
        className="absolute top-1 right-1 opacity-0 group-hover:opacity-100 transition-opacity bg-card border border-border rounded p-1 text-muted-foreground hover:text-foreground"
        title={copied ? "copied" : "copy"}
      >
        {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
      </button>
    </div>
  );
}

// --- helpers ---

type GroupedOp = { method: Method; path: string; op: Operation };
type Group = { tag: string; description?: string; operations: GroupedOp[] };

function groupOperations(doc: OpenAPIDoc | null): Group[] {
  if (!doc?.paths) return [];
  const buckets = new Map<string, GroupedOp[]>();
  for (const [path, item] of Object.entries(doc.paths)) {
    for (const m of METHODS) {
      const op = item[m] as Operation | undefined;
      if (!op) continue;
      const tag = op.tags?.[0] ?? "untagged";
      const arr = buckets.get(tag) ?? [];
      arr.push({ method: m, path, op });
      buckets.set(tag, arr);
    }
  }
  const declaredOrder = (doc.tags ?? []).map((t) => t.name);
  const tags = Array.from(buckets.keys()).sort((a, b) => {
    const ai = declaredOrder.indexOf(a);
    const bi = declaredOrder.indexOf(b);
    if (ai !== -1 && bi !== -1) return ai - bi;
    if (ai !== -1) return -1;
    if (bi !== -1) return 1;
    return a.localeCompare(b);
  });
  return tags.map((t) => ({
    tag: t,
    description: doc.tags?.find((x) => x.name === t)?.description,
    operations: (buckets.get(t) ?? []).sort((a, b) => a.path.localeCompare(b.path) || a.method.localeCompare(b.method)),
  }));
}

function filterGroups(groups: Group[], query: string, tag: string | "all"): Group[] {
  const q = query.trim().toLowerCase();
  const byTag = tag === "all" ? groups : groups.filter((g) => g.tag === tag);
  if (!q) return byTag;
  return byTag
    .map((g) => ({
      ...g,
      operations: g.operations.filter((op) =>
        op.path.toLowerCase().includes(q) ||
        (op.op.summary?.toLowerCase().includes(q) ?? false) ||
        (op.op.operationId?.toLowerCase().includes(q) ?? false) ||
        (op.op.description?.toLowerCase().includes(q) ?? false),
      ),
    }))
    .filter((g) => g.operations.length > 0);
}

function resolveSchema(schema: Schema | undefined, schemas: Record<string, Schema>, seen: Set<string> = new Set()): Schema | undefined {
  if (!schema) return undefined;
  if (schema.$ref) {
    const name = schema.$ref.replace(/^#\/components\/schemas\//, "");
    if (seen.has(name)) return { type: "object", description: `circular ref → ${name}` };
    const next = schemas[name];
    if (!next) return { type: "object", description: `unresolved ref → ${name}` };
    return resolveSchema(next, schemas, new Set([...seen, name]));
  }
  return schema;
}

function schemaType(schema: Schema | undefined, schemas: Record<string, Schema>): string {
  if (!schema) return "";
  if (schema.$ref) return schema.$ref.replace(/^#\/components\/schemas\//, "");
  const resolved = resolveSchema(schema, schemas);
  if (!resolved) return "";
  if (resolved.oneOf) return `oneOf<${resolved.oneOf.map((s) => schemaType(s, schemas)).join(" | ")}>`;
  if (resolved.anyOf) return `anyOf<${resolved.anyOf.map((s) => schemaType(s, schemas)).join(" | ")}>`;
  if (resolved.allOf) return `allOf<${resolved.allOf.map((s) => schemaType(s, schemas)).join(" & ")}>`;
  if (resolved.type === "array" && resolved.items) return `array<${schemaType(resolved.items, schemas)}>`;
  if (Array.isArray(resolved.type)) return resolved.type.join("|");
  const base = resolved.type ?? "object";
  return resolved.format ? `${base}<${resolved.format}>` : base;
}

function pickExample(mt: MediaType): unknown {
  if (mt.example !== undefined) return mt.example;
  if (mt.examples) {
    const first = Object.values(mt.examples)[0];
    if (first && "value" in first) return first.value;
  }
  return undefined;
}

// exampleFromSchema fabricates a minimal example value for a schema when
// the document doesn't provide one. Walks objects + arrays one level
// deep beyond named refs to keep the rendered example readable.
function exampleFromSchema(schema: Schema | undefined, schemas: Record<string, Schema>, depth = 0): unknown {
  if (!schema || depth > 4) return undefined;
  const resolved = resolveSchema(schema, schemas);
  if (!resolved) return undefined;
  if (resolved.example !== undefined) return resolved.example;
  if (resolved.default !== undefined) return resolved.default;
  if (resolved.enum && resolved.enum.length > 0) return resolved.enum[0];
  if (resolved.type === "array" && resolved.items) {
    const inner = exampleFromSchema(resolved.items, schemas, depth + 1);
    return inner === undefined ? [] : [inner];
  }
  if (resolved.type === "object" || resolved.properties) {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(resolved.properties ?? {})) {
      const ex = exampleFromSchema(v, schemas, depth + 1);
      if (ex !== undefined) out[k] = ex;
    }
    return Object.keys(out).length > 0 ? out : undefined;
  }
  switch (resolved.type) {
    case "string":  return resolved.format === "uuid" ? "00000000-0000-0000-0000-000000000000"
                       : resolved.format === "date-time" ? new Date().toISOString()
                       : "string";
    case "integer": return 0;
    case "number":  return 0;
    case "boolean": return false;
  }
  return undefined;
}

function statusBadgeColor(code: string): string {
  if (code.startsWith("2")) return "text-emerald-700 dark:text-emerald-300";
  if (code.startsWith("3")) return "text-sky-700 dark:text-sky-300";
  if (code.startsWith("4")) return "text-amber-700 dark:text-amber-300";
  if (code.startsWith("5")) return "text-rose-700 dark:text-rose-300";
  return "text-muted-foreground";
}
