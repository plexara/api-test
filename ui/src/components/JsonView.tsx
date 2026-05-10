import { useState } from "react";
import { Copy, Check } from "lucide-react";

export function JsonView({ value, label }: { value: unknown; label?: string }) {
  const [copied, setCopied] = useState(false);
  const json = value === undefined || value === null ? "" : JSON.stringify(value, null, 2);

  async function copy() {
    try {
      await navigator.clipboard.writeText(json);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard unavailable on http:// origins */
    }
  }

  if (!json) {
    return (
      <div className="text-xs text-muted-foreground italic">
        {label ? `${label}: ` : ""}(empty)
      </div>
    );
  }

  return (
    <div className="relative group">
      {label && <div className="text-xs text-muted-foreground mb-1">{label}</div>}
      <pre className="mono text-xs bg-muted/30 border border-border rounded p-3 overflow-auto max-h-[60vh] whitespace-pre-wrap break-words">
        {json}
      </pre>
      <button
        onClick={copy}
        className="absolute top-1 right-1 opacity-0 group-hover:opacity-100 transition-opacity bg-card border border-border rounded p-1 text-muted-foreground hover:text-foreground"
        title={copied ? "copied" : "copy JSON"}
      >
        {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
      </button>
    </div>
  );
}
