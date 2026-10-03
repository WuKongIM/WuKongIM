// Only connection credentials survive a reload; SDK messages and cursors stay in memory.
export interface DemoSession {
    apiURL: string
    uid: string
    token: string
}

const sessionKey = 'wukongim.demo.session.v1'
type SessionStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>

export function normalizeSession(input: DemoSession): DemoSession {
    if (!input.uid.trim() || !input.token.trim()) throw new Error('credentials_required')
    const url = new URL(input.apiURL)
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) {
        throw new Error('invalid_api_url')
    }
    return { apiURL: url.toString().replace(/\/$/, ''), uid: input.uid, token: input.token }
}

export function loadSession(storage: SessionStorage): DemoSession | null {
    try {
        const raw = storage.getItem(sessionKey)
        if (!raw) return null
        const value = JSON.parse(raw)
        if (!value || typeof value.apiURL !== 'string' || typeof value.uid !== 'string' || typeof value.token !== 'string') return null
        return normalizeSession(value)
    } catch {
        return null
    }
}

export function saveSession(storage: SessionStorage, value: DemoSession): void {
    storage.setItem(sessionKey, JSON.stringify(normalizeSession(value)))
}

export function clearSession(storage: SessionStorage): void {
    storage.removeItem(sessionKey)
}

// The checkbox controls test-account registration; unchecked login only uses existing credentials.
export async function establishSession(
    storage: SessionStorage,
    input: DemoSession,
    createDemoCredentials: boolean,
    register: (session: DemoSession) => Promise<unknown>,
): Promise<DemoSession> {
    const session = normalizeSession(input)
    if (createDemoCredentials) await register(session)
    saveSession(storage, session)
    return session
}
