import { validateBearerDestination } from "./transport-policy";

export type Observation = {
  id: string;
  title: string;
  content: string;
  type: string;
  project: string;
  scope: string;
  owner_subject?: string;
  topic_key?: string;
  confidence: number;
  source: string;
  session_id?: string;
  tags?: string[];
  created_at: string;
  updated_at: string;
  has_embedding?: boolean;
  embedding_model?: string;
  embedding_dimensions?: number;
  rag_status?: "indexed" | "pending" | "failed" | "unindexed";
};

export type RAGStats = {
  project: string;
  total_observations: number;
  indexed_observations: number;
  pending_observations: number;
  failed_observations: number;
  coverage_pct: number;
  embedding_model: string;
  embedding_dimensions: number;
  vector_provider: string;
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
  /** Server-confirmed workspace selected for this request session. */
  workspace_id?: string;
  display_name?: string;
  email?: string;
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

export type Community = {
  id: number;
  label: string;
  hub_node_id: string;
  members: string[];
  size: number;
  cohesion_score: number;
};

export type GodNode = {
  id: string;
  name?: string;
  label?: string;
  kind?: string;
  file_path?: string;
  degree: number;
  in_degree: number;
  out_degree: number;
  score?: number;
  source_file?: string;
};

export type SurprisingConnection = {
  source_node: string;
  target_node: string;
  relation_type: string;
  score: number;
  reasons: string[];
};

export type DependencyCycle = {
  length: number;
  nodes: string[];
};

export type BlastRadiusResult = {
  root_node: string;
  direct_impact: string[];
  total_impacted: string[];
  impacted_files: string[];
  blast_radius_pct: number;
};

export type CommunitySummary = {
  community_id: number;
  label: string;
  hub_node_id: string;
  hub_node_label: string;
  member_count: number;
  cohesion_score: number;
  key_symbols: string[];
  external_deps: string[];
  summary_markdown: string;
};

export type GraphAnalyticsReport = {
  total_nodes: number;
  total_edges: number;
  density: number;
  communities: Community[];
  community_summaries?: CommunitySummary[];
  god_nodes: GodNode[];
  surprising_connections: SurprisingConnection[];
  cycles: DependencyCycle[];
  ppr_scores?: Record<string, number>;
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

export type ProjectRule = {
  key: string;
  title: string;
  content: string;
  scope: string;
};

export type ProjectSkillSummary = {
  key: string;
  title: string;
  description: string;
  scope: string;
  project?: string;
};

export type ProjectContext = {
  project: string;
  system_prompt: string;
  rules: ProjectRule[];
  skills: ProjectSkillSummary[];
};

export type ProjectDuplicateGroup = {
  canonical_name: string;
  variants: string[];
  total_count: number;
};

export type ProjectMergeResult = {
  source_project: string;
  target_project: string;
  observations_merged: number;
  sessions_merged: number;
  prompts_merged: number;
};

export type ProjectSkill = {
  id: string;
  key: string;
  title: string;
  description: string;
  content: string;
  scope: string;
  project?: string;
  parameters?: Record<string, unknown>;
  revision: number;
  updated_at: string;
};

export type ProjectArtifactItem = {
  id: string;
  kind: "rule" | "skill";
  key: string;
  title: string;
  description?: string;
  content: string;
  scope: "project" | "workspace_default";
  project?: string;
  parameters?: Record<string, unknown>;
  revision: number;
  status: "active" | "deleted";
  updated_at: string;
};

export type SaveProjectArtifactInput = {
  kind: "rule" | "skill";
  key: string;
  title: string;
  description?: string;
  content: string;
  scope: "project" | "workspace_default";
  project?: string;
  parameters?: Record<string, unknown>;
};

export type AgentProject = { id: string; label: string };
export type AgentMessage = { role: "user" | "assistant"; content: string };
export type AgentSource = {
  handle: string;
  type: "memory" | "code";
  title: string;
  path?: string;
  line_start?: number;
  line_end?: number;
};
export type AgentConfidence = { level: "low" | "medium" | "high"; score: number };
export type AgentRetrievalTier =
  | "direct_factual"
  | "semantic_hybrid"
  | "multi_hop_graph"
  | "architectural_global";
export type AgentRetrievalStage = {
  name:
    | "lexical"
    | "dense"
    | "rrf_maxsim"
    | "graph_ppr"
    | "community_summary"
    | "code"
    | "crag";
  status: "ok" | "degraded" | "skipped";
  count: number;
};
export type AgentRetrieval = {
  tier?: AgentRetrievalTier;
  stages?: AgentRetrievalStage[];
  refinement_count?: number;
  generation?: string;
  degraded: string[];
  invalid_citations?: number;
};
export type AgentAnswer = {
  answer: string;
  sources: AgentSource[];
  confidence: AgentConfidence;
  retrieval: AgentRetrieval;
};
export type AgentRequest = {
  project_id: string;
  question: string;
  history?: AgentMessage[];
};
export type AgentStreamEvent =
  | { type: "meta"; data: { confidence?: AgentConfidence; retrieval: AgentRetrieval } }
  | { type: "delta"; data: { text: string } }
  | { type: "sources"; data: AgentSource[] }
  | { type: "done"; data: AgentAnswer };


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
  private token: string;
  // A convenience selector only. The server validates it against the
  // verified principal's grants; this client never sends a tenant id.
  private workspaceID = "";
  private readonly inflight = new Set<AbortController>();

  constructor(
    private readonly baseURL: string,
    token: string,
    private readonly onUnauthorized?: () => void,
  ) {
    this.token = token;
  }

  /**
   * Clears the live token reference and aborts every in-flight request.
   * Called on logout and automatically on any 401 response, so stale
   * credentials are never reused and pending requests do not outlive the
   * session.
   */
  invalidate(): void {
    this.token = "";
    this.workspaceID = "";
    for (const controller of this.inflight) {
      controller.abort();
    }
    this.inflight.clear();
  }

  /**
   * Sets the workspace selector for future requests. Authorization remains
   * server-side: an ungranted value receives a 403 and never widens scope.
   */
  setWorkspace(workspaceID: string): void {
    this.workspaceID = workspaceID.trim();
  }

  private async request<T>(path: string, init?: RequestInit): Promise<T> {
    const cleanBase = this.baseURL.replace(/\/$/, "");
    if (this.token) {
      // Bearer credentials may only travel over HTTPS or plain HTTP to a
      // strict loopback destination. Enforced before any request is issued
      // so the Authorization header can never leak onto the wire.
      validateBearerDestination(cleanBase);
    }
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      ...(init?.headers as Record<string, string>),
    };
    if (this.token) {
      headers["Authorization"] = `Bearer ${this.token}`;
    }
    if (this.workspaceID) {
      headers["X-Cortex-Workspace"] = this.workspaceID;
    }

    const controller = new AbortController();
    const abort = () => controller.abort(init?.signal?.reason);
    if (init?.signal?.aborted) abort();
    else init?.signal?.addEventListener("abort", abort, { once: true });
    this.inflight.add(controller);
    try {
      const response = await fetch(`${cleanBase}${path}`, {
        ...init,
        headers,
        signal: controller.signal,
      });

      if (!response.ok) {
        const body = (await response.json().catch(() => null)) as {
          error?: { message?: string; code?: string };
        } | null;
        if (response.status === 401) {
          // Abort sibling requests and drop the token reference before
          // notifying, so nothing reuses the rejected credentials.
          this.invalidate();
          this.onUnauthorized?.();
        }
        throw new APIError(
          body?.error?.message || `Error ${response.status}: Request failed`,
          response.status,
        );
      }

      if (response.status === 204) return undefined as T;
      return response.json() as Promise<T>;
    } finally {
      init?.signal?.removeEventListener("abort", abort);
      this.inflight.delete(controller);
    }
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

  async agentProjects(signal?: AbortSignal): Promise<AgentProject[]> {
    const response = await this.request<{ projects: AgentProject[] }>("/api/agent/projects", {
      signal,
    });
    return response.projects;
  }

  answerAgent(input: AgentRequest, signal?: AbortSignal): Promise<AgentAnswer> {
    return this.request<AgentAnswer>("/api/agent/answer", {
      method: "POST",
      body: JSON.stringify(input),
      signal,
    });
  }

  async streamAgent(
    input: AgentRequest,
    onEvent: (event: AgentStreamEvent) => void,
    signal?: AbortSignal,
    lastEventId?: string,
  ): Promise<AgentAnswer> {
    const cleanBase = this.baseURL.replace(/\/$/, "");
    if (this.token) validateBearerDestination(cleanBase);
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      Accept: "text/event-stream",
    };
    if (this.token) headers.Authorization = `Bearer ${this.token}`;
    if (this.workspaceID) headers["X-Cortex-Workspace"] = this.workspaceID;
    if (lastEventId) headers["Last-Event-ID"] = lastEventId;

    const controller = new AbortController();
    const abort = () => controller.abort(signal?.reason);
    if (signal?.aborted) abort();
    else signal?.addEventListener("abort", abort, { once: true });
    this.inflight.add(controller);
    try {
      const response = await fetch(`${cleanBase}/api/agent/stream`, {
        method: "POST",
        headers,
        body: JSON.stringify(input),
        signal: controller.signal,
      });
      if (!response.ok) {
        const body = (await response.json().catch(() => null)) as {
          error?: { message?: string };
        } | null;
        if (response.status === 401) {
          this.invalidate();
          this.onUnauthorized?.();
        }
        throw new APIError(
          body?.error?.message || `Error ${response.status}: Request failed`,
          response.status,
        );
      }
      if (!response.body) throw new APIError("Agent stream is unavailable", 503);

      let completed: AgentAnswer | undefined;
      for await (const event of parseAgentSSE(response.body)) {
        if (event.type === "error") {
          throw new APIError(event.data.message || "Agent stream failed", event.data.status || 503);
        }
        const typed = event as AgentStreamEvent;
        onEvent(typed);
        if (typed.type === "done") completed = typed.data;
      }
      if (!completed) throw new APIError("Agent stream ended before completion", 503);
      return completed;
    } finally {
      signal?.removeEventListener("abort", abort);
      this.inflight.delete(controller);
    }
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

  async search(query: string, project = ""): Promise<{ value: Observation[]; Count: number }> {
    const filters = `q=${encodeURIComponent(query)}${project ? `&project=${encodeURIComponent(project)}` : ""}`;
    let raw: any;
    try {
      // Force the semantic tier so Web searches exploit embeddings whenever
      // the connected Cortex runtime has a healthy vector index.
      raw = await this.request<any>(`/api/search/hybrid?${filters}&mode=semantic`);
    } catch (error) {
      // Older server-mode deployments do not expose the hybrid route yet.
      // Preserve compatibility without hiding genuine backend failures.
      if (!(error instanceof APIError) || error.status !== 404) throw error;
      raw = await this.request<any>(`/api/search?${filters}`);
    }
    if (Array.isArray(raw)) {
      return { value: raw, Count: raw.length };
    }
    if (raw && Array.isArray(raw.results)) {
      return { value: raw.results, Count: raw.count ?? raw.results.length };
    }
    if (raw && Array.isArray(raw.value)) {
      return { value: raw.value, Count: raw.Count ?? raw.value.length };
    }
    return { value: [], Count: 0 };
  }

  subgraph(id: string, depth = 2, maxNodes = 100) {
    const cleanId = (id || "").replace(/^(observation|session|entity):/, "");
    return this.request<GraphSubgraph>(
      `/api/graph/${encodeURIComponent(cleanId)}/subgraph?depth=${depth}&max_nodes=${maxNodes}`,
    );
  }

  related(id: string, depth = 1) {
    const cleanId = (id || "").replace(/^(observation|session|entity):/, "");
    return this.request<{ value: Observation[]; Count: number }>(
      `/api/graph/${encodeURIComponent(cleanId)}/related?depth=${depth}`,
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

  projectGraph(project?: string, limit = 150) {
    const params = new URLSearchParams();
    if (project) params.set("project", project);
    params.set("limit", limit.toString());
    return this.request<GraphSubgraph>(`/api/graph/project-graph?${params.toString()}`);
  }

  analytics(project?: string, limit = 100, query?: string) {
    const params = new URLSearchParams();
    if (project) params.set("project", project);
    params.set("limit", limit.toString());
    if (query) params.set("query", query);
    return this.request<GraphAnalyticsReport>(`/api/graph/analytics?${params.toString()}`);
  }

  blastRadius(nodeId: string, depth = 3) {
    return this.request<BlastRadiusResult>(
      `/api/graph/blast-radius?node_id=${encodeURIComponent(nodeId)}&depth=${depth}`,
    );
  }

  ingestCode(data: { directory?: string; project?: string; max_files?: number }) {
    return this.request<any>("/api/graph/ingest-code", {
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

  getProjectContext(project = "") {
    return this.request<ProjectContext>(
      `/api/projects/context${project ? `?project=${encodeURIComponent(project)}` : ""}`,
    );
  }

  listProjectArtifacts(project = "", kind = "") {
    const params = new URLSearchParams();
    if (project) params.set("project", project);
    if (kind) params.set("kind", kind);
    const qs = params.toString();
    return this.request<ProjectArtifactItem[]>(
      `/api/projects/artifacts${qs ? `?${qs}` : ""}`,
    );
  }

  saveProjectArtifact(data: SaveProjectArtifactInput) {
    return this.request<ProjectArtifactItem>("/api/projects/artifacts", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  deleteProjectArtifact(id: string, reason = "Deleted by admin") {
    return this.request<{ status: string }>(
      `/api/projects/artifacts/${id}?reason=${encodeURIComponent(reason)}`,
      {
        method: "DELETE",
      },
    );
  }

  getAIStatus() {
    return this.request<{
      llm: {
        provider: string;
        model: string;
        base_url: string;
        configured: boolean;
      };
      embedding: {
        provider: string;
        model: string;
        base_url: string;
        dimensions: number;
        configured: boolean;
      };
    }>("/api/admin/ai/status");
  }

  testLLM() {
    return this.request<{
      status: "ok" | "error" | "not_configured";
      provider: string;
      model: string;
      latency_ms: number;
      response?: string;
      error?: string;
      message?: string;
    }>("/api/admin/ai/test-llm", {
      method: "POST",
    });
  }

  testEmbedding() {
    return this.request<{
      status: "ok" | "error" | "not_configured";
      provider: string;
      model: string;
      dimensions?: number;
      latency_ms: number;
      sample_vector?: number[];
      error?: string;
      message?: string;
    }>("/api/admin/ai/test-embedding", {
      method: "POST",
    });
  }

  getProjectDuplicates() {
    return this.request<ProjectDuplicateGroup[]>("/api/projects/duplicates");
  }

  mergeProject(sourceProject: string, targetProject: string) {
    return this.request<ProjectMergeResult>("/api/projects/merge", {
      method: "POST",
      body: JSON.stringify({
        source_project: sourceProject,
        target_project: targetProject,
      }),
    });
  }

  getCodeSymbols(params?: {
    project?: string;
    file?: string;
    kind?: string;
    package?: string;
    q?: string;
    limit?: number;
    offset?: number;
  }) {
    const q = new URLSearchParams();
    if (params?.project) q.set("project", params.project);
    if (params?.file) q.set("file", params.file);
    if (params?.kind) q.set("kind", params.kind);
    if (params?.package) q.set("package", params.package);
    if (params?.q) q.set("q", params.q);
    if (params?.limit) q.set("limit", String(params.limit));
    if (params?.offset) q.set("offset", String(params.offset));
    return this.request<CodeSymbol[]>(`/api/code/symbols?${q.toString()}`);
  }

  getCodeGraph(project?: string) {
    const q = new URLSearchParams();
    if (project) q.set("project", project);
    return this.request<CodeGraph>(`/api/code/graph?${q.toString()}`);
  }

  getCodeAnalytics(project?: string) {
    const q = new URLSearchParams();
    if (project) q.set("project", project);
    return this.request<CodeAnalyticsReport>(`/api/code/analytics?${q.toString()}`);
  }

  ingestCodeAST(data: { path: string; project: string; max_files?: number }) {
    return this.request<CodeAnalyticsReport>("/api/code/ingest", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  getRAGStats(project?: string) {
    const q = new URLSearchParams();
    if (project) q.set("project", project);
    return this.request<RAGStats>(`/api/rag/stats?${q.toString()}`);
  }
}

export type CodeSymbol = {
  id: string;
  project: string;
  file_path: string;
  line_number: number;
  kind: string;
  name: string;
  package_name?: string;
  signature?: string;
  doc_summary?: string;
  file_hash?: string;
  created_at: string;
  updated_at: string;
};

export type CodeRelation = {
  id?: number;
  project: string;
  source_id: string;
  target_id: string;
  relation: string;
  confidence: number;
  reasoning?: string;
  created_at: string;
};

export type CodeGraph = {
  project: string;
  symbols: CodeSymbol[];
  relations: CodeRelation[];
};

export type ImportCycle = {
  id: string;
  nodes: string[];
  length: number;
};

export type CommunityCohesion = {
  name: string;
  symbol_count: number;
  internal_edges: number;
  cohesion_score: number;
};

export type CodeAnalyticsReport = {
  project: string;
  total_symbols: number;
  total_relations: number;
  total_files: number;
  average_cohesion: number;
  god_nodes: GodNode[];
  import_cycles: ImportCycle[];
  communities: CommunityCohesion[];
  generated_at: string;
};

type ParsedAgentSSEEvent = (AgentStreamEvent & { id?: string }) | {
  type: "error";
  id?: string;
  data: { status?: number; code?: string; message?: string };
};

async function* parseAgentSSE(
  stream: ReadableStream<Uint8Array>,
): AsyncGenerator<ParsedAgentSSEEvent> {
  const reader = stream.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  const parseBlock = (block: string): ParsedAgentSSEEvent | undefined => {
    let eventName = "";
    let eventId = "";
    const data: string[] = [];
    for (const line of block.split(/\r\n|\r|\n/)) {
      if (!line || line.startsWith(":")) continue;
      if (line.startsWith("id:")) eventId = line.slice(3).trim();
      if (line.startsWith("event:")) eventName = line.slice(6).trim();
      if (line.startsWith("data:")) data.push(line.slice(5).replace(/^ /, ""));
    }
    if (!eventName || data.length === 0) return undefined;
    if (!["meta", "delta", "sources", "done", "error"].includes(eventName)) {
      return undefined;
    }
    try {
      const parsed = { type: eventName, data: JSON.parse(data.join("\n")) } as ParsedAgentSSEEvent;
      if (eventId) parsed.id = eventId;
      return parsed;
    } catch {
      throw new APIError("Agent stream contained invalid data", 503);
    }
  };

  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      let match = /\r\n\r\n|\n\n|\r\r/.exec(buffer);
      while (match) {
        const block = buffer.slice(0, match.index);
        buffer = buffer.slice(match.index + match[0].length);
        const event = parseBlock(block);
        if (event) yield event;
        match = /\r\n\r\n|\n\n|\r\r/.exec(buffer);
      }
    }
    buffer += decoder.decode();
    const event = parseBlock(buffer);
    if (event) yield event;
  } finally {
    reader.releaseLock();
  }
}


