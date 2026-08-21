// Shared bearer-transport policy for the web client.
//
// Mirrors internal/transportpolicy: a request carrying a Bearer credential
// may only travel over HTTPS, or over plain HTTP to a strict loopback
// destination. Every surface that can transmit a bearer token (the API
// client, the auth handshake, and every agent config exporter) enforces
// this module before any request or export is produced.

/** Thrown when a URL is not an acceptable Bearer destination. */
export class InsecureTransportError extends Error {
  constructor(reason: string) {
    super(`insecure transport: ${reason}`);
    this.name = "InsecureTransportError";
  }
}

export function isStrictLoopbackHost(host: string): boolean {
  const h = host.trim().toLowerCase();
  if (h === "localhost") {
    return true;
  }
  if (h.includes(":")) {
    // IPv6 literal: URL.hostname keeps the brackets, so strip them first.
    // Only the exact loopback address ::1 is sanctioned. IPv4-mapped
    // spellings such as ::ffff:127.0.0.1 are deliberately rejected.
    const bare = h.replace(/^\[/, "").replace(/\]$/, "");
    return bare === "::1";
  }
  // IPv4 dotted-quad literal (no leading zeros) inside 127.0.0.0/8.
  const parts = h.split(".");
  if (
    parts.length === 4 &&
    parts.every((p) => /^\d{1,3}$/.test(p) && Number(p) <= 255 && (p === "0" || !p.startsWith("0")))
  ) {
    return Number(parts[0]) === 127;
  }
  return false;
}

/**
 * Validates that rawURL is an acceptable destination for requests carrying
 * Bearer credentials: any HTTPS URL, or plain HTTP only on strict loopback.
 * Must run before any request (or export referencing the destination) is
 * issued; throws InsecureTransportError otherwise.
 */
export function validateBearerDestination(rawURL: string): void {
  let parsed: URL;
  try {
    parsed = new URL(rawURL);
  } catch {
    throw new InsecureTransportError("destination must be an absolute HTTP(S) URL");
  }
  if (parsed.protocol === "https:") {
    return;
  }
  if (parsed.protocol === "http:") {
    if (!isStrictLoopbackHost(parsed.hostname)) {
      throw new InsecureTransportError(
        `plain HTTP to "${parsed.hostname}" is forbidden for Bearer destinations; use HTTPS (only strict loopback may use plain HTTP)`,
      );
    }
    return;
  }
  throw new InsecureTransportError(`scheme "${parsed.protocol.replace(":", "")}" is not an HTTP(S) transport`);
}
