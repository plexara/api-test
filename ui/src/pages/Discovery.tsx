// Discovery page: embeds the Redoc viewer served at /docs (rendered
// against /openapi.json by the oapi-generator backend). The iframe
// loads the same /docs URL an operator would visit directly; the
// portal just frames it alongside the rest of the sidebar nav.

import { useEffect, useState } from "react";

export default function Discovery() {
  const [reachable, setReachable] = useState<"checking" | "ok" | "missing">("checking");

  useEffect(() => {
    // Probe /openapi.json so we can show a friendly empty-state if
    // the operator is running a build without the oapi-generator
    // surface mounted. We HEAD the JSON rather than the HTML because
    // a 404 on /openapi.json is the most reliable signal that the
    // OpenAPI surface isn't wired up; the /docs HTML may exist as a
    // static asset even when the generator is absent.
    fetch("/openapi.json", { method: "HEAD", credentials: "include" })
      .then((r) => setReachable(r.ok ? "ok" : "missing"))
      .catch(() => setReachable("missing"));
  }, []);

  return (
    <div className="flex flex-col h-[calc(100vh-3rem)] max-w-none -mx-6 -my-6">
      <div className="px-6 py-3 border-b border-border bg-card flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold">Discovery</h1>
          <div className="text-xs text-muted-foreground">
            OpenAPI 3.1 reference rendered from{" "}
            <a href="/openapi.json" className="underline hover:text-foreground">/openapi.json</a>
            {" "}via{" "}
            <a href="/docs" className="underline hover:text-foreground">/docs</a>.
          </div>
        </div>
        <div className="flex gap-2 text-xs">
          <a
            href="/openapi.json"
            className="bg-card border border-input rounded px-2 py-1 hover:bg-muted"
            download
          >
            Download JSON
          </a>
          <a
            href="/openapi.yaml"
            className="bg-card border border-input rounded px-2 py-1 hover:bg-muted"
            download
          >
            Download YAML
          </a>
        </div>
      </div>

      {reachable === "ok" && (
        // sandbox keeps the embedded Redoc from navigating the parent
        // window if a future spec extension introduces an external link
        // with target=_top. allow-scripts is required for the Redoc
        // bundle to render; allow-same-origin lets it fetch
        // /openapi.json relative to the parent origin.
        <iframe
          src="/docs"
          title="API reference"
          className="flex-1 w-full border-0"
          sandbox="allow-scripts allow-same-origin"
        />
      )}

      {reachable === "missing" && (
        <div className="p-6 max-w-3xl text-sm text-muted-foreground space-y-2">
          <div className="text-foreground font-medium">OpenAPI surface not available.</div>
          <p>
            This deployment of api-test is running without the OpenAPI
            generator surface mounted. The Discovery page expects{" "}
            <code className="mono">/openapi.json</code> and{" "}
            <code className="mono">/docs</code> to be reachable; the
            HEAD probe returned a non-OK status.
          </p>
          <p>
            Bring it online by running an api-test build that includes
            the in-tree <code className="mono">pkg/oapi</code>{" "}
            generator (any build from <code className="mono">main</code>{" "}
            once the openapi branch lands). The Plexara API gateway
            also needs this surface to register api-test as a
            connection — Discovery doubles as a quick health check.
          </p>
        </div>
      )}

      {reachable === "checking" && (
        <div className="p-6 text-sm text-muted-foreground">Probing OpenAPI surface…</div>
      )}
    </div>
  );
}
