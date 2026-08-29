export const DEFAULT_SERVER_URL = "http://localhost:7438";

export interface ServerEndpointBuildConfig {
  managed?: string;
  url?: string;
}

export interface ServerEndpoint {
  managed: boolean;
  url: string;
}

// The Compose build supplies this configuration at build time. A managed
// endpoint is deliberately opt-in so a separately deployed UI can still ask
// the operator which Cortex server it should use.
export function resolveServerEndpoint(config: ServerEndpointBuildConfig = {}): ServerEndpoint {
  const configuredURL = config.url?.trim().replace(/\/+$/, "");
  return {
    managed: config.managed?.trim().toLowerCase() === "true",
    url: configuredURL || DEFAULT_SERVER_URL,
  };
}

export const serverEndpoint = resolveServerEndpoint({
  managed: process.env.NEXT_PUBLIC_CORTEX_MANAGED_ENDPOINT,
  url: process.env.NEXT_PUBLIC_CORTEX_SERVER_URL,
});
