import type {
  DevelopmentTokenInput,
  PersonMessageSyncInput,
  RouteAddresses,
  SyncedPersonMessage,
} from "./bff";

interface ProductHttpClientOptions {
  baseUrl: string;
  fetch?: FetchLike;
  personDirectoryRetry?: Partial<PersonDirectoryRetry>;
}

interface PersonDirectoryRetry {
  maxAttempts: number;
  delayMs: number;
  wait: (delayMs: number) => Promise<void>;
}

type FetchLike = (
  input: RequestInfo | URL,
  init?: RequestInit,
) => Promise<Response>;

const PERSON_DIRECTORY_PENDING_MESSAGE =
  "internal/message: valid channel membership required";
const DEFAULT_PERSON_DIRECTORY_RETRY: PersonDirectoryRetry = {
  maxAttempts: 20,
  delayMs: 250,
  wait: (delayMs) =>
    new Promise((resolve) => {
      setTimeout(resolve, delayMs);
    }),
};

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
  readonly #personDirectoryRetry: PersonDirectoryRetry;

  constructor(options: ProductHttpClientOptions) {
    this.#baseUrl = options.baseUrl.replace(/\/+$/, "");
    this.#fetch = options.fetch ?? globalThis.fetch;
    this.#personDirectoryRetry = {
      ...DEFAULT_PERSON_DIRECTORY_RETRY,
      ...options.personDirectoryRetry,
    };
    if (
      !Number.isSafeInteger(this.#personDirectoryRetry.maxAttempts) ||
      this.#personDirectoryRetry.maxAttempts < 1 ||
      this.#personDirectoryRetry.maxAttempts > 100
    ) {
      throw new Error("person-directory retry attempts must be from 1 to 100");
    }
    if (
      !Number.isSafeInteger(this.#personDirectoryRetry.delayMs) ||
      this.#personDirectoryRetry.delayMs < 0 ||
      this.#personDirectoryRetry.delayMs > 5_000
    ) {
      throw new Error("person-directory retry delay must be from 0 to 5000ms");
    }
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
    for (
      let attempt = 1;
      attempt <= this.#personDirectoryRetry.maxAttempts;
      attempt += 1
    ) {
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
        const productMessage = await productErrorMessage(response);
        const projectionPending =
          response.status === 400 &&
          productMessage === PERSON_DIRECTORY_PENDING_MESSAGE;
        if (
          projectionPending &&
          attempt < this.#personDirectoryRetry.maxAttempts
        ) {
          await this.#personDirectoryRetry.wait(
            this.#personDirectoryRetry.delayMs,
          );
          continue;
        }
        throw new Error(
          `POST /channel/messagesync failed with HTTP ${response.status}`,
        );
      }
      return parseSyncedMessages(await response.json());
    }

    throw new Error("POST /channel/messagesync exhausted its bounded retry");
  }
}

async function productErrorMessage(
  response: Response,
): Promise<string | undefined> {
  try {
    const body = (await response.json()) as { msg?: unknown };
    return typeof body.msg === "string" && body.msg.length <= 256
      ? body.msg
      : undefined;
  } catch {
    return undefined;
  }
}

function parseSyncedMessages(value: unknown): SyncedPersonMessage[] {
  const body = value as { messages?: unknown };
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
