import { FormEvent, useEffect, useRef, useState } from "react";
import cytoscape, { type Core } from "cytoscape";
import {
  AuditEntry,
  CortexAPI,
  GraphSubgraph,
  Observation,
  Principal,
  Score,
  ServerStats,
  Session,
} from "./api";
import { AdministrationPanel, isAdminPrincipal } from "./AdministrationPanel";
import { normalizeServerURL } from "./auth";

const configuredURL = import.meta.env.VITE_API_URL?.trim() || "";
const defaultURL = configuredURL || "http://localhost:7438";
const serverLocked = configuredURL !== "";
const tokenKey = "cortex.web.token";
const urlKey = "cortex.web.url";
type WorkspaceView = "memories" | "graph" | "admin" | "security";

const viewTitles: Record<
  WorkspaceView,
  { title: string; description: string }
> = {
  memories: {
    title: "Memories",
    description: "Search, inspect, and curate durable context.",
  },
  graph: {
    title: "Graph",
    description: "Explore the neighborhood around the selected memory.",
  },
  admin: {
    title: "Administration",
    description: "Manage attributable users and internal credentials.",
  },
  security: {
    title: "Security",
    description: "Review recent authorization decisions.",
  },
};

export function App() {
  const [api, setApi] = useState<CortexAPI | null>(null);
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState("");
  const [observations, setObservations] = useState<Observation[]>([]);
  const [selected, setSelected] = useState<Observation | null>(null);
  const [score, setScore] = useState<Score | null>(null);
  const [related, setRelated] = useState<Observation[]>([]);
  const [creating, setCreating] = useState(false);
  const [query, setQuery] = useState("");
  const [project, setProject] = useState("");
  const [loading, setLoading] = useState(false);
  const [stats, setStats] = useState<ServerStats | null>(null);
  const [audit, setAudit] = useState<AuditEntry[]>([]);
  const [auditLoading, setAuditLoading] = useState(false);
  const [auditLoaded, setAuditLoaded] = useState(false);
  const [projects, setProjects] = useState<string[]>([]);
  const [principal, setPrincipal] = useState<Principal | null>(null);
  const [graph, setGraph] = useState<GraphSubgraph | null>(null);
  const [view, setView] = useState<WorkspaceView>("memories");
  const restoredSession = useRef(false);

  const load = async (
    client: CortexAPI,
    search = "",
    projectFilter = project,
  ) => {
    setLoading(true);
    setError("");
    try {
      const filters = projectFilter
        ? `&project=${encodeURIComponent(projectFilter)}`
        : "";
      const [result, serverStats, visibleProjects] = await Promise.all([
        search.trim()
          ? client
              .search(search, projectFilter)
              .then((response) => response.value)
          : client.listObservations(`?limit=50${filters}`),
        client.stats(),
        client.projects(),
      ]);
      setObservations(result || []);
      setStats(serverStats);
      setProjects(visibleProjects || []);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Could not load Cortex data",
      );
    } finally {
      setLoading(false);
    }
  };

  const expireSession = () => {
    sessionStorage.removeItem(tokenKey);
    setApi(null);
    setPrincipal(null);
    setConnected(false);
    setError("Your session is invalid or expired. Enter a valid bearer token.");
  };

  const connect = async (url: string, token: string) => {
    setError("");
    try {
      const normalizedURL = normalizeServerURL(serverLocked ? defaultURL : url);
      const client = new CortexAPI(normalizedURL, token, expireSession);
      await client.verifyAccess();
      const currentPrincipal = await client.me();
      localStorage.setItem(urlKey, normalizedURL);
      sessionStorage.setItem(tokenKey, token);
      setApi(client);
      setConnected(true);
      setPrincipal(currentPrincipal);
      await load(client);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Could not connect to Cortex",
      );
    }
  };

  useEffect(() => {
    if (restoredSession.current) return;
    restoredSession.current = true;
    localStorage.removeItem(tokenKey);
    const token = sessionStorage.getItem(tokenKey);
    if (token) void connect(localStorage.getItem(urlKey) || defaultURL, token);
  }, []);

  if (!api || !connected)
    return (
      <Connect onConnect={connect} error={error} serverLocked={serverLocked} />
    );

  const openObservation = async (observation: Observation) => {
    setCreating(false);
    setSelected(observation);
    setScore(null);
    setRelated([]);
    setGraph(null);
    try {
      const [full, importance, relatedGraph, subgraph] = await Promise.all([
        api.getObservation(observation.id),
        api.getScore(observation.id),
        api.related(observation.id),
        api.subgraph(observation.id),
      ]);
      setSelected(full);
      setScore(importance);
      setRelated(relatedGraph.value || []);
      setGraph(subgraph);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Could not load observation",
      );
    }
  };

  const disconnect = () => {
    sessionStorage.removeItem(tokenKey);
    setApi(null);
    setPrincipal(null);
    setConnected(false);
    setError("");
  };
  const adminAllowed = isAdminPrincipal(principal);
  const loadAudit = async () => {
    if (!adminAllowed) return;
    setAuditLoading(true);
    try {
      setAudit(await api.audit());
      setAuditLoaded(true);
    } catch (err) {
      setError(
        err instanceof Error
          ? `Could not load audit trail: ${err.message}`
          : "Could not load audit trail",
      );
    } finally {
      setAuditLoading(false);
    }
  };
  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <span className="brand-mark">C</span>
          <span>
            CORTEX<span className="muted"> / CONTROL</span>
          </span>
        </div>
        <nav aria-label="Workspace">
          <NavItem
            active={view === "memories"}
            label="Memories"
            meta={stats?.observations ?? observations.length}
            onClick={() => setView("memories")}
          />
          <NavItem
            active={view === "graph"}
            label="Graph"
            meta={stats?.edges ?? 0}
            onClick={() => setView("graph")}
          />
          <NavItem
            active={view === "admin"}
            label="Administration"
            meta={adminAllowed ? "Admin" : "View"}
            onClick={() => setView("admin")}
          />
          <NavItem
            active={view === "security"}
            label="Security"
            onClick={() => setView("security")}
          />
        </nav>
        <div className="sidebar-bottom">
          <div className="connection">
            <i /> SERVER ONLINE
            <span>{localStorage.getItem(urlKey) || defaultURL}</span>
          </div>
          <button className="disconnect" onClick={disconnect}>
            Disconnect
          </button>
        </div>
      </aside>
      <main className="main">
        <header className="topbar">
          <div>
            <h1>{viewTitles[view].title}</h1>
            <p className="topbar-copy">{viewTitles[view].description}</p>
          </div>
          <div className="top-actions">
            <span className="principal-name">{principal?.id.slice(0, 8)}</span>
            <span className="session-badge">
              {principal?.roles[0] || "member"}
            </span>
          </div>
        </header>
        {error && (
          <div className="alert" role="status" aria-live="polite">
            {error}
            <button onClick={() => setError("")}>Dismiss</button>
          </div>
        )}
        <div className="metrics" aria-label="Workspace metrics">
          <Metric
            value={stats?.observations ?? observations.length}
            label="memories"
          />
          <Metric value={stats?.active_sessions ?? 0} label="active sessions" />
          <Metric value={stats?.projects ?? 0} label="projects" />
          <Metric value={stats?.edges ?? 0} label="relationships" />
        </div>
        {view === "memories" && (
          <section className="workspace-grid">
            <div className="panel library">
              <div className="panel-head">
                <div>
                  <p className="eyebrow">MEMORY LIBRARY</p>
                  <h2>
                    {query ? `Results for “${query}”` : "Recent observations"}
                  </h2>
                </div>
                <div className="library-actions">
                  <span className="count">{observations.length} ITEMS</span>
                  <button
                    className="primary small"
                    onClick={() => {
                      setCreating(true);
                      setSelected(null);
                      setScore(null);
                    }}
                  >
                    New memory <span>+</span>
                  </button>
                </div>
              </div>
              <form
                className="search"
                onSubmit={(event) => {
                  event.preventDefault();
                  void load(api, query);
                }}
              >
                <span>⌕</span>
                <input
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder="Search memories..."
                />
                <select
                  value={project}
                  onChange={(event) => {
                    const nextProject = event.target.value;
                    setProject(nextProject);
                    void load(api, query, nextProject);
                  }}
                >
                  <option value="">All projects</option>
                  {projects.map((item) => (
                    <option key={item} value={item}>
                      {item}
                    </option>
                  ))}
                </select>
                <kbd>ENTER</kbd>
              </form>
              <div className="list">
                {loading ? (
                  <div className="empty">Loading memory index...</div>
                ) : observations.length === 0 ? (
                  <div className="empty">No memories found.</div>
                ) : (
                  observations.map((item) => (
                    <button
                      className={`memory-row ${selected?.id === item.id ? "selected" : ""}`}
                      key={item.id}
                      onClick={() => void openObservation(item)}
                    >
                      <span className="type-dot" />
                      <span className="memory-copy">
                        <strong>{item.title || "Untitled observation"}</strong>
                        <small>
                          {item.project || "unassigned"} ·{" "}
                          {formatDate(item.created_at)}
                        </small>
                      </span>
                      <span className="memory-type">{item.type || "note"}</span>
                      <span className="chevron">›</span>
                    </button>
                  ))
                )}
              </div>
            </div>
            <Detail
              observation={selected}
              creating={creating}
              score={score}
              related={related}
              api={api}
              onSaved={(item) => {
                setCreating(false);
                setSelected(item);
                void load(api, query);
              }}
              onDeleted={() => {
                setCreating(false);
                setSelected(null);
                setScore(null);
                setRelated([]);
                void load(api, query);
              }}
            />
          </section>
        )}
        {view === "graph" && (
          <GraphCanvas
            graph={graph}
            onSelect={(id) => {
              void api
                .getObservation(id)
                .then(openObservation)
                .catch((err) =>
                  setError(
                    err instanceof Error
                      ? err.message
                      : "Could not load graph node",
                  ),
                );
            }}
          />
        )}
        {view === "admin" && principal && (
          <AdministrationPanel
            api={api}
            principal={principal}
            onMessage={setError}
          />
        )}
        {view === "security" && (
          <AuditPanel
            entries={audit}
            loading={auditLoading}
            loaded={auditLoaded}
            allowed={adminAllowed}
            onLoad={() => void loadAudit()}
          />
        )}
      </main>
    </div>
  );
}

function Connect({
  onConnect,
  error,
  serverLocked,
}: {
  onConnect: (url: string, token: string) => Promise<void>;
  error: string;
  serverLocked: boolean;
}) {
  const [url, setURL] = useState(localStorage.getItem(urlKey) || defaultURL);
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    await onConnect(url, token);
    setBusy(false);
  };
  return (
    <div className="connect-page">
      <section className="connect-card">
        <div className="brand">
          <span className="brand-mark">C</span>
          <span>CORTEX</span>
        </div>
        <div className="connect-copy">
          <h1>Connect to Cortex</h1>
          <p>Enter a bearer token to open the published workspace.</p>
        </div>
        <form onSubmit={submit}>
          {!serverLocked && (
            <label>
              Server URL
              <input
                type="url"
                value={url}
                onChange={(event) => setURL(event.target.value)}
                placeholder="http://localhost:7438"
                required
              />
            </label>
          )}
          <label>
            Bearer token
            <input
              type="password"
              value={token}
              onChange={(event) => setToken(event.target.value)}
              placeholder="Paste your production token"
              autoComplete="off"
              required
              autoFocus
            />
          </label>
          {error && (
            <div className="form-error" role="alert">
              {error}
            </div>
          )}
          <button className="primary" disabled={busy}>
            {busy ? "Connecting..." : "Continue"}
          </button>
        </form>
        <small className="privacy">
          Stored for this browser tab only · {serverLocked ? defaultURL : url}
        </small>
      </section>
    </div>
  );
}

function NavItem({
  active,
  label,
  meta,
  onClick,
}: {
  active: boolean;
  label: string;
  meta?: string | number;
  onClick: () => void;
}) {
  return (
    <button
      className={`nav-item ${active ? "active" : ""}`}
      onClick={onClick}
      aria-current={active ? "page" : undefined}
    >
      <span>{label}</span>
      {meta !== undefined && <small>{meta}</small>}
    </button>
  );
}

function Metric({ value, label }: { value: number; label: string }) {
  return (
    <span>
      <strong>{value}</strong> {label}
    </span>
  );
}

function Detail({
  observation,
  creating,
  score,
  related,
  api,
  onSaved,
  onDeleted,
}: {
  observation: Observation | null;
  creating: boolean;
  score: Score | null;
  related: Observation[];
  api: CortexAPI;
  onSaved: (item: Observation) => void;
  onDeleted: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState("");
  const [content, setContent] = useState("");
  const [type, setType] = useState("manual");
  const [sessionID, setSessionID] = useState("");
  const [sessions, setSessions] = useState<Session[]>([]);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  useEffect(() => {
    setTitle(observation?.title || "");
    setContent(observation?.content || "");
    setType(observation?.type || "manual");
    setSessionID(observation?.session_id || "");
    setEditing(creating);
    setMessage("");
  }, [observation, creating]);
  useEffect(() => {
    if (!creating) return;
    void api
      .sessions()
      .then((values) => {
        setSessions(values);
        setSessionID((current) => current || values[0]?.id || "");
      })
      .catch(() => setMessage("Sessions could not be loaded."));
  }, [api, creating]);
  if (!observation && !creating)
    return (
      <div className="panel detail empty-detail">
        <span className="detail-symbol">+</span>
        <p>Select a memory to inspect its full context.</p>
        <small>
          Search the library or choose an observation from the list.
        </small>
      </div>
    );
  const save = async () => {
    setBusy(true);
    try {
      const item = await api.updateObservation(observation?.id || "", {
        title,
        content,
      });
      onSaved(item);
      setEditing(false);
      setMessage("Saved");
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Save failed");
    } finally {
      setBusy(false);
    }
  };
  const create = async () => {
    setBusy(true);
    try {
      const item = await api.createObservation({
        title,
        content,
        type,
        session_id: sessionID,
      });
      onSaved(item);
      setMessage("Created");
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Create failed");
    } finally {
      setBusy(false);
    }
  };
  const remove = async () => {
    if (!observation || !confirm("Delete this observation permanently?"))
      return;
    setBusy(true);
    try {
      await api.deleteObservation(observation.id);
      onDeleted();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Delete failed");
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="panel detail">
      <div className="panel-head">
        <div>
          <p className="eyebrow">
            {creating ? "NEW OBSERVATION" : "OBSERVATION DETAIL"}
          </p>
          <h2>
            {editing
              ? creating
                ? "Create memory"
                : "Edit memory"
              : observation?.title}
          </h2>
        </div>
        <div className="detail-actions">
          {editing ? (
            <>
              <button
                onClick={() => (creating ? onDeleted() : setEditing(false))}
              >
                Cancel
              </button>
              <button
                className="primary small"
                onClick={() => void (creating ? create() : save())}
                disabled={busy}
              >
                {creating ? "Create" : "Save"}
              </button>
            </>
          ) : (
            <>
              <button onClick={() => setEditing(true)}>Edit</button>
              <button
                className="danger"
                onClick={() => void remove()}
                disabled={busy}
              >
                Delete
              </button>
            </>
          )}
        </div>
      </div>
      {editing ? (
        <div className="editor">
          {creating && (
            <label>
              Session
              <select
                value={sessionID}
                onChange={(event) => setSessionID(event.target.value)}
                required
              >
                <option value="">Select a session</option>
                {sessions.map((session) => (
                  <option key={session.id} value={session.id}>
                    {session.project || "Unassigned project"} ·{" "}
                    {formatDate(session.started_at)}
                  </option>
                ))}
              </select>
            </label>
          )}
          <label>
            Title
            <input
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              placeholder="What should be remembered?"
            />
          </label>
          <label>
            Type
            <select
              value={type}
              onChange={(event) => setType(event.target.value)}
            >
              <option value="decision">Decision</option>
              <option value="bugfix">Bug fix</option>
              <option value="pattern">Pattern</option>
              <option value="discovery">Discovery</option>
              <option value="learning">Learning</option>
              <option value="manual">Note</option>
            </select>
          </label>
          <label>
            Content
            <textarea
              value={content}
              onChange={(event) => setContent(event.target.value)}
              placeholder="Add the durable context..."
              rows={9}
            />
          </label>
        </div>
      ) : (
        <>
          <div className="tags">
            <span>{observation?.type || "note"}</span>
            <span>{observation?.project || "unassigned"}</span>
            <span>{observation?.scope || "project"}</span>
          </div>
          <p className="content">{observation?.content}</p>
          <div className="meta-grid">
            <Meta label="PUBLIC ID" value={observation?.id || ""} />
            <Meta
              label="CREATED"
              value={formatDate(observation?.created_at || "")}
            />
            <Meta label="SOURCE" value={observation?.source || "manual"} />
            <Meta
              label="CONFIDENCE"
              value={`${Math.round((observation?.confidence || 0) * 100)}%`}
            />
          </div>
          {score && (
            <div className="score">
              <span>IMPORTANCE SCORE</span>
              <strong>{score.score.toFixed(2)}</strong>
              <small>{score.access_count} accesses</small>
            </div>
          )}
          <div className="related">
            <div>
              <span className="eyebrow">CONNECTED MEMORIES</span>
              <strong>{related.length.toString().padStart(2, "0")}</strong>
            </div>
            {related.length ? (
              related.slice(0, 4).map((item) => (
                <div className="related-row" key={item.id}>
                  <span className="type-dot" />
                  <span>{item.title}</span>
                </div>
              ))
            ) : (
              <small>No related observations yet.</small>
            )}
          </div>
        </>
      )}
      {message && (
        <div className="inline-message" role="status">
          {message}
        </div>
      )}
    </div>
  );
}

function Meta({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <span className="eyebrow">{label}</span>
      <strong>{value}</strong>
    </div>
  );
}
function GraphCanvas({
  graph,
  onSelect,
}: {
  graph: GraphSubgraph | null;
  onSelect: (id: string) => void;
}) {
  const container = useRef<HTMLDivElement | null>(null);
  const graphInstance = useRef<Core | null>(null);
  useEffect(() => {
    if (!container.current || !graph?.nodes.length) return;
    const instance = cytoscape({
      container: container.current,
      elements: [
        ...graph.nodes.map((node) => ({
          data: { ...node, root: node.id === graph.root },
        })),
        ...graph.edges.map((edge) => ({ data: { ...edge, label: edge.type } })),
      ],
      layout: {
        name: "cose",
        animate: false,
        fit: true,
        padding: 32,
        nodeRepulsion: () => 9000,
      },
      style: [
        {
          selector: "node",
          style: {
            "background-color": "#263d2a",
            "border-color": "#719079",
            "border-width": 1.5,
            color: "#d9e1d2",
            label: "data(label)",
            "font-family": "DM Mono",
            "font-size": 8,
            "text-valign": "bottom",
            "text-margin-y": 7,
            width: 28,
            height: 28,
          },
        },
        {
          selector: "node[kind = 'observation']",
          style: {
            "background-color": "#66532b",
            "border-color": "#d1ad55",
            width: 36,
            height: 36,
          },
        },
        {
          selector: "node[kind = 'entity']",
          style: {
            shape: "diamond",
            "background-color": "#29475a",
            "border-color": "#76a8c6",
          },
        },
        {
          selector: "node[kind = 'actor']",
          style: {
            shape: "round-rectangle",
            "background-color": "#51395c",
            "border-color": "#a07ab0",
          },
        },
        {
          selector: "node[?root]",
          style: { width: 48, height: 48, "border-width": 3 },
        },
        {
          selector: "edge",
          style: {
            width: 1,
            "line-color": "#526a55",
            "target-arrow-color": "#526a55",
            "target-arrow-shape": "triangle",
            "curve-style": "bezier",
            label: "data(label)",
            color: "#748075",
            "font-size": 7,
            "text-rotation": "autorotate",
            "text-background-color": "#131813",
            "text-background-opacity": 0.85,
            "text-background-padding": "2px",
          },
        },
      ],
    });
    graphInstance.current = instance;
    instance.on("tap", "node[kind = 'observation']", (event) =>
      onSelect(String(event.target.id()).replace(/^observation:/, "")),
    );
    return () => {
      graphInstance.current = null;
      instance.destroy();
    };
  }, [graph]);
  const adjustZoom = (factor: number) => {
    const instance = graphInstance.current;
    if (instance) instance.zoom(instance.zoom() * factor);
  };
  return (
    <section className="graph-panel panel" id="graph">
      <div className="panel-head">
        <div>
          <h2>
            {graph
              ? "Heterogeneous memory neighborhood"
              : "Select a memory to explore"}
          </h2>
        </div>
        <div className="graph-actions">
          <span className="count">
            {graph
              ? `${graph.nodes.length} NODES / ${graph.edges.length} EDGES${graph.truncated ? " / TRUNCATED" : ""}`
              : "IDLE"}
          </span>
          {graph && (
            <>
              <button onClick={() => adjustZoom(1.2)} aria-label="Zoom in">
                +
              </button>
              <button onClick={() => adjustZoom(1 / 1.2)} aria-label="Zoom out">
                −
              </button>
              <button onClick={() => graphInstance.current?.fit(undefined, 32)}>
                Fit
              </button>
            </>
          )}
        </div>
      </div>
      <div className="graph-canvas">
        {graph?.nodes.length ? (
          <div
            className="cytoscape-canvas"
            ref={container}
            role="img"
            aria-label="Knowledge graph with observations, entities, actors, sessions, and projects"
          />
        ) : (
          <div className="graph-empty">
            Select a memory in the Memories view to load its graph.
          </div>
        )}
      </div>
    </section>
  );
}

function AuditPanel({
  entries,
  loading,
  loaded,
  allowed,
  onLoad,
}: {
  entries: AuditEntry[];
  loading: boolean;
  loaded: boolean;
  allowed: boolean;
  onLoad: () => void;
}) {
  return (
    <section className="panel audit-panel" id="audit">
      <div className="panel-head">
        <div>
          <h2>Authorization audit trail</h2>
        </div>
        {allowed && (
          <button
            className="detail-actions button"
            onClick={onLoad}
            disabled={loading}
          >
            {loading
              ? "Loading..."
              : loaded
                ? "Refresh events"
                : "Load recent events"}
          </button>
        )}
      </div>
      {!allowed ? (
        <div className="permission-state">
          <strong>Administrative access required</strong>
          <span>
            Audit events include security-sensitive authorization decisions and
            are available to owner and admin roles.
          </span>
        </div>
      ) : entries.length ? (
        <div className="audit-list">
          {entries.map((entry) => (
            <div className="audit-row" key={entry.id}>
              <span className={entry.allowed ? "audit-ok" : "audit-denied"}>
                {entry.allowed ? "ALLOW" : "DENY"}
              </span>
              <strong>{entry.action}</strong>
              <span>{entry.resource_type}</span>
              <small>{formatDate(entry.created_at)}</small>
            </div>
          ))}
        </div>
      ) : (
        <p className="audit-hint">
          {loaded
            ? "No authorization events were returned."
            : "Load the latest authorization decisions on demand."}
        </p>
      )}
    </section>
  );
}
function formatDate(value: string) {
  if (!value) return "unknown date";
  return new Intl.DateTimeFormat("en", {
    month: "short",
    day: "numeric",
    year: "numeric",
  }).format(new Date(value));
}
