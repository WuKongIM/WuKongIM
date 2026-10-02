export type ChatMessage = { role: 'user' | 'assistant'; content: string };
export type ModelConfig = { url: string; apiKey: string; model: string; proxy?: string };

// Accept an OpenAI-compatible base URL or an exact Chat Completions endpoint.
export function completionURL(value: string): string {
  let url: URL;
  try { url = new URL(value); } catch { throw Error('请填写模型 URL。'); }
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.hash) throw Error('模型 URL 必须为 HTTP(S)，且不含用户名、密码或片段。');
  url.pathname = url.pathname.replace(/\/+$/, '');
  if (!url.pathname.endsWith('/chat/completions')) url.pathname = `${url.pathname || '/v1'}/chat/completions`;
  return url.href;
}

// Decode bounded SSE frames across arbitrary UTF-8/network boundaries. An EOF
// without [DONE] or finish_reason is a failure, not a successful partial answer.
export async function* streamCompletion(config: ModelConfig, messages: ChatMessage[], signal: AbortSignal, onModel: (model: string) => void): AsyncGenerator<string> {
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
  const timeout = AbortSignal.timeout(180000);
  const combined = AbortSignal.any([signal, timeout]);
  try {
    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    let model = config.model;
    if (!config.proxy && !model) {
      if (config.apiKey) headers.Authorization = `Bearer ${config.apiKey}`;
      const url = new URL(config.url); url.pathname = url.pathname.replace(/\/chat\/completions$/, '/models');
      const response = await fetch(url, { headers, signal: combined, redirect: 'error' });
      if (!response.ok) throw Error(`无法获取模型列表（HTTP ${response.status}），请填写模型名称。`);
      const catalog = await response.json();
      const ids = catalog.data?.map((item: any) => item.id).filter((id: unknown) => typeof id === 'string' && !/embedding|tts|whisper|image|dall-e|moderation|transcrib|realtime|audio/i.test(id)) || [];
      model = ids.find((id: string) => id === 'gpt-4o-mini') || ids.find((id: string) => /chat/i.test(id)) || ids[0];
      if (!model) throw Error('模型服务未列出聊天模型，请手动填写模型名称。');
    }
    if (!config.proxy && config.apiKey) headers.Authorization = `Bearer ${config.apiKey}`;
    const response = await fetch(config.proxy || config.url, { method: 'POST', headers, body: JSON.stringify(config.proxy ? { url: config.url, api_key: config.apiKey, model, messages } : { model, messages, stream: true }), signal: combined, redirect: 'error' });
    if (!response.ok) throw Error(`模型请求失败（HTTP ${response.status}），请检查 URL、API Key、模型名称或服务额度。`);
    if (!response.body || !response.headers.get('content-type')?.includes('text/event-stream')) throw Error('模型没有返回 SSE 流，请确认接口支持 stream。');
    const selected = response.headers.get('X-WK-Demo-Model');
    if (selected) model = decodeURIComponent(selected);
    if (model) onModel(model);
    reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '', lines: string[] = [], eventSize = 0, textSize = 0, received = false, finished = false;
    while (true) {
      const { value, done } = await reader.read();
      buffer += value ? decoder.decode(value, { stream: true }) : decoder.decode();
      if (buffer.length > 65536) throw Error('模型 SSE 数据超过单帧上限。');
      let match: RegExpExecArray | null;
      while ((match = /\r\n|\n|\r(?!$)/.exec(buffer))) {
        const line = buffer.slice(0, match.index); buffer = buffer.slice(match.index + match[0].length);
        if (line) {
          if (line.startsWith('data:')) { const data = line.slice(5).replace(/^ /, ''); eventSize += data.length; if (eventSize > 65536) throw Error('模型 SSE 数据超过单帧上限。'); lines.push(data); }
          continue;
        }
        const data = lines.join('\n'); lines = []; eventSize = 0;
        if (!data) continue;
        if (data === '[DONE]') { if (!received) throw Error('模型没有返回文本。'); return; }
        let packet: any;
        try { packet = JSON.parse(data); } catch { throw Error('模型返回了无效的 SSE JSON。'); }
        if (packet.error) throw Error('模型返回错误，请检查配置和服务额度。');
        const choice = packet.choices?.find((item: any) => item.index === 0) || packet.choices?.[0];
        if (!choice) continue;
        const content = choice.delta?.content || choice.delta?.refusal;
        if (typeof content === 'string' && content) {
          textSize += content.length;
          if (textSize > 16384) throw Error('回复超过 16,384 字符的演示上限。');
          received = true; yield content;
        }
        if (choice.finish_reason) {
          if (!['stop', 'length'].includes(choice.finish_reason)) throw Error('模型结束了回复，但没有完成文本生成。');
          finished = true;
        }
      }
      if (done) { if (!finished || !received) throw Error('模型流意外中断，回复未完成。'); return; }
    }
  } catch (error) {
    if (signal.aborted) throw error;
    if (timeout.aborted) throw Error('模型请求超时，请重试。');
    if (error instanceof TypeError) throw Error('模型连接失败。若浏览器被跨域限制，请通过 npm start 的本地代理打开 Demo。');
    throw error;
  } finally { await reader?.cancel().catch(() => {}); }
}
