// Catalog redirects carry their real address across Demo origins and reloads.
// Embedded entries fall back to their own catalog; Node entries publish theirs.
export function demoHomeURL(development = false): string {
  const normalize = (value: string | null | undefined): URL | undefined => {
    if (!value) return;
    try {
      const url = new URL(value, location.origin);
      if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash || !['/demos', '/demos/'].includes(url.pathname)) return;
      url.pathname = '/demos/';
      return url;
    } catch { return; }
  };
  const requested = normalize(new URLSearchParams(location.search).get('home'));
  // A query may select the same origin or a loopback preview, never another site.
  if (requested && (requested.origin === location.origin || ['127.0.0.1', 'localhost', '[::1]'].includes(requested.hostname))) return requested.href;
  const configured = normalize(document.querySelector<HTMLMetaElement>('meta[name="wk-demo-home"]')?.content);
  return configured?.href || new URL('/demos/', development ? 'http://127.0.0.1:5001' : location.origin).href;
}
