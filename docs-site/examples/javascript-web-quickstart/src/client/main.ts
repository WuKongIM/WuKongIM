import { BrowserBffClient } from "./browser-bff";
import { WukongIMRuntime } from "./sdk-runtime";
import { QuickstartSession, type SessionSnapshot } from "./session";

const query = new URLSearchParams(globalThis.location.search);
const uid = validUid(query.get("uid")) ?? "alice";
const peerUid = validUid(query.get("peer")) ?? (uid === "alice" ? "bob" : "alice");

const elements = {
  identity: required<HTMLElement>("identity"),
  peer: required<HTMLElement>("peer"),
  connection: required<HTMLElement>("connection-status"),
  node: required<HTMLElement>("node-id"),
  connect: required<HTMLButtonElement>("connect-button"),
  disconnect: required<HTMLButtonElement>("disconnect-button"),
  reconnect: required<HTMLButtonElement>("reconnect-sync-button"),
  form: required<HTMLFormElement>("message-form"),
  input: required<HTMLInputElement>("message-input"),
  send: required<HTMLButtonElement>("send-button"),
  events: required<HTMLOListElement>("event-log"),
  error: required<HTMLElement>("ui-error"),
};

const bff = new BrowserBffClient();
const runtime = new WukongIMRuntime(bff);
const session = new QuickstartSession({ uid, peerUid, bff, runtime });

elements.identity.textContent = uid;
elements.peer.textContent = peerUid;
session.subscribe(render);

elements.connect.addEventListener("click", () => void run(() => session.connect()));
elements.disconnect.addEventListener("click", () => void run(() => session.disconnect()));
elements.reconnect.addEventListener("click", () =>
  void run(() => session.reconnectAndSync()),
);
elements.form.addEventListener("submit", (event) => {
  event.preventDefault();
  const text = elements.input.value;
  void run(async () => {
    await session.sendText(text);
    elements.input.value = "";
    elements.input.focus();
  });
});

async function run(action: () => Promise<void>): Promise<void> {
  elements.error.hidden = true;
  try {
    await action();
  } catch (error) {
    elements.error.textContent =
      error instanceof Error ? error.message : "The quickstart action failed";
    elements.error.hidden = false;
  }
}

function render(snapshot: SessionSnapshot): void {
  elements.connection.textContent = snapshot.connection;
  elements.connection.dataset.state = snapshot.connection;
  elements.node.textContent = snapshot.nodeId === undefined ? "—" : String(snapshot.nodeId);
  elements.connect.disabled =
    snapshot.connection === "connecting" || snapshot.connection === "connected";
  elements.disconnect.disabled = snapshot.connection !== "connected";
  elements.reconnect.disabled =
    snapshot.connection !== "disconnected" && snapshot.connection !== "failed";
  elements.input.disabled = snapshot.connection !== "connected";
  elements.send.disabled = snapshot.connection !== "connected";

  const fragment = document.createDocumentFragment();
  for (const event of snapshot.events) {
    const item = document.createElement("li");
    item.dataset.eventKind = event.kind;
    item.dataset.testid = `event-${event.kind}`;
    const label = document.createElement("span");
    label.className = "event-kind";
    label.textContent = event.kind;
    const text = document.createElement("span");
    text.textContent = event.text;
    item.append(label, text);
    fragment.append(item);
  }
  elements.events.replaceChildren(fragment);
  elements.events.scrollTop = elements.events.scrollHeight;
}

function required<T extends HTMLElement>(testId: string): T {
  const element = document.querySelector<T>(`[data-testid="${testId}"]`);
  if (!element) throw new Error(`missing UI element: ${testId}`);
  return element;
}

function validUid(value: string | null): string | undefined {
  return value !== null && /^[A-Za-z0-9._-]{1,64}$/.test(value)
    ? value
    : undefined;
}
