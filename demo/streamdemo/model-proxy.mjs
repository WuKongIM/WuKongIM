import { once } from 'node:events';

// This proxy belongs to the local demo process, not the Product API. It keeps
// keys request-scoped and never logs upstream bodies, headers, URLs or errors.
export function createModelProxy() {
  const active = new Set();
  const handler = async (req, res) => {
    res.setHeader('Cache-Control', 'no-store');
    res.setHeader('X-Content-Type-Options', 'nosniff');
    const fail = (status, error) => {
      if (res.destroyed) return;
      if (res.headersSent) { res.end(`data: ${JSON.stringify({ error: { message: error } })}\n\n`); return; }
      res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify({ error }));
    };
    const host = req.headers.host;
    let local = false;
    try { local = ['127.0.0.1', 'localhost', '[::1]'].includes(new URL(`http://${host}`).hostname); } catch { /* Reject invalid authorities. */ }
    if (!local || req.headers.origin && ![`http://${host}`, `https://${host}`].includes(req.headers.origin) || req.headers['sec-fetch-site'] === 'cross-site') return fail(403, '模型代理仅允许本机同源请求。');
    if (req.method !== 'POST') return fail(405, '仅支持 POST。');
    if (!req.headers['content-type']?.startsWith('application/json')) return fail(415, '请求必须为 JSON。');
    if (active.size >= 4) return fail(503, '模型代理繁忙，请稍后再试。');
    const controller = new AbortController(); active.add(controller);
    const timer = setTimeout(() => controller.abort(), 180000);
    const bodyTimer = setTimeout(() => { fail(408, '模型请求正文读取超时。'); req.destroy(); }, 15000);
    const closed = () => controller.abort(); res.once('close', closed);
    try {
      let size = 0; const chunks = [];
      for await (const chunk of req) {
        size += chunk.length;
        if (size > 128 * 1024) return fail(413, '模型请求超出 128 KiB。');
        chunks.push(chunk);
      }
      clearTimeout(bodyTimer);
      let input;
      try { input = JSON.parse(Buffer.concat(chunks).toString('utf8')); } catch { return fail(400, '模型请求不是有效 JSON。'); }
      let url;
      try { url = new URL(input.url); } catch { return fail(400, '请输入有效的模型 URL。'); }
      if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.hash || url.href.length > 2048) return fail(400, '模型 URL 必须为 HTTP(S)，且不含凭据或片段。');
      if (!url.pathname.endsWith('/chat/completions')) return fail(400, '需要 Chat Completions URL。');
      if (typeof input.api_key !== 'string' || input.api_key.length > 16384 || /[\r\n]/.test(input.api_key)) return fail(400, 'API Key 格式无效。');
      if (!Array.isArray(input.messages) || !input.messages.length || input.messages.length > 40 || input.messages.some(message => !['user', 'assistant'].includes(message.role) || typeof message.content !== 'string')) return fail(400, '对话上下文格式无效。');
      const headers = { 'Content-Type': 'application/json' };
      if (input.api_key) headers.Authorization = `Bearer ${input.api_key}`;
      let model = typeof input.model === 'string' ? input.model.trim() : '';
      if (!model) {
        const modelsURL = new URL(url); modelsURL.pathname = modelsURL.pathname.replace(/\/chat\/completions$/, '/models');
        const catalog = await fetch(modelsURL, { headers, signal: controller.signal, redirect: 'error' });
        if (!catalog.ok) return fail(catalog.status, '无法获取模型列表，请填写模型名称或检查 URL 和 API Key。');
        const parts = []; let bytes = 0;
        for await (const chunk of catalog.body) { bytes += chunk.byteLength; if (bytes > 65536) return fail(502, '模型列表过大，请手动填写模型名称。'); parts.push(Buffer.from(chunk)); }
        const ids = JSON.parse(Buffer.concat(parts).toString('utf8')).data?.map(item => item.id).filter(id => typeof id === 'string' && !/embedding|tts|whisper|image|dall-e|moderation|transcrib|realtime|audio/i.test(id)) || [];
        model = ids.find(id => id === 'gpt-4o-mini') || ids.find(id => /chat/i.test(id)) || ids[0];
        if (!model) return fail(502, '模型服务未列出聊天模型，请手动填写模型名称。');
      }
      if (model.length > 256 || /[\r\n]/.test(model)) return fail(400, '模型名称格式无效。');
      const response = await fetch(url, { method: 'POST', headers, body: JSON.stringify({ model, messages: input.messages, stream: true }), signal: controller.signal, redirect: 'error' });
      if (!response.ok) return fail(response.status, '模型请求失败，请检查 URL、API Key、模型名称或服务额度。');
      if (!response.headers.get('content-type')?.includes('text/event-stream') || !response.body) return fail(502, '模型服务没有返回 SSE 流，请确认接口支持 stream。');
      res.writeHead(200, { 'Content-Type': 'text/event-stream; charset=utf-8', 'X-WK-Demo-Model': encodeURIComponent(model), 'X-Accel-Buffering': 'no' });
      // Honor response backpressure rather than accumulating tokens in memory.
      let bytes = 0;
      for await (const chunk of response.body) {
        bytes += chunk.byteLength;
        if (bytes > 4 * 1024 * 1024) { fail(502, '模型流超出 4 MiB。'); return; }
        if (!res.write(chunk)) await once(res, 'drain', { signal: controller.signal });
      }
      res.end();
    } catch { fail(controller.signal.aborted ? 504 : 502, controller.signal.aborted ? '模型请求已中止或超时。' : '模型连接失败，请检查 URL 或服务端连接。'); }
    finally { clearTimeout(timer); clearTimeout(bodyTimer); res.off('close', closed); controller.abort(); active.delete(controller); }
  };
  handler.abortAll = () => { for (const controller of active) controller.abort(); };
  return handler;
}
