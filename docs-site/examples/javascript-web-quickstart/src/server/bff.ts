export interface DevelopmentTokenInput {
  uid: string;
  token: string;
  deviceFlag: 1;
  deviceLevel: 0;
}

/** Must remain aligned with the Phase 12 OpenAPI slice. */
export const MAX_PERSON_MESSAGE_SYNC_LIMIT = 100;

export interface RouteAddresses {
  tcpAddress: string;
  websocketAddress: string;
  secureWebsocketAddress: string;
}

export interface PersonMessageSyncInput {
  loginUid: string;
  peerUid: string;
  startMessageSeq: number;
  endMessageSeq: number;
  limit: number;
  pullMode: number;
}

export interface SyncedPersonMessage {
  messageId: string;
  messageSeq: number;
  clientMsgNo: string;
  fromUid: string;
  timestamp: number;
  payload: string;
}

/** Product HTTP operations that are intentionally kept behind the local BFF. */
export interface WuKongIMProductClient {
  updateToken(input: DevelopmentTokenInput): Promise<void>;
  discoverRoute(): Promise<RouteAddresses>;
  syncPersonMessages(input: PersonMessageSyncInput): Promise<SyncedPersonMessage[]>;
}

interface BffHandlerOptions {
  productClient: WuKongIMProductClient;
  tokenFactory: () => string;
}

type BffHandler = (request: Request) => Promise<Response>;

function jsonResponse(status: number, value: unknown): Response {
  return Response.json(value, {
    status,
    headers: { "cache-control": "no-store" },
  });
}

function hostnameFromHostHeader(host: string): string {
  if (host.startsWith("[")) {
    return host.slice(1, host.indexOf("]"));
  }
  return host.split(":", 1)[0]?.toLowerCase() ?? "";
}

function isLoopbackRequest(request: Request): boolean {
  const url = new URL(request.url);
  const hostname = hostnameFromHostHeader(
    request.headers.get("host") ?? url.host,
  );
  return hostname === "127.0.0.1" || hostname === "localhost" || hostname === "::1";
}

function isValidUid(value: unknown): value is string {
  return typeof value === "string" && /^[A-Za-z0-9._-]{1,64}$/.test(value);
}

function isNonNegativeInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

function isBoundedPageSize(value: unknown): value is number {
  return (
    typeof value === "number" &&
    Number.isSafeInteger(value) &&
    value >= 1 &&
    value <= MAX_PERSON_MESSAGE_SYNC_LIMIT
  );
}

function isPullMode(value: unknown): value is 0 | 1 {
  return value === 0 || value === 1;
}

function browserWebSocketURL(route: RouteAddresses): string {
  const secure = route.secureWebsocketAddress !== "";
  const value = secure ? route.secureWebsocketAddress : route.websocketAddress;
  if (value === "") {
    throw new Error("Product HTTP route has no browser WebSocket address");
  }
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    throw new Error("Product HTTP route has an invalid WebSocket address");
  }
  const expectedProtocol = secure ? "wss:" : "ws:";
  if (parsed.protocol !== expectedProtocol) {
    throw new Error(`Product HTTP route must use ${expectedProtocol.slice(0, -1)}`);
  }
  return value;
}

/** Creates the same-origin HTTP boundary consumed by the browser example. */
export function createBffHandler(options: BffHandlerOptions): BffHandler {
  return async (request) => {
    const url = new URL(request.url);

    if (!isLoopbackRequest(request)) {
      return jsonResponse(403, { error: "development BFF is localhost-only" });
    }
    const origin = request.headers.get("origin");
    if (origin !== null && origin !== url.origin) {
      return jsonResponse(403, { error: "cross-origin requests are not allowed" });
    }

    if (
      request.method === "POST" &&
      url.pathname === "/api/development/identity"
    ) {
      // docs:start bff-provision-identity
      const body = (await request.json()) as { uid?: unknown };
      const uid = typeof body.uid === "string" ? body.uid.trim() : "";
      if (!isValidUid(uid)) {
        return jsonResponse(400, {
          error:
            "uid must use 1-64 letters, numbers, dots, underscores, or hyphens",
        });
      }
      const token = options.tokenFactory();

      await options.productClient.updateToken({
        uid,
        token,
        deviceFlag: 1,
        deviceLevel: 0,
      });
      const route = await options.productClient.discoverRoute();
      const websocketUrl = browserWebSocketURL(route);

      return jsonResponse(200, { uid, token, websocketUrl });
      // docs:end bff-provision-identity
    }

    if (request.method === "POST" && url.pathname === "/api/messages/sync") {
      // docs:start bff-sync-messages
      const body = (await request.json()) as {
        uid?: unknown;
        peerUid?: unknown;
        startMessageSeq?: unknown;
        endMessageSeq?: unknown;
        limit?: unknown;
        pullMode?: unknown;
      };
      if (
        !isValidUid(body.uid) ||
        !isValidUid(body.peerUid) ||
        !isNonNegativeInteger(body.startMessageSeq) ||
        !isNonNegativeInteger(body.endMessageSeq) ||
        !isBoundedPageSize(body.limit) ||
        !isPullMode(body.pullMode)
      ) {
        return jsonResponse(400, { error: "invalid person-message sync request" });
      }
      const messages = await options.productClient.syncPersonMessages({
        loginUid: body.uid,
        peerUid: body.peerUid,
        startMessageSeq: body.startMessageSeq,
        endMessageSeq: body.endMessageSeq,
        limit: body.limit,
        pullMode: body.pullMode,
      });
      return jsonResponse(200, { messages });
      // docs:end bff-sync-messages
    }

    return jsonResponse(404, { error: "not found" });
  };
}
