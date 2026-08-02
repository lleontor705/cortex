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

export class CortexAPI {
  constructor(private readonly baseURL: string, private readonly token: string) {}

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
      throw new Error(body?.error?.message || `Request failed (${response.status})`);
    }
    if (response.status === 204) return undefined as T;
    return response.json() as Promise<T>;
  }

  health() { return this.request<{ status: string }>("/health"); }
  stats() { return this.request<ServerStats>("/api/stats"); }
  sessions(project = "") { return this.request<Session[]>(`/api/sessions${project ? `?project=${encodeURIComponent(project)}` : ""}`); }
  audit(limit = 25) { return this.request<AuditEntry[]>(`/api/audit?limit=${limit}`); }
  projects() { return this.request<string[]>("/api/projects"); }
  listObservations(params = "") { return this.request<Observation[]>(`/api/observations${params}`); }
  search(query: string, project = "") { return this.request<{ value: Observation[]; Count: number }>(`/api/search?q=${encodeURIComponent(query)}${project ? `&project=${encodeURIComponent(project)}` : ""}`); }
  getObservation(id: string) { return this.request<Observation>(`/api/observations/${id}`); }
  getScore(id: string) { return this.request<Score>(`/api/scores/${id}`); }
  related(id: string) { return this.request<{ value: Observation[]; Count: number }>(`/api/graph/${id}/related?depth=1`); }
  createSession(input: Pick<Session, "project" | "summary">) { return this.request<Session>("/api/sessions", { method: "POST", body: JSON.stringify(input) }); }
  createObservation(input: Partial<Observation> & { session_id: string }) { return this.request<Observation>("/api/observations", { method: "POST", body: JSON.stringify(input) }); }
  updateObservation(id: string, input: Partial<Observation>) { return this.request<Observation>(`/api/observations/${id}`, { method: "PUT", body: JSON.stringify(input) }); }
  deleteObservation(id: string) { return this.request<void>(`/api/observations/${id}`, { method: "DELETE" }); }
}
