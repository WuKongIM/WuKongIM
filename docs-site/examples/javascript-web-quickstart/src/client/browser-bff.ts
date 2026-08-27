import type { DevelopmentIdentity, QuickstartBff } from "./session";

type FetchLike = (
  input: RequestInfo | URL,
  init?: RequestInit,
) => Promise<Response>;

interface BrowserBffClientOptions {
  fetch?: FetchLike;
  baseUrl?: string;
}

export interface BrowserSyncedMessage {
  messageId: string;
  messageSeq: number;
  clientMsgNo: string;
  fromUid: string;
  timestamp: number;
  payload: string;
}

export interface BrowserMessageSyncInput {
  uid: string;
  peerUid: string;
  startMessageSeq: number;
  endMessageSeq: number;
  limit: number;
  pullMode: 0 | 1;
}

/** Same-origin browser client; it never knows the Product HTTP base URL. */
export class BrowserBffClient implements QuickstartBff {
  readonly #fetch: FetchLike;
  readonly #baseUrl: string;

  constructor(options: BrowserBffClientOptions = {}) {
    this.#fetch = options.fetch ?? globalThis.fetch.bind(globalThis);
    this.#baseUrl =
      options.baseUrl ??
      (typeof globalThis.location === "undefined"
        ? "http://localhost"
        : globalThis.location.origin);
  }

  async provisionIdentity(uid: string): Promise<DevelopmentIdentity> {
    // docs:start browser-provision-identity
    const response = await this.#post("/api/development/identity", { uid });
    // docs:end browser-provision-identity
    const value = response as Partial<DevelopmentIdentity>;
    if (
      typeof value.uid !== "string" ||
      typeof value.token !== "string" ||
      typeof value.websocketUrl !== "string"
    ) {
      throw new Error("BFF returned an invalid development identity");
    }
    return {
      uid: value.uid,
      token: value.token,
      websocketUrl: value.websocketUrl,
    };
  }

  async syncPersonMessages(
    input: BrowserMessageSyncInput,
  ): Promise<BrowserSyncedMessage[]> {
    // docs:start browser-sync-messages
    const response = await this.#post("/api/messages/sync", input);
    // docs:end browser-sync-messages
    const value = response as { messages?: unknown };
    if (!Array.isArray(value.messages)) {
      throw new Error("BFF returned an invalid message page");
    }
    return value.messages as BrowserSyncedMessage[];
  }

  async #post(path: string, body: unknown): Promise<unknown> {
    const response = await this.#fetch(new URL(path, this.#baseUrl), {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body),
    });
    const value = (await response.json()) as { error?: unknown };
    if (!response.ok) {
      throw new Error(
        typeof value.error === "string"
          ? value.error
          : `BFF request failed with HTTP ${response.status}`,
      );
    }
    return value;
  }
}
