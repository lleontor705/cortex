export const DEFAULT_SERVER_URL = "http://localhost:7438";

export interface ServerEndpointBuildConfig {
  managed?: string;
  url?: string;
}

export interface ServerEndpoint {
  managed: boolean;
  url: string;
}

/** Shape of the runtime config script served by the Go server before app scripts. */
export interface CortexWebRuntimeConfig {
  serverUrl?: string;
}

declare global {
  interface Window {
    __CORTEX_WEB_CONFIG__?: CortexWebRuntimeConfig;
  }
}

export function resolveServerEndpoint(config: ServerEndpointBuildConfig = {}): ServerEndpoint {
  const configuredURL = config.url?.trim().replace(/\/+$/, "");
  return {
    managed: config.managed?.trim().toLowerCase() === "true",
    url: configuredURL || DEFAULT_SERVER_URL,
  };
}

function readRuntimeServerUrl(): string | undefined {
  if (typeof window === "undefined") {
    return undefined;
  }
  return window.__CORTEX_WEB_CONFIG__?.serverUrl;
}

// Resolution is deferred to first ACCESS, not module evaluation: the app
// bundles ship as async scripts and the runtime /config.js script may execute
// around them, so reading the injected config during module init would race
// the injection and resurrect the localhost fallback. Consumers read this
// during the React render/effect lifecycle, which always runs after every
// document script has executed.
let cachedEndpoint: ServerEndpoint | null = null;

function runtimeEndpoint(): ServerEndpoint {
  if (cachedEndpoint === null) {
    const runtimeServerUrl = readRuntimeServerUrl();
    cachedEndpoint = resolveServerEndpoint({
      managed: runtimeServerUrl ? "true" : undefined,
      url: runtimeServerUrl,
    });
  }
  return cachedEndpoint;
}

// An injected runtime endpoint means the deployment dictates the API origin, so
// the UI must not ask the operator to pick one. Absent config keeps the
// unmanaged behavior and falls back to the documented default.
export const serverEndpoint: ServerEndpoint = {
  get managed(): boolean {
    return runtimeEndpoint().managed;
  },
  get url(): string {
    return runtimeEndpoint().url;
  },
};
