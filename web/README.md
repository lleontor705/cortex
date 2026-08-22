# Cortex Control Room (Next.js)

The modern Next.js web application for Cortex. It provides real-time workspace metrics, observation curation, 2D knowledge graph visualization, LLM extraction playgrounds, MCP project directives/skills governance, and administrator token management with BOLA role-based access control.

## Key Features

- 🌓 **Dynamic Light & Dark Themes:** Seamless switching between Slate/Dark Glassmorphism and Clean Paper/Sky light mode with persistent storage.
- 🔑 **Bearer Token Authentication:** Connect securely with `admin`, `owner`, or `member` roles.
- 📊 **Differentiated Dashboards:** Admins toggle between Tenant Global Analytics and Personal Workspace Views.
- ☁️ **Project-Level Cloud Sync:** Granular toggle per project (`Cloud Sync: ON` / `Local Only`).
- 🛡️ **BOLA Authorization & Ownership:** Regular members can only delete observations authored with their own token, protecting shared corporate knowledge.

## Development

```bash
npm install
npm run dev
```

Open `http://localhost:3000` (or `http://localhost:5173` if running production container), enter your Cortex Server URL (`http://localhost:7438`) and Bearer Token.

## Production Build & Docker

```bash
# Build Next.js application
npm run build

# Docker Container
docker build -t cortex-web ./web
docker run --rm -p 5173:80 cortex-web
```
