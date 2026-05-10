import { useQuery } from "@tanstack/react-query";
import { portalAPI } from "@/lib/api";
import { JsonView } from "@/components/JsonView";

export default function Config() {
  const q = useQuery({ queryKey: ["server"], queryFn: portalAPI.server });

  if (q.isLoading) return <div className="text-muted-foreground">Loading…</div>;
  if (q.error) return <div className="text-destructive">Failed to load config.</div>;
  const d = q.data!;

  return (
    <div className="space-y-6 max-w-4xl">
      <div className="flex items-baseline justify-between">
        <h1 className="text-2xl font-semibold">Config</h1>
        <div className="text-xs text-muted-foreground mono">{d.version} · {d.commit?.slice(0, 8) || "?"}</div>
      </div>
      <p className="text-sm text-muted-foreground">
        The effective server config with secrets redacted. Read-only; edit via configs/api-test.live.yaml and restart.
      </p>
      <JsonView label="config" value={d.config} />
    </div>
  );
}
