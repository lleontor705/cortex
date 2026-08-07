import { FormEvent, useEffect, useState } from "react";
import { CortexAPI, Principal, Token, User } from "./api";

export function isAdminPrincipal(principal: Principal | null): boolean {
  return (
    principal?.roles.some((role) => role === "owner" || role === "admin") ??
    false
  );
}

type Props = {
  api: CortexAPI;
  principal: Principal;
  onMessage: (message: string) => void;
};

export function AdministrationPanel({ api, principal, onMessage }: Props) {
  const allowed = isAdminPrincipal(principal);
  const [users, setUsers] = useState<User[]>([]);
  const [tokens, setTokens] = useState<Token[]>([]);
  const [loading, setLoading] = useState(allowed);
  const [loaded, setLoaded] = useState(false);
  const [action, setAction] = useState("");
  const [email, setEmail] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [role, setRole] = useState("member");
  const [tokenName, setTokenName] = useState("");
  const [tokenSubject, setTokenSubject] = useState("");
  const [issuedSecret, setIssuedSecret] = useState("");

  const refresh = async () => {
    if (!allowed) return;
    setLoading(true);
    try {
      const [nextUsers, nextTokens] = await Promise.all([
        api.users(),
        api.tokens(),
      ]);
      setUsers(nextUsers);
      setTokens(nextTokens);
      setLoaded(true);
    } catch (error) {
      onMessage(actionError(error, "Could not load administration data"));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void refresh();
  }, [api, allowed]);

  if (!allowed) {
    return (
      <section className="panel admin-panel access-panel" id="admin">
        <div>
          <h2>Administration requires elevated access</h2>
          <p>
            Your session is valid, but the current principal has{" "}
            <strong>{principal.roles.join(", ") || "no assigned role"}</strong>.
            Ask an owner to issue a token for an administrator account.
          </p>
        </div>
      </section>
    );
  }

  const createUser = async (event: FormEvent) => {
    event.preventDefault();
    setAction("create-user");
    try {
      await api.createUser({ email, display_name: displayName, roles: [role] });
      setEmail("");
      setDisplayName("");
      onMessage(`User ${displayName} created.`);
      await refresh();
    } catch (error) {
      onMessage(actionError(error, "Could not create user"));
    } finally {
      setAction("");
    }
  };

  const issueToken = async (event: FormEvent) => {
    event.preventDefault();
    setAction("issue-token");
    try {
      const issued = await api.issueToken({
        subject: tokenSubject,
        name: tokenName,
      });
      setIssuedSecret(issued.secret || "");
      setTokenName("");
      onMessage(
        "Internal token issued. Store the secret before dismissing it.",
      );
      await refresh();
    } catch (error) {
      onMessage(actionError(error, "Could not issue token"));
    } finally {
      setAction("");
    }
  };

  const toggleUser = async (user: User) => {
    const verb = user.active ? "disable" : "enable";
    if (
      !confirm(`${verb[0].toUpperCase()}${verb.slice(1)} ${user.display_name}?`)
    )
      return;
    setAction(`user-${user.id}`);
    try {
      await api.setUserActive(user.id, !user.active);
      onMessage(`${user.display_name} ${verb}d.`);
      await refresh();
    } catch (error) {
      onMessage(actionError(error, `Could not ${verb} user`));
    } finally {
      setAction("");
    }
  };

  const rotateToken = async (token: Token) => {
    if (
      !confirm(
        `Rotate token ${token.name || token.prefix}? The existing secret will be revoked.`,
      )
    )
      return;
    setAction(`token-${token.id}`);
    try {
      const issued = await api.rotateToken(token.id);
      setIssuedSecret(issued.secret || "");
      onMessage(
        "Token rotated. Store the replacement secret before dismissing it.",
      );
      await refresh();
    } catch (error) {
      onMessage(actionError(error, "Could not rotate token"));
    } finally {
      setAction("");
    }
  };

  const revokeToken = async (token: Token) => {
    if (
      !confirm(
        `Revoke token ${token.name || token.prefix}? This cannot be undone.`,
      )
    )
      return;
    setAction(`token-${token.id}`);
    try {
      await api.revokeToken(token.id);
      onMessage("Token revoked.");
      await refresh();
    } catch (error) {
      onMessage(actionError(error, "Could not revoke token"));
    } finally {
      setAction("");
    }
  };

  const copySecret = async () => {
    try {
      await navigator.clipboard.writeText(issuedSecret);
      onMessage("Token secret copied to the clipboard.");
    } catch {
      onMessage("Clipboard access failed. Select and copy the token manually.");
    }
  };

  return (
    <section className="panel admin-panel" id="admin" aria-busy={loading}>
      <div className="panel-head admin-heading">
        <div>
          <h2>Users and internal tokens</h2>
          <p className="panel-description">
            Issue attributable credentials, review usage, and revoke access
            without sharing the server bootstrap token.
          </p>
        </div>
        <div className="admin-summary">
          <span>{users.length} USERS</span>
          <span>
            {tokens.filter((token) => !token.revoked_at).length} ACTIVE TOKENS
          </span>
          <button
            className="button"
            onClick={() => void refresh()}
            disabled={loading || action !== ""}
          >
            {loading ? "Loading..." : "Refresh"}
          </button>
        </div>
      </div>

      {issuedSecret && (
        <div className="issued-secret" role="status">
          <span>NEW TOKEN, SHOWN ONCE</span>
          <code>{issuedSecret}</code>
          <span className="row-actions">
            <button onClick={() => void copySecret()}>Copy</button>
            <button onClick={() => setIssuedSecret("")}>Dismiss</button>
          </span>
        </div>
      )}

      <div className="admin-grid">
        <div className="admin-column">
          <div className="column-title">
            <div>
              <h3>Directory</h3>
            </div>
            <small>Persisted principals and grants</small>
          </div>
          <form className="admin-form" onSubmit={createUser}>
            <label>
              Email
              <input
                type="email"
                placeholder="operator@example.com"
                value={email}
                onChange={(event) => setEmail(event.target.value)}
                required
              />
            </label>
            <label>
              Display name
              <input
                placeholder="Operator name"
                value={displayName}
                onChange={(event) => setDisplayName(event.target.value)}
                required
              />
            </label>
            <label>
              Role
              <select
                value={role}
                onChange={(event) => setRole(event.target.value)}
              >
                <option value="member">Member</option>
                <option value="admin">Admin</option>
                <option value="owner">Owner</option>
              </select>
            </label>
            <button className="primary small" disabled={action !== ""}>
              {action === "create-user" ? "Creating..." : "Create user"}
            </button>
          </form>
          <div className="identity-list">
            {loading && !loaded ? (
              <EmptyState
                title="Loading directory"
                detail="Resolving users and grants..."
              />
            ) : users.length === 0 ? (
              <EmptyState
                title="No users yet"
                detail="Create the first attributable user above."
              />
            ) : (
              users.map((user) => (
                <article className="identity-row" key={user.id}>
                  <span
                    className={`status-dot ${user.active ? "active" : "inactive"}`}
                  />
                  <span className="identity-copy">
                    <strong>{user.display_name}</strong>
                    <small>{user.email}</small>
                    <span className="identity-meta">
                      {user.roles.join(", ")} / grant v{user.grant_version}
                    </span>
                  </span>
                  <button
                    onClick={() => void toggleUser(user)}
                    disabled={action !== ""}
                  >
                    {action === `user-${user.id}`
                      ? "Working..."
                      : user.active
                        ? "Disable"
                        : "Enable"}
                  </button>
                </article>
              ))
            )}
          </div>
        </div>

        <div className="admin-column">
          <div className="column-title">
            <div>
              <h3>Credentials</h3>
            </div>
            <small>Named, revocable internal tokens</small>
          </div>
          <form className="admin-form token-form" onSubmit={issueToken}>
            <label>
              User
              <select
                value={tokenSubject}
                onChange={(event) => setTokenSubject(event.target.value)}
                required
              >
                <option value="">Select active user</option>
                {users
                  .filter((user) => user.active)
                  .map((user) => (
                    <option key={user.id} value={user.id}>
                      {user.display_name}
                    </option>
                  ))}
              </select>
            </label>
            <label>
              Token name
              <input
                placeholder="CI agent / workstation"
                value={tokenName}
                onChange={(event) => setTokenName(event.target.value)}
                required
              />
            </label>
            <button
              className="primary small"
              disabled={action !== "" || users.every((user) => !user.active)}
            >
              {action === "issue-token" ? "Issuing..." : "Issue token"}
            </button>
          </form>
          <div className="identity-list token-list">
            {loading && !loaded ? (
              <EmptyState
                title="Loading credentials"
                detail="Resolving token inventory..."
              />
            ) : tokens.length === 0 ? (
              <EmptyState
                title="No internal tokens"
                detail="Issue a named token for an active user."
              />
            ) : (
              tokens.map((token) => (
                <article
                  className={`identity-row token-row ${token.revoked_at ? "revoked" : ""}`}
                  key={token.id}
                >
                  <span
                    className={`status-dot ${token.revoked_at ? "inactive" : "active"}`}
                  />
                  <span className="identity-copy">
                    <strong>{token.name || "Unnamed token"}</strong>
                    <small>
                      {token.prefix} / {token.principal_type}
                    </small>
                    <span className="identity-meta">
                      Subject {shortID(token.subject)} / Scopes{" "}
                      {token.scopes.join(", ") || "inherited"}
                    </span>
                    <span className="identity-meta">
                      Last used {formatAdminDate(token.last_used_at, "Never")} /
                      Expires{" "}
                      {formatAdminDate(token.expires_at, "No expiration")}
                    </span>
                  </span>
                  {!token.revoked_at && (
                    <span className="row-actions">
                      <button
                        onClick={() => void rotateToken(token)}
                        disabled={action !== ""}
                      >
                        Rotate
                      </button>
                      <button
                        className="danger"
                        onClick={() => void revokeToken(token)}
                        disabled={action !== ""}
                      >
                        Revoke
                      </button>
                    </span>
                  )}
                  {token.revoked_at && (
                    <span className="revoked-label">REVOKED</span>
                  )}
                </article>
              ))
            )}
          </div>
        </div>
      </div>
    </section>
  );
}

function EmptyState({ title, detail }: { title: string; detail: string }) {
  return (
    <div className="admin-empty">
      <strong>{title}</strong>
      <span>{detail}</span>
    </div>
  );
}

function actionError(error: unknown, fallback: string): string {
  return error instanceof Error ? `${fallback}: ${error.message}` : fallback;
}

function formatAdminDate(value: string | undefined, fallback: string): string {
  if (!value) return fallback;
  return new Intl.DateTimeFormat("en", {
    month: "short",
    day: "numeric",
    year: "numeric",
  }).format(new Date(value));
}

function shortID(value: string): string {
  return value.length > 12 ? `${value.slice(0, 8)}...` : value;
}
