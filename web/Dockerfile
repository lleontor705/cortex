FROM --platform=$BUILDPLATFORM node:22-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci
COPY . .
ENV NEXT_TELEMETRY_DISABLED=1
ARG NEXT_PUBLIC_CORTEX_MANAGED_ENDPOINT=true
ARG NEXT_PUBLIC_CORTEX_SERVER_URL=https://cortex-server-clean-production.up.railway.app
ENV NEXT_PUBLIC_CORTEX_MANAGED_ENDPOINT=$NEXT_PUBLIC_CORTEX_MANAGED_ENDPOINT
ENV NEXT_PUBLIC_CORTEX_SERVER_URL=$NEXT_PUBLIC_CORTEX_SERVER_URL
RUN --mount=type=cache,target=/app/.next/cache \
    npm run build

FROM node:22-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1
ENV PORT=80
ENV HOSTNAME=0.0.0.0

# Run as non-root user
USER node

COPY --from=builder --chown=node:node /app/package*.json ./
COPY --from=builder --chown=node:node /app/public ./public
COPY --from=builder --chown=node:node /app/.next/standalone ./
COPY --from=builder --chown=node:node /app/.next/static ./.next/static

EXPOSE 80

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:80/health || exit 1

CMD ["node", "server.js"]
