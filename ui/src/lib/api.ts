// Thin fetch wrapper that:
//   - Targets /api/v1/portal/* and /api/v1/admin/* on the same origin.
//   - Sends X-API-Key from sessionStorage when present (falls through to the
//     session cookie otherwise).
//   - Always sends X-Requested-With on writes so the server's CSRF check
//     accepts the request (and a forged <form> POST cannot reach it).
//   - Surfaces 401 as a typed Error and triggers a registered handler so
//     the auth store can clear local state and bounce to /login.

const API_KEY_STORAGE = "api-test-api-key";

export class HttpError extends Error {
  constructor(public status: number, message: string, public body?: unknown) {
    super(message);
  }
}

export function setApiKey(key: string) {
  sessionStorage.setItem(API_KEY_STORAGE, key);
}

export function clearApiKey() {
  sessionStorage.removeItem(API_KEY_STORAGE);
}

export function getApiKey(): string | null {
  return sessionStorage.getItem(API_KEY_STORAGE);
}

let onUnauthorized: (() => void) | null = null;
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

async function request<T>(
  path: string,
  init: RequestInit = {},
  signal?: AbortSignal,
): Promise<T> {
  const headers = new Headers(init.headers);
  const key = getApiKey();
  if (key) headers.set("X-API-Key", key);
  if (init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  if (!headers.has("X-Requested-With")) {
    headers.set("X-Requested-With", "XMLHttpRequest");
  }
  const resp = await fetch(path, {
    credentials: "include",
    ...init,
    headers,
    signal,
  });
  if (resp.status === 204) return undefined as T;

  const ct = resp.headers.get("content-type") || "";
  let body: unknown;
  if (ct.includes("application/json")) {
    body = await resp.json().catch(() => undefined);
  } else {
    body = await resp.text();
  }

  if (!resp.ok) {
    if (resp.status === 401) {
      clearApiKey();
      onUnauthorized?.();
    }
    const msg =
      typeof body === "object" && body !== null && "error" in body
        ? String((body as { error: string }).error)
        : resp.statusText || `HTTP ${resp.status}`;
    throw new HttpError(resp.status, msg, body);
  }
  return body as T;
}

export const api = {
  get:    <T>(path: string, signal?: AbortSignal) => request<T>(path, undefined, signal),
  post:   <T>(path: string, body: unknown, signal?: AbortSignal) => request<T>(path, { method: "POST", body: JSON.stringify(body) }, signal),
  delete: <T>(path: string, signal?: AbortSignal) => request<T>(path, { method: "DELETE" }, signal),
};

// --- typed endpoints ---

export type Identity = {
  subject: string;
  email?: string;
  name?: string;
  auth_type: "oidc" | "apikey" | "anonymous";
  claims?: Record<string, unknown>;
  api_key_id?: string;
};

export type EndpointMeta = {
  name: string;
  group: string;
  method: string;
  path: string;
  description: string;
  auth_required: boolean;
};

export type AuditEvent = {
  id: string;
  timestamp: string;
  duration_ms: number;
  request_id?: string;
  session_id?: string;
  user_subject?: string;
  user_email?: string;
  auth_type?: string;
  api_key_name?: string;
  method: string;
  path: string;
  route_name?: string;
  endpoint_group?: string;
  status: number;
  bytes_in: number;
  bytes_out: number;
  success: boolean;
  error_message?: string;
  error_category?: string;
  remote_addr?: string;
  user_agent?: string;
  payload?: AuditPayload;
};

export type AuditPayload = {
  request_headers?: Record<string, string[]>;
  request_query?: Record<string, string[]>;
  request_content_type?: string;
  request_body?: string;
  request_size_bytes?: number;
  request_truncated?: boolean;
  request_remote_addr?: string;
  response_headers?: Record<string, string[]>;
  response_content_type?: string;
  response_body?: string;
  response_size_bytes?: number;
  response_truncated?: boolean;
  replayed_from?: string;
};

export type AuditMeta = {
  filters: string[];
  features: { timeseries: boolean; breakdown: boolean; stats: boolean; stream: boolean; export: boolean; replay: boolean };
};

export type TryItRequest = {
  method?: string;
  path_params?: Record<string, string>;
  query_params?: Record<string, string[]>;
  headers?: Record<string, string[]>;
  body?: string;
};

export type TryItResponse = {
  dispatched_to: string;
  method: string;
  status: number;
  headers: Record<string, string[]>;
  body: string;
  body_truncated: boolean;
};

export type DashboardResponse = {
  window_from: string;
  window_to: string;
  total: number;
  success_count: number;
  recent: AuditEvent[];
};

export type Key = {
  id: string;
  name: string;
  description?: string;
  created_by?: string;
  created_at: string;
  expires_at?: string;
  last_used_at?: string;
};

export const portalAPI = {
  me:           () => api.get<Identity>("/api/v1/portal/me"),
  server:       () => api.get<{ version: string; commit: string; date: string; config: unknown }>("/api/v1/portal/server"),
  endpoints:    () => api.get<{ endpoints: EndpointMeta[] }>("/api/v1/portal/endpoints"),
  endpointDetail: (name: string) => api.get<EndpointMeta>(`/api/v1/portal/endpoints/${encodeURIComponent(name)}`),
  auditMeta:    () => api.get<AuditMeta>("/api/v1/portal/audit/meta"),
  audit:        (qs: string) => api.get<{ events: AuditEvent[]; total: number; limit: number; offset: number }>(`/api/v1/portal/audit/events${qs ? "?" + qs : ""}`),
  auditEvent:   (id: string) => api.get<AuditEvent>(`/api/v1/portal/audit/events/${encodeURIComponent(id)}`),
  dashboard:    () => api.get<DashboardResponse>("/api/v1/portal/dashboard"),
  wellknown:    () => api.get<{ protected_resource_url: string; authorization_server: string; oidc_enabled: boolean; audience: string; api_endpoint: string }>("/api/v1/portal/wellknown"),
  tryIt:        (group: string, route: string, body: TryItRequest) =>
    api.post<TryItResponse>(`/api/v1/portal/tryit/${encodeURIComponent(group)}/${encodeURIComponent(route)}`, body),
};

export const adminAPI = {
  listKeys:   () => api.get<{ keys: Key[] }>("/api/v1/admin/keys"),
  createKey:  (name: string, description?: string) => api.post<{ key: Key; plaintext: string }>("/api/v1/admin/keys", { name, description }),
  deleteKey:  (name: string) => api.delete<void>(`/api/v1/admin/keys/${encodeURIComponent(name)}`),
};
