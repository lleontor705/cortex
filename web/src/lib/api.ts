export type Observation = {
  id: string;
  title: string;
  content: string;
  type: string;
  project: string;
  scope: string;
  topic_key?: string;
  confidence: number;
  source: string;
  session_id?: string;
  tags?: string[];
  created_at: string;
  updated_at: string;
};

export type Session = {
  id: string;
  project: string;
  summary?: string;
  started_at: string;
};

export type ServerStats = {
  observations: number;
  sessions: number;
  active_sessions: number;
  edges: number;
  projects: number;
};

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

export type AuditEntry = {
  id: string;
  actor_subject: string;
  action: string;
  resource_type: string;
  resource_id: string;
  reason: string;
  allowed: boolean;
  created_at: string;
};

export type GraphNode = {
  id: string;
  kind: string;
  subtype?: string;
  label: string;
  project?: string;
  hop: number;
  metadata?: Record<string, unknown>;
};

export type GraphLink = {
  id: string;
  source: string;
  target: string;
  type: string;
  weight?: number;
  confidence?: number;
  assertion_kind?: string;
  assertion_status?: string;
  metadata?: Record<string, unknown>;
};

export type GraphSubgraph = {
  root: string;
  nodes: GraphNode[];
  edges: GraphLink[];
  truncated: boolean;
};

export type ExtractedObservation = {
  title: string;
  content: string;
  type: string;
  project: string;
  scope: string;
  confidence: number;
  tags: string[];
  entities?: string[];
};

export type ExtractedEdge = {
  from_title: string;
  to_title: string;
  relation_type: string;
  reasoning: string;
  confidence: number;
};

export type ExtractionResult = {
  observations: ExtractedObservation[];
  edges: ExtractedEdge[];
  summary: string;
  source_method: string;
  extracted_at: string;
};

export type SynthesisResult = {
  project: string;
  summary: string;
  key_decisions: string[];
  patterns: string[];
  open_issues: string[];
  synthesized_at: string;
};

export class APIError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
    this.name = "APIError";
  }
}

export class CortexClient {
  constructor(
    private readonly baseURL: string,
    private readonly token: string,
    private readonly onUnauthorized?: () => void,
  ) {}

  private async request<T>(path: string, init?: RequestInit): Promise<T> {
    const cleanBase = this.baseURL.replace(/\/$/, "");
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      ...(init?.headers as Record<string, string>),
    };
    if (this.token) {
      headers["Authorization"] = `Bearer ${this.token}`;
    }

    const response = await fetch(`${cleanBase}${path}`, {
      ...init,
      headers,
    });

    if (!response.ok) {
      const body = (await response.json().catch(() => null)) as {
        error?: { message?: string; code?: string };
      } | null;
      if (response.status === 401) {
        this.onUnauthorized?.();
      }
      throw new APIError(
        body?.error?.message || `Error ${response.status}: Request failed`,
        response.status,
      );
    }

    if (response.status === 204) return undefined as T;
    return response.json() as Promise<T>;
  }

  health() {
    return this.request<{ status: string }>("/health");
  }

  me() {
    return this.request<Principal>("/api/me");
  }

  stats() {
    return this.request<ServerStats>("/api/stats");
  }

  projects() {
    return this.request<string[]>("/api/projects");
  }

  sessions(project = "") {
    return this.request<Session[]>(
      `/api/sessions${project ? `?project=${encodeURIComponent(project)}` : ""}`,
    );
  }

  createSession(data: { project: string; summary?: string }) {
    return this.request<Session>("/api/sessions", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  listObservations(params = "") {
    return this.request<Observation[]>(`/api/observations${params}`);
  }

  getObservation(id: string) {
    return this.request<Observation>(`/api/observations/${id}`);
  }

  createObservation(data: Partial<Observation> & { session_id?: string }) {
    return this.request<Observation>("/api/observations", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  updateObservation(id: string, data: Partial<Observation>) {
    return this.request<Observation>(`/api/observations/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  deleteObservation(id: string) {
    return this.request<void>(`/api/observations/${id}`, {
      method: "DELETE",
    });
  }

  search(query: string, project = "") {
    return this.request<{ value: Observation[]; Count: number }>(
      `/api/search?q=${encodeURIComponent(query)}${project ? `&project=${encodeURIComponent(project)}` : ""}`,
    );
  }

  subgraph(id: string, depth = 2, maxNodes = 100) {
    return this.request<GraphSubgraph>(
      `/api/graph/${id}/subgraph?depth=${depth}&max_nodes=${maxNodes}`,
    );
  }

  related(id: string, depth = 1) {
    return this.request<{ value: Observation[]; Count: number }>(
      `/api/graph/${id}/related?depth=${depth}`,
    );
  }

  createEdge(data: {
    from_id: string;
    to_id: string;
    relation_type: string;
    weight?: number;
    confidence?: number;
    reasoning?: string;
  }) {
    return this.request<any>("/api/graph/edges", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  resolveConflict(data: {
    new_observation_id: string;
    obsolete_observation_id: string;
    reason: string;
  }) {
    return this.request<any>("/api/graph/resolve", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  extract(data: {
    text: string;
    project: string;
    session_id?: string;
    llm_config?: {
      provider?: string;
      base_url?: string;
      api_key?: string;
      model?: string;
    };
  }) {
    return this.request<ExtractionResult>("/api/extract", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  synthesize(data: {
    project: string;
    observations: Observation[];
    llm_config?: {
      provider?: string;
      base_url?: string;
      api_key?: string;
      model?: string;
    };
  }) {
    return this.request<SynthesisResult>("/api/synthesize", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  users() {
    return this.request<User[]>("/api/admin/users");
  }

  createUser(data: {
    email: string;
    display_name: string;
    roles: string[];
    workspaces?: string[];
    projects?: string[];
    scopes?: string[];
  }) {
    return this.request<User>("/api/admin/users", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  setUserActive(id: string, active: boolean) {
    return this.request<void>(
      `/api/admin/users/${id}/${active ? "enable" : "disable"}`,
      { method: "POST" },
    );
  }

  tokens() {
    return this.request<Token[]>("/api/admin/tokens");
  }

  issueToken(data: {
    subject: string;
    name: string;
    scopes?: string[];
    expires_at?: string;
  }) {
    return this.request<Token>("/api/admin/tokens", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  rotateToken(id: string) {
    return this.request<Token>(`/api/admin/tokens/${id}/rotate`, {
      method: "POST",
    });
  }

  revokeToken(id: string) {
    return this.request<void>(`/api/admin/tokens/${id}`, {
      method: "DELETE",
    });
  }

  audit(limit = 50) {
    return this.request<AuditEntry[]>(`/api/audit?limit=${limit}`);
  }
}
