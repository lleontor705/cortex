const loopbackHosts = new Set(["localhost", "127.0.0.1", "[::1]"]);

export function normalizeServerURL(value: string): string {
  const url = new URL(value.trim());
  const secure = url.protocol === "https:";
  const localHTTP = url.protocol === "http:" && loopbackHosts.has(url.hostname);
  if (!secure && !localHTTP) {
    throw new Error("Use HTTPS for remote Cortex servers.");
  }
  if (url.username || url.password || url.search || url.hash) {
    throw new Error("Enter only the Cortex server origin.");
  }
  return url.origin;
}
