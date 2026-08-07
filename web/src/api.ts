export type Observation = {
  id: string;
  title: string;
  content: string;
  type: string;
  project: string;
  scope: string;
  topic_key: string;
  confidence: number;
  source: string;
  session_id?: string;
  created_at: string;
  updated_at: string;
};

export type Session = {
  id: string;
  project: string;
  summary?: string;
  started_at: string;
};

export type Score = {
  observation_id: string;
  score: number;
  access_count: number;
  last_accessed?: string;
  updated_at: string;
};

export type ServerStats = {
  observations: number;
  sessions: number;
  active_sessions: number;
  edges: number;
  projects: number;
};
export type AuditEntry = { id: string; actor_subject: string; action: string; resource_type: string; resource_id: string; reason: string; allowed: boolean; created_at: string };

export type Principal = {
  id: string;
  type: string;
  org_id: string;
  workspaces: string[];
  projects: string[];
  roles: string[];
  scopes: string[];
  classification_clearance: string[];
  auth_method: string;
};

export type User = {
  id: string;
  email: string;
  display_name: string;
  active: boolean;
  roles: string[];
  workspaces: string[];
  projects: string[];
  scopes: string[];
  classification_clearance: string[];
  grant_version: number;
  created_at: string;
};

export type Token = {
  id: string;
  name: string;
  prefix: string;
  subject: string;
  principal_type: string;
  scopes: string[];
  workspaces: string[];
  expires_at?: string;
  revoked_at?: string;
  last_used_at?: string;
  secret?: string;
};

export type GraphNode = { id: string; kind: string; subtype?: string; label: string; project?: string; hop: number; metadata?: Record<string, unknown> };
export type GraphLink = { id: string; source: string; target: string; type: string; weight?: number; confidence?: number; assertion_kind?: string; assertion_status?: string; metadata?: Record<string, unknown> };
export type GraphSubgraph = { root: string; nodes: GraphNode[]; edges: GraphLink[]; truncated: boolean };

export class APIError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
    this.name = "APIError";
  }
}

export class CortexAPI {
  constructor(
    private readonly baseURL: string,
    private readonly token: string,
    private readonly onUnauthorized?: () => void,
  ) {}

  private async request<T>(path: string, init?: RequestInit): Promise<T> {
    const response = await fetch(`${this.baseURL.replace(/\/$/, "")}${path}`, {
      ...init,
      headers: {
        Authorization: `Bearer ${this.token}`,
        "Content-Type": "application/json",
        ...init?.headers,
      },
    });
    if (!response.ok) {
      const body = await response.json().catch(() => null) as { error?: { message?: string } } | null;
      if (response.status === 401) this.onUnauthorized?.();
      throw new APIError(body?.error?.message || `Request failed (${response.status})`, response.status);
    }
    if (response.status === 204) return undefined as T;
    return response.json() as Promise<T>;
  }

  health() { return this.request<{ status: string }>("/health"); }
  verifyAccess() { return this.stats(); }
  me() { return this.request<Principal>("/api/me"); }
  stats() { return this.request<ServerStats>("/api/stats"); }
  sessions(project = "") { return this.request<Session[]>(`/api/sessions${project ? `?project=${encodeURIComponent(project)}` : ""}`); }
  audit(limit = 25) { return this.request<AuditEntry[]>(`/api/audit?limit=${limit}`); }
  projects() { return this.request<string[]>("/api/projects"); }
  listObservations(params = "") { return this.request<Observation[]>(`/api/observations${params}`); }
  search(query: string, project = "") { return this.request<{ value: Observation[]; Count: number }>(`/api/search?q=${encodeURIComponent(query)}${project ? `&project=${encodeURIComponent(project)}` : ""}`); }
  getObservation(id: string) { return this.request<Observation>(`/api/observations/${id}`); }
  getScore(id: string) { return this.request<Score>(`/api/scores/${id}`); }
  related(id: string) { return this.request<{ value: Observation[]; Count: number }>(`/api/graph/${id}/related?depth=1`); }
  subgraph(id: string, depth = 2, maxNodes = 100) { return this.request<GraphSubgraph>(`/api/graph/${id}/subgraph?depth=${depth}&max_nodes=${maxNodes}`); }
  users() { return this.request<User[]>("/api/admin/users"); }
  createUser(input: { email: string; display_name: string; roles: string[] }) { return this.request<User>("/api/admin/users", { method: "POST", body: JSON.stringify(input) }); }
  setUserActive(id: string, active: boolean) { return this.request<void>(`/api/admin/users/${id}/${active ? "enable" : "disable"}`, { method: "POST" }); }
  tokens() { return this.request<Token[]>("/api/admin/tokens"); }
  issueToken(input: { subject: string; name: string; scopes?: string[]; expires_at?: string }) { return this.request<Token>("/api/admin/tokens", { method: "POST", body: JSON.stringify(input) }); }
  rotateToken(id: string) { return this.request<Token>(`/api/admin/tokens/${id}/rotate`, { method: "POST" }); }
  revokeToken(id: string) { return this.request<void>(`/api/admin/tokens/${id}`, { method: "DELETE" }); }
  createSession(input: Pick<Session, "project" | "summary">) { return this.request<Session>("/api/sessions", { method: "POST", body: JSON.stringify(input) }); }
  createObservation(input: Partial<Observation> & { session_id: string }) { return this.request<Observation>("/api/observations", { method: "POST", body: JSON.stringify(input) }); }
  updateObservation(id: string, input: Partial<Observation>) { return this.request<Observation>(`/api/observations/${id}`, { method: "PUT", body: JSON.stringify(input) }); }
  deleteObservation(id: string) { return this.request<void>(`/api/observations/${id}`, { method: "DELETE" }); }
}
