import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { adminAPI, HttpError } from "@/lib/api";
import { useState } from "react";
import { Trash2, Copy, Check } from "lucide-react";

export default function ApiKeys() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["keys"], queryFn: adminAPI.listKeys });
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [created, setCreated] = useState<{ name: string; plaintext: string } | null>(null);
  const [error, setError] = useState<string | null>(null);

  const createMut = useMutation({
    mutationFn: () => adminAPI.createKey(name, description || undefined),
    onSuccess: (r) => {
      setCreated({ name: r.key.name, plaintext: r.plaintext });
      setName("");
      setDescription("");
      setError(null);
      void qc.invalidateQueries({ queryKey: ["keys"] });
    },
    onError: (e: HttpError) => setError(e.message),
  });

  const deleteMut = useMutation({
    mutationFn: (n: string) => adminAPI.deleteKey(n),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["keys"] }),
  });

  if (q.isLoading) return <div className="text-muted-foreground">Loading…</div>;

  return (
    <div className="space-y-6 max-w-4xl">
      <div className="flex items-baseline justify-between">
        <h1 className="text-2xl font-semibold">API Keys</h1>
        <div className="text-xs text-muted-foreground">{q.data?.keys.length ?? 0} keys</div>
      </div>

      {q.error && (
        <div className="bg-destructive/10 text-destructive border border-destructive/40 rounded p-3 text-sm">
          Failed to load keys (DB-backed key store may be disabled in this build).
        </div>
      )}

      {created && (
        <div className="bg-success/10 border border-success/30 rounded p-3 text-sm space-y-2">
          <div className="font-medium">Key {created.name} created.</div>
          <div className="text-xs text-muted-foreground">
            This is the only time the plaintext value is shown — copy it now.
          </div>
          <CopyBox value={created.plaintext} />
          <button onClick={() => setCreated(null)} className="text-xs text-muted-foreground hover:text-foreground">dismiss</button>
        </div>
      )}

      <div className="bg-card text-card-foreground border border-border rounded-lg p-4 space-y-3">
        <div className="text-sm font-medium">Create new</div>
        <div className="flex gap-2">
          <input
            type="text"
            placeholder="name (e.g. ci-runner)"
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="flex-1 bg-background border border-input rounded px-3 py-2 text-sm"
          />
          <input
            type="text"
            placeholder="description (optional)"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            className="flex-1 bg-background border border-input rounded px-3 py-2 text-sm"
          />
          <button
            onClick={() => createMut.mutate()}
            disabled={!name || createMut.isPending}
            className="bg-primary text-primary-foreground rounded px-4 py-2 text-sm disabled:opacity-50"
          >
            {createMut.isPending ? "Creating…" : "Create"}
          </button>
        </div>
        {error && <div className="text-sm text-destructive">{error}</div>}
      </div>

      <div className="bg-card text-card-foreground border border-border rounded-lg overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-muted/50 text-muted-foreground">
            <tr>
              <th className="text-left px-3 py-2 font-medium">Name</th>
              <th className="text-left px-3 py-2 font-medium">Description</th>
              <th className="text-left px-3 py-2 font-medium">Created</th>
              <th className="text-left px-3 py-2 font-medium">Last used</th>
              <th className="px-3 py-2"></th>
            </tr>
          </thead>
          <tbody>
            {(q.data?.keys ?? []).map((k) => (
              <tr key={k.id} className="border-t border-border">
                <td className="px-3 py-1.5 mono">{k.name}</td>
                <td className="px-3 py-1.5 text-muted-foreground">{k.description || "-"}</td>
                <td className="px-3 py-1.5 text-muted-foreground mono text-xs">{new Date(k.created_at).toLocaleString()}</td>
                <td className="px-3 py-1.5 text-muted-foreground mono text-xs">{k.last_used_at ? new Date(k.last_used_at).toLocaleString() : "never"}</td>
                <td className="px-3 py-1.5 text-right">
                  <button
                    onClick={() => { if (confirm(`Delete key "${k.name}"?`)) deleteMut.mutate(k.name); }}
                    className="text-muted-foreground hover:text-destructive"
                    title="delete"
                  >
                    <Trash2 className="size-4" />
                  </button>
                </td>
              </tr>
            ))}
            {(!q.data?.keys || q.data.keys.length === 0) && (
              <tr><td colSpan={5} className="px-3 py-6 text-muted-foreground text-center">No keys yet.</td></tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function CopyBox({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch { /* clipboard unavailable */ }
  }
  return (
    <div className="flex gap-2 items-center">
      <code className="flex-1 mono bg-muted/30 px-2 py-1 rounded break-all">{value}</code>
      <button onClick={copy} className="bg-card border border-input rounded p-1.5 text-muted-foreground hover:text-foreground">
        {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
      </button>
    </div>
  );
}
