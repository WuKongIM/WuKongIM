const fnvOffsetBasis64 = 0xcbf29ce484222325n
const fnvPrime64 = 0x100000001b3n

function escapeXML(value: string): string {
  return value.replace(/[&<>"']/g, (character) => {
    switch (character) {
      case "&":
        return "&amp;"
      case "<":
        return "&lt;"
      case ">":
        return "&gt;"
      case '"':
        return "&quot;"
      default:
        return "&apos;"
    }
  })
}

function hashUID(uid: string): bigint {
  let hash = fnvOffsetBasis64
  for (const character of uid) {
    hash ^= BigInt(character.codePointAt(0) ?? 0)
    hash = BigInt.asUintN(64, hash * fnvPrime64)
  }
  return hash
}

function identiconPath(hash: bigint): string {
  const cells: string[] = []

  for (let index = 0; index < 64; index += 1) {
    if ((hash & (1n << BigInt(index))) === 0n) {
      continue
    }

    const column = index % 8
    const row = Math.floor(index / 8)
    const x = column * 8
    const y = row * 8
    cells.push(`M${x} ${y}h8v8H${x}z`)
  }

  return cells.join("") || "M24 24h16v16H24z"
}

// avatarURLForUID returns a deterministic, self-contained avatar for the exact UID.
export function avatarURLForUID(uid: string): string {
  const displayUID = uid || "?"
  const initials = Array.from(displayUID).slice(0, 2).join("").toUpperCase()
  const hash = hashUID(uid)
  const hue = Number(hash % 360n)
  const saturation = 58 + Number((hash >> 9n) % 18n)
  const lightness = 36 + Number((hash >> 17n) % 8n)
  const background = `hsl(${hue} ${saturation}% ${lightness}%)`
  const escapedUID = escapeXML(displayUID)
  const escapedInitials = escapeXML(initials)
  const svg = [
    '<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64" viewBox="0 0 64 64" role="img"',
    ` aria-label="Avatar ${escapedUID}">`,
    `<rect width="64" height="64" rx="16" fill="${background}"/>`,
    `<path d="${identiconPath(hash)}" fill="#FFFFFF" opacity="0.16"/>`,
    '<text x="32" y="33" fill="#FFFFFF" font-family="system-ui, sans-serif" font-size="24"',
    ` font-weight="600" text-anchor="middle" dominant-baseline="central">${escapedInitials}</text>`,
    "</svg>",
  ].join("")
  return `data:image/svg+xml;charset=UTF-8,${encodeURIComponent(svg)}`
}
