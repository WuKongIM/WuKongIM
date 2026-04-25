export function getManagerApiBaseUrl() {
  const raw = import.meta.env.VITE_API_BASE_URL?.trim() ?? ""
  if (!raw) {
    return ""
  }

  return raw.replace(/\/+$/, "")
}
