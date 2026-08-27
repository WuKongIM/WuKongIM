import WKSDK, {
  Channel,
  ChannelTypePerson,
  ConnectStatus,
  Message,
  MessageStatus,
  MessageText,
  PullMode,
  SyncOptions,
  type ConnectStatusListener,
  type MessageListener,
  type MessageStatusListener,
} from "wukongimjssdk";

import {
  type BrowserBffClient,
  type BrowserSyncedMessage,
} from "./browser-bff";
import {
  type ChatRuntime,
  type DevelopmentIdentity,
  type RuntimeMessage,
  type SendAcknowledgement,
} from "./session";
import { decodeTextPayload } from "./payload";

interface PendingConnection {
  resolve: (value: { nodeId?: number }) => void;
  reject: (error: Error) => void;
  timer: ReturnType<typeof setTimeout>;
}

const DISCONNECT_TIMEOUT_MS = 3_000;

/** Adapts the pinned wukongimjssdk singleton to one iframe session. */
export class WukongIMRuntime implements ChatRuntime {
  readonly #bff: BrowserBffClient;
  readonly #sdk: WKSDK;
  readonly #connectTimeoutMs: number;
  readonly #messageListeners = new Set<(message: RuntimeMessage) => void>();
  readonly #ackListeners = new Set<(ack: SendAcknowledgement) => void>();
  readonly #disconnectListeners = new Set<(reasonCode?: number) => void>();
  #uid = "";
  #manualDisconnect = false;
  #connected = false;
  #pendingConnection?: PendingConnection;

  readonly #connectStatusListener: ConnectStatusListener = (
    status,
    reasonCode,
    connectionInfo,
  ) => {
    if (status === ConnectStatus.Connected) {
      this.#connected = true;
      this.#manualDisconnect = false;
      this.#resolveConnection(connectionInfo?.nodeId);
      return;
    }
    if (status === ConnectStatus.ConnectFail || status === ConnectStatus.ConnectKick) {
      this.#rejectConnection(
        new Error(`SDK connection rejected · reason ${reasonCode ?? 0}`),
      );
    }
    if (status === ConnectStatus.Disconnect || status === ConnectStatus.ConnectKick) {
      const wasConnected = this.#connected;
      this.#connected = false;
      if (wasConnected && !this.#manualDisconnect) {
        for (const listener of this.#disconnectListeners) listener(reasonCode);
      }
    }
  };

  readonly #messageListener: MessageListener = (message) => {
    if (message.fromUID === this.#uid) return;
    const observed = runtimeMessageFromSdk(message);
    for (const listener of this.#messageListeners) listener(observed);
  };

  readonly #messageStatusListener: MessageStatusListener = (ack) => {
    const observed: SendAcknowledgement = {
      clientSeq: ack.clientSeq,
      messageSeq: ack.messageSeq,
      reasonCode: ack.reasonCode,
    };
    for (const listener of this.#ackListeners) listener(observed);
  };

  constructor(
    bff: BrowserBffClient,
    sdk: WKSDK = WKSDK.shared(),
    connectTimeoutMs = 10_000,
  ) {
    this.#bff = bff;
    this.#sdk = sdk;
    this.#connectTimeoutMs = connectTimeoutMs;
    this.#sdk.connectManager.addConnectStatusListener(
      this.#connectStatusListener,
    );
    this.#sdk.chatManager.addMessageListener(this.#messageListener);
    this.#sdk.chatManager.addMessageStatusListener(this.#messageStatusListener);
  }

  async connect(identity: DevelopmentIdentity): Promise<{ nodeId?: number }> {
    if (this.#pendingConnection) {
      throw new Error("an SDK connection attempt is already in progress");
    }
    if (this.#connected) {
      throw new Error("the SDK is already connected; disconnect it before reconnecting");
    }
    this.#uid = identity.uid;
    this.#manualDisconnect = false;

    // docs:start sdk-configure-and-connect
    const config = this.#sdk.config;
    config.uid = identity.uid;
    config.token = identity.token;
    config.addr = identity.websocketUrl;
    config.deviceFlag = 1;
    config.provider.syncMessagesCallback = async (channel, options) => {
      if (channel.channelType !== ChannelTypePerson) {
        throw new Error("the quickstart only syncs person channels");
      }
      const messages = await this.#bff.syncPersonMessages({
        uid: identity.uid,
        peerUid: channel.channelID,
        startMessageSeq: options.startMessageSeq,
        endMessageSeq: options.endMessageSeq,
        limit: Math.min(options.limit, 100),
        pullMode: options.pullMode,
      });
      return messages.map((message) => syncedMessageToSdk(message, channel));
    };
    this.#sdk.config = config;
    // docs:end sdk-configure-and-connect

    const result = new Promise<{ nodeId?: number }>((resolve, reject) => {
      this.#pendingConnection = {
        resolve,
        reject,
        timer: setTimeout(() => {
          this.#rejectConnection(new Error("SDK connection timed out"));
        }, this.#connectTimeoutMs),
      };
    });
    this.#sdk.connect();
    return result;
  }

  async disconnect(): Promise<void> {
    this.#manualDisconnect = true;
    this.#connected = false;
    this.#rejectConnection(new Error("SDK connection cancelled"));
    const socket = this.#sdk.connectManager.ws?.ws as WebSocket | undefined;
    const close = waitForSocketClose(socket, DISCONNECT_TIMEOUT_MS);
    this.#sdk.disconnect();
    await close;
  }

  async sendText(peerUid: string, text: string): Promise<{ clientSeq: number }> {
    // docs:start sdk-send-text
    const message = await this.#sdk.chatManager.send(
      new MessageText(text),
      new Channel(peerUid, ChannelTypePerson),
    );
    // docs:end sdk-send-text
    return { clientSeq: message.clientSeq };
  }

  async syncMessages(peerUid: string): Promise<RuntimeMessage[]> {
    // docs:start sdk-reconnect-sync
    const options = new SyncOptions();
    options.startMessageSeq = 0;
    options.endMessageSeq = 0;
    options.limit = 50;
    options.pullMode = PullMode.Up;
    const messages = await this.#sdk.chatManager.syncMessages(
      new Channel(peerUid, ChannelTypePerson),
      options,
    );
    // docs:end sdk-reconnect-sync
    return messages.map(runtimeMessageFromSdk);
  }

  onMessage(listener: (message: RuntimeMessage) => void): () => void {
    this.#messageListeners.add(listener);
    return () => this.#messageListeners.delete(listener);
  }

  onSendAcknowledgement(
    listener: (ack: SendAcknowledgement) => void,
  ): () => void {
    this.#ackListeners.add(listener);
    return () => this.#ackListeners.delete(listener);
  }

  onUnexpectedDisconnect(listener: (reasonCode?: number) => void): () => void {
    this.#disconnectListeners.add(listener);
    return () => this.#disconnectListeners.delete(listener);
  }

  #resolveConnection(nodeId?: number): void {
    const pending = this.#pendingConnection;
    if (!pending) return;
    clearTimeout(pending.timer);
    this.#pendingConnection = undefined;
    pending.resolve(nodeId === undefined ? {} : { nodeId });
  }

  #rejectConnection(error: Error): void {
    const pending = this.#pendingConnection;
    if (!pending) return;
    clearTimeout(pending.timer);
    this.#pendingConnection = undefined;
    pending.reject(error);
  }
}

function waitForSocketClose(
  socket: WebSocket | undefined,
  timeoutMs: number,
): Promise<void> {
  if (!socket || typeof socket.addEventListener !== "function") {
    return Promise.reject(new Error("cannot observe the browser WebSocket close"));
  }
  if (socket.readyState === WebSocket.CLOSED) return Promise.resolve();

  return new Promise<void>((resolve, reject) => {
    const onClose = () => {
      clearTimeout(timer);
      resolve();
    };
    const timer = setTimeout(() => {
      socket.removeEventListener("close", onClose);
      reject(new Error("browser WebSocket close timed out"));
    }, timeoutMs);
    socket.addEventListener("close", onClose, { once: true });
  });
}

function syncedMessageToSdk(
  value: BrowserSyncedMessage,
  channel: Channel,
): Message {
  const decoded = decodeTextPayload(value.payload);
  const message = new Message();
  message.messageID = value.messageId;
  message.messageSeq = value.messageSeq;
  message.clientMsgNo = value.clientMsgNo;
  message.fromUID = value.fromUid;
  message.timestamp = value.timestamp;
  message.channel = channel;
  message.content = new MessageText(decoded.text);
  message.status = MessageStatus.Normal;
  return message;
}

function runtimeMessageFromSdk(message: Message): RuntimeMessage {
  const text =
    message.content instanceof MessageText && typeof message.content.text === "string"
      ? message.content.text
      : message.content?.conversationDigest ?? "[non-text message]";
  return {
    messageId: message.messageID,
    messageSeq: message.messageSeq,
    clientMsgNo: message.clientMsgNo,
    fromUid: message.fromUID,
    text,
  };
}
