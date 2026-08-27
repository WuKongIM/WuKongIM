export interface DecodedTextPayload {
  type: 1;
  text: string;
}

/** Decodes the text-only payload contract deliberately used by this example. */
export function decodeTextPayload(payload: string): DecodedTextPayload {
  const bytes = Uint8Array.from(globalThis.atob(payload), (value) =>
    value.charCodeAt(0),
  );
  const value = JSON.parse(new TextDecoder().decode(bytes)) as {
    type?: unknown;
    content?: unknown;
  };
  if (value.type !== 1 || typeof value.content !== "string") {
    throw new Error("the quickstart only recovers text messages (type 1)");
  }
  return { type: 1, text: value.content };
}
