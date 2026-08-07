# Cortex Control Room

The web client for the authenticated Cortex server. It provides workspace counters, observation curation, a heterogeneous graph explorer, authorization audit visibility, and administrator-issued user tokens with per-user attribution.

## Development

```bash
npm install
npm run dev
```

Set `VITE_API_URL` when the server is not running at `http://localhost:7438`. The bearer token is entered in the connection screen and stored only in browser session storage; it is cleared when the tab session ends.

## Docker

```bash
docker build --build-arg VITE_API_URL=http://localhost:7438 -t cortex-web ./web
docker run --rm -p 5173:80 cortex-web
```

Open `http://localhost:5173` after starting the Cortex server.

## Current API scope

The client uses only authorized server endpoints. Project names and graph data are filtered through verified principal grants. The Administration section is visible to every authenticated principal so access requirements are explicit, but user and token operations are loaded and enabled only for `owner` and `admin` roles. Issued token secrets are shown once and remain only in ephemeral React state.
