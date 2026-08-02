import { FormEvent, useEffect, useState } from "react";
import { AuditEntry, CortexAPI, Observation, Score, ServerStats } from "./api";

const defaultURL = import.meta.env.VITE_API_URL || "http://localhost:7438";
const tokenKey = "cortex.web.token";
const urlKey = "cortex.web.url";

export function App() {
  const [api, setApi] = useState<CortexAPI | null>(() => {
    const token = localStorage.getItem(tokenKey);
    return token ? new CortexAPI(localStorage.getItem(urlKey) || defaultURL, token) : null;
  });
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
  const [projects, setProjects] = useState<string[]>([]);

  const load = async (client: CortexAPI, search = "") => {
    setLoading(true); setError("");
    try {
      const filters = project ? `&project=${encodeURIComponent(project)}` : "";
      const [result, serverStats, visibleProjects] = await Promise.all([
        search.trim() ? client.search(search, project).then((response) => response.value) : client.listObservations(`?limit=50${filters}`),
        client.stats(),
        client.projects(),
      ]);
      setObservations(result || []);
      setStats(serverStats);
      setProjects(visibleProjects || []);
    } catch (err) { setError(err instanceof Error ? err.message : "Could not load Cortex data"); }
    finally { setLoading(false); }
  };

  const connect = async (url: string, token: string) => {
    const client = new CortexAPI(url, token);
    setError("");
    try { await client.health(); localStorage.setItem(urlKey, url); localStorage.setItem(tokenKey, token); setApi(client); setConnected(true); await load(client); }
    catch (err) { setError(err instanceof Error ? err.message : "Could not connect to Cortex"); }
  };

  useEffect(() => { if (api && !connected) connect(localStorage.getItem(urlKey) || defaultURL, localStorage.getItem(tokenKey) || ""); }, [api]);

  if (!api || !connected) return <Connect onConnect={connect} error={error} />;

  const openObservation = async (observation: Observation) => {
    setCreating(false); setSelected(observation); setScore(null); setRelated([]);
    try { const [full, importance, graph] = await Promise.all([api.getObservation(observation.id), api.getScore(observation.id), api.related(observation.id)]); setSelected(full); setScore(importance); setRelated(graph.value || []); }
    catch (err) { setError(err instanceof Error ? err.message : "Could not load observation"); }
  };

  const disconnect = () => { localStorage.removeItem(tokenKey); setApi(null); setConnected(false); };
  const loadAudit = async () => { setAuditLoading(true); try { setAudit(await api.audit()); } catch (err) { setError(err instanceof Error ? err.message : "Audit is restricted to administrators"); } finally { setAuditLoading(false); } };
  return <div className="shell">
    <aside className="sidebar">
      <div className="brand"><span className="brand-mark">C</span><span>CORTEX<span className="muted"> / CONTROL</span></span></div>
      <nav><button className="nav-item active">Overview <span>01</span></button><button className="nav-item">Memories <span>{(stats?.observations ?? observations.length).toString().padStart(2, "0")}</span></button><button className="nav-item">Sessions <span>{(stats?.sessions ?? 0).toString().padStart(2, "0")}</span></button><button className="nav-item">Graph <span>{(stats?.edges ?? 0).toString().padStart(2, "0")}</span></button></nav>
      <div className="sidebar-bottom"><div className="connection"><i /> SERVER ONLINE<span>{localStorage.getItem(urlKey) || defaultURL}</span></div><button className="disconnect" onClick={disconnect}>Disconnect</button></div>
    </aside>
    <main className="main">
      <header className="topbar"><div><p className="eyebrow">SERVER WORKSPACE</p><h1>Memory control room</h1></div><div className="top-actions"><span className="live"><i /> LIVE</span><button className="avatar">SA</button></div></header>
      {error && <div className="alert">{error}<button onClick={() => setError("")}>Dismiss</button></div>}
      <section className="stats"><Stat label="MEMORIES" value={(stats?.observations ?? observations.length).toString().padStart(2, "0")} detail="visible workspace records" tone="gold" /><Stat label="ACTIVE SESSIONS" value={(stats?.active_sessions ?? 0).toString().padStart(2, "0")} detail={`${stats?.sessions ?? 0} total sessions`} tone="green" /><Stat label="PROJECTS" value={(stats?.projects ?? 0).toString().padStart(2, "0")} detail="workspace projects" tone="blue" /><Stat label="RELATIONSHIPS" value={(stats?.edges ?? 0).toString().padStart(2, "0")} detail="knowledge graph edges" tone="violet" /></section>
      <section className="workspace-grid">
        <div className="panel library"><div className="panel-head"><div><p className="eyebrow">MEMORY LIBRARY</p><h2>{query ? `Results for “${query}”` : "Recent observations"}</h2></div><div className="library-actions"><span className="count">{observations.length} ITEMS</span><button className="primary small" onClick={() => { setCreating(true); setSelected(null); setScore(null); }}>New memory <span>+</span></button></div></div><form className="search" onSubmit={(event) => { event.preventDefault(); void load(api, query); }}><span>⌕</span><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search memories..." /><select value={project} onChange={(event) => { setProject(event.target.value); void load(api, query); }}><option value="">All projects</option>{projects.map((item) => <option key={item} value={item}>{item}</option>)}</select><kbd>ENTER</kbd></form><div className="list">{loading ? <div className="empty">Loading memory index...</div> : observations.length === 0 ? <div className="empty">No memories found.</div> : observations.map((item) => <button className={`memory-row ${selected?.id === item.id ? "selected" : ""}`} key={item.id} onClick={() => void openObservation(item)}><span className="type-dot" /><span className="memory-copy"><strong>{item.title || "Untitled observation"}</strong><small>{item.project || "unassigned"} · {formatDate(item.created_at)}</small></span><span className="memory-type">{item.type || "note"}</span><span className="chevron">›</span></button>)}</div></div>
        <Detail observation={selected} creating={creating} score={score} related={related} api={api} onSaved={(item) => { setCreating(false); setSelected(item); void load(api, query); }} onDeleted={() => { setCreating(false); setSelected(null); setScore(null); setRelated([]); void load(api, query); }} />
      </section>
      <GraphCanvas root={selected} related={related} onSelect={(item) => void openObservation(item)} />
      <AuditPanel entries={audit} loading={auditLoading} onLoad={() => void loadAudit()} />
    </main>
  </div>;
}

function Connect({ onConnect, error }: { onConnect: (url: string, token: string) => Promise<void>; error: string }) {
  const [url, setURL] = useState(localStorage.getItem(urlKey) || defaultURL); const [token, setToken] = useState(localStorage.getItem(tokenKey) || ""); const [busy, setBusy] = useState(false);
  const submit = async (event: FormEvent) => { event.preventDefault(); setBusy(true); await onConnect(url, token); setBusy(false); };
  return <div className="connect-page"><div className="connect-card"><div className="brand"><span className="brand-mark">C</span><span>CORTEX<span className="muted"> / CONTROL</span></span></div><p className="eyebrow">SERVER CONSOLE</p><h1>Enter the memory layer.</h1><p className="lead">Connect to a Cortex server to inspect, search, and curate your workspace.</p><form onSubmit={submit}><label>SERVER URL<input value={url} onChange={(event) => setURL(event.target.value)} placeholder="http://localhost:7438" /></label><label>BEARER TOKEN<input type="password" value={token} onChange={(event) => setToken(event.target.value)} placeholder="docker-development-token" required /></label>{error && <div className="form-error">{error}</div>}<button className="primary" disabled={busy}>{busy ? "Connecting..." : "Connect to server"}<span>→</span></button></form><small className="privacy">Credentials stay in this browser and are sent only to the server URL above.</small></div></div>;
}

function Stat({ label, value, detail, tone }: { label: string; value: string; detail: string; tone: string }) { return <div className={`stat ${tone}`}><p className="eyebrow">{label}</p><strong>{value}</strong><span>{detail}</span></div>; }

function Detail({ observation, creating, score, related, api, onSaved, onDeleted }: { observation: Observation | null; creating: boolean; score: Score | null; related: Observation[]; api: CortexAPI; onSaved: (item: Observation) => void; onDeleted: () => void }) {
  const [editing, setEditing] = useState(false); const [title, setTitle] = useState(""); const [content, setContent] = useState(""); const [type, setType] = useState("note"); const [sessionID, setSessionID] = useState(""); const [busy, setBusy] = useState(false); const [message, setMessage] = useState("");
  useEffect(() => { setTitle(observation?.title || ""); setContent(observation?.content || ""); setType(observation?.type || "note"); setSessionID(observation?.session_id || ""); setEditing(creating); setMessage(""); }, [observation, creating]);
  if (!observation && !creating) return <div className="panel detail empty-detail"><span className="detail-symbol">+</span><p>Select a memory to inspect its full context.</p><small>Search the library or choose an observation from the list.</small></div>;
  const save = async () => { setBusy(true); try { const item = await api.updateObservation(observation?.id || "", { title, content }); onSaved(item); setEditing(false); setMessage("Saved"); } catch (err) { setMessage(err instanceof Error ? err.message : "Save failed"); } finally { setBusy(false); } };
  const create = async () => { setBusy(true); try { const item = await api.createObservation({ title, content, type, session_id: sessionID }); onSaved(item); setMessage("Created"); } catch (err) { setMessage(err instanceof Error ? err.message : "Create failed"); } finally { setBusy(false); } };
  const remove = async () => { if (!observation || !confirm("Delete this observation permanently?")) return; setBusy(true); try { await api.deleteObservation(observation.id); onDeleted(); } catch (err) { setMessage(err instanceof Error ? err.message : "Delete failed"); } finally { setBusy(false); } };
  return <div className="panel detail"><div className="panel-head"><div><p className="eyebrow">{creating ? "NEW OBSERVATION" : "OBSERVATION DETAIL"}</p><h2>{editing ? (creating ? "Create memory" : "Edit memory") : observation?.title}</h2></div><div className="detail-actions">{editing ? <><button onClick={() => creating ? onDeleted() : setEditing(false)}>Cancel</button><button className="primary small" onClick={() => void (creating ? create() : save())} disabled={busy}>{creating ? "Create" : "Save"}</button></> : <><button onClick={() => setEditing(true)}>Edit</button><button className="danger" onClick={() => void remove()} disabled={busy}>Delete</button></>}</div></div>{editing ? <div className="editor">{creating && <input value={sessionID} onChange={(event) => setSessionID(event.target.value)} placeholder="Session UUID (required)" />}<input value={title} onChange={(event) => setTitle(event.target.value)} placeholder="Title" /><input value={type} onChange={(event) => setType(event.target.value)} placeholder="Type" /><textarea value={content} onChange={(event) => setContent(event.target.value)} placeholder="Content" rows={9} /></div> : <><div className="tags"><span>{observation?.type || "note"}</span><span>{observation?.project || "unassigned"}</span><span>{observation?.scope || "project"}</span></div><p className="content">{observation?.content}</p><div className="meta-grid"><Meta label="PUBLIC ID" value={observation?.id || ""} /><Meta label="CREATED" value={formatDate(observation?.created_at || "")} /><Meta label="SOURCE" value={observation?.source || "manual"} /><Meta label="CONFIDENCE" value={`${Math.round((observation?.confidence || 0) * 100)}%`} /></div>{score && <div className="score"><span>IMPORTANCE SCORE</span><strong>{score.score.toFixed(2)}</strong><small>{score.access_count} accesses</small></div>}<div className="related"><div><span className="eyebrow">CONNECTED MEMORIES</span><strong>{related.length.toString().padStart(2, "0")}</strong></div>{related.length ? related.slice(0, 4).map((item) => <div className="related-row" key={item.id}><span className="type-dot" /><span>{item.title}</span></div>) : <small>No related observations yet.</small>}</div></>}</div>;
}

function Meta({ label, value }: { label: string; value: string }) { return <div><span className="eyebrow">{label}</span><strong>{value}</strong></div>; }
function GraphCanvas({ root, related, onSelect }: { root: Observation | null; related: Observation[]; onSelect: (item: Observation) => void }) {
  const nodes = root ? [root, ...related.filter((item) => item.id !== root.id).slice(0, 6)] : [];
  return <section className="graph-panel panel"><div className="panel-head"><div><p className="eyebrow">KNOWLEDGE GRAPH</p><h2>{root ? "Local memory neighborhood" : "Select a memory to explore"}</h2></div><span className="count">{nodes.length ? `${nodes.length} NODES` : "IDLE"}</span></div><div className="graph-canvas">{nodes.length ? <svg viewBox="0 0 900 230" role="img" aria-label="Related memory graph"><g className="graph-lines">{nodes.slice(1).map((_, index) => <line key={index} x1="450" y1="112" x2={180 + index * 108} y2={48 + (index % 2) * 125} />)}</g>{nodes.map((node, index) => { const center = index === 0; const x = center ? 450 : 180 + (index - 1) * 108; const y = center ? 112 : 48 + ((index - 1) % 2) * 125; return <g className={`graph-node ${center ? "root" : ""}`} key={node.id} onClick={() => onSelect(node)}><circle cx={x} cy={y} r={center ? 35 : 25} /><text x={x} y={y + 4} textAnchor="middle">{center ? "ROOT" : `0${index}`}</text><title>{node.title}</title></g>; })}</svg> : <div className="graph-empty">Relationships will appear here when you select an observation.</div>}</div></section>;
}
function AuditPanel({ entries, loading, onLoad }: { entries: AuditEntry[]; loading: boolean; onLoad: () => void }) { return <section className="panel audit-panel"><div className="panel-head"><div><p className="eyebrow">ADMINISTRATION</p><h2>Audit trail</h2></div><button className="detail-actions button" onClick={onLoad} disabled={loading}>{loading ? "Loading..." : "Load recent events"}</button></div>{entries.length ? <div className="audit-list">{entries.map((entry) => <div className="audit-row" key={entry.id}><span className={entry.allowed ? "audit-ok" : "audit-denied"}>{entry.allowed ? "ALLOW" : "DENY"}</span><strong>{entry.action}</strong><span>{entry.resource_type}</span><small>{formatDate(entry.created_at)}</small></div>)}</div> : <p className="audit-hint">Recent authorization events are restricted to administrators and are loaded on demand.</p>}</section>; }
function formatDate(value: string) { if (!value) return "unknown date"; return new Intl.DateTimeFormat("en", { month: "short", day: "numeric", year: "numeric" }).format(new Date(value)); }
