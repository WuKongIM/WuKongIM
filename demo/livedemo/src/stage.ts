type Visual = { text: string; kind: 'barrage' | 'like'; expires: number };

/** Bounded, disposable live visuals. Chat persistence and delivery live elsewhere. */
export class LiveStage {
  // Newest 30 visuals only; every queued item expires after six seconds.
  private queue: Visual[] = [];
  // At most 12 nodes, removed by their animation or a bounded deadline.
  private active = new Map<HTMLElement, number>();
  private tracks = Array.from({ length: 4 }, () => ({ active: 0, last: 0 }));
  private ticker: ReturnType<typeof setInterval>;
  private enabled = true;
  private reduced = matchMedia('(prefers-reduced-motion: reduce)');
  constructor(private readonly element: HTMLElement) {
    this.ticker = setInterval(() => this.pump(), 180);
    this.reduced.addEventListener('change', this.clear);
    document.addEventListener('visibilitychange', this.visibility);
  }
  enqueue(content: string, kind: Visual['kind']) {
    if (!this.enabled || this.reduced.matches || document.hidden || this.element.offsetParent === null) return;
    this.queue.push({ text: content, kind, expires: Date.now() + 6000 });
    if (this.queue.length > 30) this.queue.shift();
    this.pump();
  }
  toggle(enabled: boolean) { this.enabled = enabled; if (!enabled) this.clear(); }
  private visibility = () => { if (document.hidden) this.clear(); };
  clear = () => {
    this.queue = []; for (const node of this.active.keys()) node.remove(); this.active.clear();
    this.tracks.forEach(track => { track.active = 0; track.last = 0; });
    this.element.dataset.queueSize = '0'; this.element.dataset.activeSize = '0';
  };
  private remove(node: HTMLElement) {
    if (!this.active.has(node)) return;
    const track = Number(node.dataset.track); if (Number.isInteger(track)) this.tracks[track].active--;
    this.active.delete(node); node.remove();
  }
  private pump() {
    const now = Date.now();
    for (const [node, deadline] of this.active) if (deadline <= now) this.remove(node);
    this.queue = this.queue.filter(item => item.expires > now);
    if (document.hidden || this.reduced.matches || !this.enabled || this.element.offsetParent === null) { this.clear(); return; }
    const item = this.queue[0];
    if (item && this.active.size < 12) {
      const index = this.tracks.findIndex(track => track.active < 3 && now - track.last >= 1800);
      const likes = [...this.active.keys()].filter(node => node.classList.contains('live-heart')).length;
      if ((item.kind === 'like' && likes < 4) || (item.kind === 'barrage' && index >= 0)) {
        this.queue.shift(); const node = document.createElement('span'); node.textContent = item.text;
        node.className = item.kind === 'like' ? 'live-heart' : 'live-barrage'; node.dataset.liveVisual = item.kind; node.dataset.testid = item.kind === 'like' ? 'like-pulse' : 'barrage-item';
        if (item.kind === 'barrage') {
          node.dataset.track = String(index); node.style.top = `${13 + index * 16}%`;
          this.tracks[index].active++; this.tracks[index].last = now;
        }
        this.element.append(node); node.style.setProperty('--distance', `${this.element.clientWidth + node.offsetWidth + 30}px`);
        this.active.set(node, now + (item.kind === 'like' ? 2400 : 8100));
        node.addEventListener('animationend', () => this.remove(node), { once: true });
      }
    }
    this.element.dataset.queueSize = String(this.queue.length); this.element.dataset.activeSize = String(this.active.size);
  }
  destroy() { clearInterval(this.ticker); this.clear(); this.reduced.removeEventListener('change', this.clear); document.removeEventListener('visibilitychange', this.visibility); }
}
