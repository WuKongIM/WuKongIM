import type {
  DevelopmentTokenInput,
  PersonMessageSyncInput,
  RouteAddresses,
  SyncedPersonMessage,
} from "./bff";

interface ProductHttpClientOptions {
  baseUrl: string;
  fetch?: FetchLike;
}

type FetchLike = (
  input: RequestInfo | URL,
  init?: RequestInit,
) => Promise<Response>;

function requiredString(
  value: unknown,
  field: string,
  options: { allowEmpty?: boolean } = {},
): string {
  if (typeof value !== "string" || (!options.allowEmpty && value.length === 0)) {
    throw new Error(`Product HTTP response has invalid ${field}`);
  }
  return value;
}

function nonNegativeInteger(value: unknown, field: string): number {
  if (!Number.isSafeInteger(value) || Number(value) < 0) {
    throw new Error(`Product HTTP response has invalid ${field}`);
  }
  return Number(value);
}

/** Calls WuKongIM Product HTTP only from the trusted Node.js process. */
export class ProductHttpClient {
  readonly #baseUrl: string;
  readonly #fetch: FetchLike;

  constructor(options: ProductHttpClientOptions) {
    this.#baseUrl = options.baseUrl.replace(/\/+$/, "");
    this.#fetch = options.fetch ?? globalThis.fetch;
  }

  async updateToken(input: DevelopmentTokenInput): Promise<void> {
    // docs:start product-http-token
    const response = await this.#fetch(`${this.#baseUrl}/user/token`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        uid: input.uid,
        token: input.token,
        device_flag: input.deviceFlag,
        device_level: input.deviceLevel,
      }),
    });
    // docs:end product-http-token

    if (!response.ok) {
      throw new Error(`POST /user/token failed with HTTP ${response.status}`);
    }
  }

  async discoverRoute(): Promise<RouteAddresses> {
    // docs:start product-http-route
    const response = await this.#fetch(`${this.#baseUrl}/route`);
    // docs:end product-http-route
    if (!response.ok) {
      throw new Error(`GET /route failed with HTTP ${response.status}`);
    }
    const body = (await response.json()) as Record<string, unknown>;
    return {
      tcpAddress: requiredString(body.tcp_addr, "tcp_addr", { allowEmpty: true }),
      websocketAddress: requiredString(body.ws_addr, "ws_addr", {
        allowEmpty: true,
      }),
      secureWebsocketAddress: requiredString(body.wss_addr, "wss_addr", {
        allowEmpty: true,
      }),
    };
  }

  async syncPersonMessages(
    input: PersonMessageSyncInput,
  ): Promise<SyncedPersonMessage[]> {
    // docs:start product-http-message-sync
    const response = await this.#fetch(
      `${this.#baseUrl}/channel/messagesync`,
      {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          login_uid: input.loginUid,
          channel_id: input.peerUid,
          channel_type: 1,
          start_message_seq: input.startMessageSeq,
          end_message_seq: input.endMessageSeq,
          limit: input.limit,
          pull_mode: input.pullMode,
        }),
      },
    );
    // docs:end product-http-message-sync
    if (!response.ok) {
      throw new Error(
        `POST /channel/messagesync failed with HTTP ${response.status}`,
      );
    }
    const body = (await response.json()) as { messages?: unknown };
    if (!Array.isArray(body.messages)) {
      throw new Error("POST /channel/messagesync returned invalid messages");
    }
    return body.messages.map((value) => {
      if (typeof value !== "object" || value === null) {
        throw new Error("POST /channel/messagesync returned an invalid message");
      }
      const message = value as Record<string, unknown>;
      return {
        // Keep the server's decimal string. Converting int64 message_id through
        // JavaScript number can silently lose precision.
        messageId: requiredString(message.message_idstr, "message_idstr"),
        messageSeq: nonNegativeInteger(message.message_seq, "message_seq"),
        clientMsgNo: requiredString(message.client_msg_no, "client_msg_no"),
        fromUid: requiredString(message.from_uid, "from_uid"),
        timestamp: nonNegativeInteger(message.timestamp, "timestamp"),
        payload: requiredString(message.payload, "payload", { allowEmpty: true }),
      };
    });
  }
}
