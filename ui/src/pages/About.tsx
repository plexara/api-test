import { useQuery } from "@tanstack/react-query";
import { portalAPI } from "@/lib/api";

export default function About() {
  const sq = useQuery({ queryKey: ["server"], queryFn: portalAPI.server });
  const wq = useQuery({ queryKey: ["wellknown"], queryFn: portalAPI.wellknown });

  return (
    <div className="space-y-6 max-w-3xl">
      <div className="flex items-baseline justify-between">
        <h1 className="text-2xl font-semibold">About api-test</h1>
        {sq.data && (
          <div className="text-xs text-muted-foreground mono">
            {sq.data.version} · {sq.data.commit?.slice(0, 8) || "?"} · {sq.data.date}
          </div>
        )}
      </div>

      <p className="text-sm leading-relaxed">
        api-test is a controllable HTTP REST fixture used to exercise the
        Plexara API gateway end-to-end. Endpoints are deliberately simple
        and deterministic — their job is not to compute anything useful,
        their job is to make a gateway's behavior observable. Every request
        is recorded in a Postgres-backed audit log, so you can compare what
        a client sent, what reached this server, and what came back.
      </p>

      <div className="bg-card text-card-foreground border border-border rounded-lg p-4 space-y-2">
        <div className="text-sm font-medium">Test against Plexara</div>
        <p className="text-sm text-muted-foreground">
          Register api-test as a Plexara connection (see <code className="mono">examples/plexara-connection.yaml</code>),
          then call its endpoints from any Plexara client. Every call lands in this portal's Audit page.
        </p>
        {wq.data && (
          <div className="text-xs mono text-muted-foreground pt-2 border-t border-border space-y-1">
            <div>API endpoint:    <span className="text-foreground">{wq.data.api_endpoint}</span></div>
            <div>OIDC issuer:     <span className="text-foreground">{wq.data.authorization_server || "(disabled)"}</span></div>
            <div>OIDC audience:   <span className="text-foreground">{wq.data.audience || "(disabled)"}</span></div>
          </div>
        )}
      </div>
    </div>
  );
}
