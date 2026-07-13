const utf8Decoder = new TextDecoder("utf-8", { fatal: true })

function hasUnsupportedControlCharacter(value: string) {
  for (const character of value) {
    const codePoint = character.codePointAt(0) ?? 0
    const isAllowedWhitespace = codePoint === 0x09 || codePoint === 0x0a || codePoint === 0x0d
    if (!isAllowedWhitespace && (codePoint < 0x20 || (codePoint >= 0x7f && codePoint <= 0x9f))) {
      return true
    }
  }
  return false
}

// decodeManagerMessagePayload renders textual payloads while preserving binary payloads as base64.
export function decodeManagerMessagePayload(value: string) {
  if (!value) {
    return ""
  }
  try {
    const binary = atob(value)
    const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0))
    const decoded = utf8Decoder.decode(bytes)
    return hasUnsupportedControlCharacter(decoded) ? value : decoded
  } catch {
    return value
  }
}
