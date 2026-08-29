FROM node:22-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
ENV NEXT_TELEMETRY_DISABLED=1
ARG NEXT_PUBLIC_CORTEX_MANAGED_ENDPOINT=true
ARG NEXT_PUBLIC_CORTEX_SERVER_URL=https://cortex-server-production-cb53.up.railway.app
ENV NEXT_PUBLIC_CORTEX_MANAGED_ENDPOINT=$NEXT_PUBLIC_CORTEX_MANAGED_ENDPOINT
ENV NEXT_PUBLIC_CORTEX_SERVER_URL=$NEXT_PUBLIC_CORTEX_SERVER_URL
RUN npm run build

FROM node:22-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1
ENV PORT=80
ENV HOSTNAME=0.0.0.0

COPY --from=builder /app/package*.json ./
COPY --from=builder /app/public ./public
COPY --from=builder /app/.next/standalone ./
COPY --from=builder /app/.next/static ./.next/static

RUN printf '#!/bin/sh\nexec node /app/server.js "$@"\n' > /usr/local/bin/cortex && \
    chmod +x /usr/local/bin/cortex

EXPOSE 80
CMD ["node", "server.js"]
