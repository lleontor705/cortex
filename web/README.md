# Cortex Control Room

The first web client for the authenticated Cortex server. It provides a server connection screen, workspace counters, project filtering, session counts, observation search/listing, observation detail, an interactive local graph, editing, deletion, importance scores, and an administrator-only audit trail.

## Development

```bash
npm install
npm run dev
```

Set `VITE_API_URL` when the server is not running at `http://localhost:7438`. The bearer token is entered in the connection screen and stored in browser local storage for the current workspace.

## Docker

```bash
docker build --build-arg VITE_API_URL=http://localhost:7438 -t cortex-web ./web
docker run --rm -p 5173:80 cortex-web
```

Open `http://localhost:5173` after starting the Cortex server.

## Current API scope

The client intentionally uses only authorized server endpoints. Project names are read-only and filtered through the configured principal grants. Grant mutation is not exposed: the current server bearer token represents one configured service account, so allowing that same account to rewrite its grants would create an escalation path. Grant administration must be added together with verified user identity, separate administrator credentials, and explicit manage authorization.
