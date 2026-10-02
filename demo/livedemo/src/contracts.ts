export type Viewer = { uid: string; name: string; muted: boolean; pending: boolean };
export type Snapshot = { roomId: string; version: number; hostUid: string; notice: string; viewers: Viewer[]; pending: null | { requestId: string; targetUid: string; desiredMuted: boolean } };
export type Bootstrap = { roomId: string; invite: string; hostUid: string; wsUrl: string; viewer: { uid: string; token: string; name: string }; viewerKey: string; ownerKey?: string; snapshot: Snapshot };
export type Notification = { status: 'confirmed' | 'pending'; requestId: string; roomVersion: number };
export type ControlIntent = { roomId: string; requestId: string; kind: 'notice'; text: string } | { roomId: string; requestId: string; kind: 'mute'; targetUid: string; muted: boolean };
export type ControlResult = { snapshot: Snapshot; operation: { requestId: string; status: 'confirmed' | 'pending' }; notification: Notification };
export type AudiencePayload = { type: 1; content: string; live_demo: { kind: 'barrage' | 'like'; roomId: string; eventId: string } };
export const uuid = (value: unknown): value is string => typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(value);
export const text = (value: unknown, max = 256): value is string => typeof value === 'string' && value.length > 0 && value.length <= max;
const record = (value: unknown): value is Record<string, any> => !!value && typeof value === 'object' && !Array.isArray(value);
export function snapshot(value: unknown, roomId: string, hostUid: string, viewerUid = ''): value is Snapshot {
  if (!record(value) || value.roomId !== roomId || value.hostUid !== hostUid || !Number.isSafeInteger(value.version) || value.version < 1 || typeof value.notice !== 'string' || [...value.notice].length > 120 || !Array.isArray(value.viewers) || value.viewers.length > 16) return false;
  const users = new Set<string>();
  for (const viewer of value.viewers) {
    if (!record(viewer) || !text(viewer.uid) || !text(viewer.name, 64) || typeof viewer.muted !== 'boolean' || typeof viewer.pending !== 'boolean' || users.has(viewer.uid) || viewer.uid === hostUid) return false;
    users.add(viewer.uid);
  }
  if (viewerUid && !users.has(viewerUid)) return false;
  if (value.pending === null) return value.viewers.every((viewer: Viewer) => !viewer.pending);
  return record(value.pending) && uuid(value.pending.requestId) && users.has(value.pending.targetUid) && typeof value.pending.desiredMuted === 'boolean' && value.viewers.every((viewer: Viewer) => viewer.pending === (viewer.uid === value.pending.targetUid));
}
export function bootstrap(value: unknown): value is Bootstrap {
  if (!record(value) || !text(value.roomId) || !text(value.invite, 512) || !text(value.hostUid) || !text(value.viewerKey, 1024) || !record(value.viewer) || !text(value.viewer.uid) || !text(value.viewer.token, 1024) || !text(value.viewer.name, 64) || !text(value.wsUrl, 2048)) return false;
  try { const url = new URL(value.wsUrl); if (!['ws:', 'wss:'].includes(url.protocol) || url.username || url.password) return false; } catch { return false; }
  return (value.ownerKey === undefined || text(value.ownerKey, 1024)) && snapshot(value.snapshot, value.roomId, value.hostUid, value.viewer.uid);
}
export function notification(value: unknown): value is Notification {
  return record(value) && ['confirmed', 'pending'].includes(value.status) && uuid(value.requestId) && Number.isSafeInteger(value.roomVersion) && value.roomVersion > 0;
}
export function controlResult(value: unknown, session: Bootstrap): value is ControlResult {
  return record(value) && snapshot(value.snapshot, session.roomId, session.hostUid, session.viewer.uid) && record(value.operation) && uuid(value.operation.requestId) && ['confirmed', 'pending'].includes(value.operation.status) && notification(value.notification);
}
export function decodePayload(value: unknown): Record<string, any> | undefined {
  if (typeof value === 'string') {
    if (value.length > 6000) return;
    try { value = JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(value), c => c.charCodeAt(0)))); } catch { return; }
  }
  if (!record(value)) return;
  try { if (new TextEncoder().encode(JSON.stringify(value)).length > 4096) return; } catch { return; }
  return value;
}
