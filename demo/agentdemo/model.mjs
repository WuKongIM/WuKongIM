// Chat Completions tool deltas are assembled before any tool can execute.
// Reasoning fields and provider error bodies are never sent to the UI or logs.
export async function* modelTurn(config, messages, tools, signal) {
  const deadline = AbortSignal.any([signal, AbortSignal.timeout(180000)]);
  const headers = {'content-type':'application/json'};
  if (config.apiKey) headers.Authorization = `Bearer ${config.apiKey}`;
  if (!config.model) {
    const catalogURL = new URL(config.url); catalogURL.pathname = catalogURL.pathname.replace(/\/chat\/completions$/, '/models');
    const catalog = await fetch(catalogURL,{headers,signal:deadline,redirect:'error'});
    if (!catalog.ok || !catalog.body) throw Error('Model catalog unavailable');
    let size=0; const chunks=[];
    for await(const chunk of catalog.body){size+=chunk.length;if(size>65536)throw Error('Model catalog too large');chunks.push(Buffer.from(chunk));}
    const ids=JSON.parse(Buffer.concat(chunks)).data?.map(m=>m.id).filter(id=>typeof id==='string'&&id.length<256&&!/[\r\n]/.test(id)&&!/embedding|tts|whisper|image|dall-e|moderation|transcrib|realtime|audio/i.test(id))||[];
    config.model=ids.find(id=>/chat/i.test(id))||ids[0];if(!config.model)throw Error('Missing model');
  }
  const response=await fetch(config.url,{method:'POST',headers,body:JSON.stringify({model:config.model,messages,tools,tool_choice:'auto',stream:true}),signal:deadline,redirect:'error'});
  if(!response.ok||!response.body||!response.headers.get('content-type')?.includes('text/event-stream'))throw Error('Model stream unavailable');
  const reader=response.body.getReader(),decoder=new TextDecoder();
  const calls=new Map();let buffer='',data=[],frameSize=0,total=0,textSize=0,finish='',received=false;
  function result(){
    if(!received||!['stop','tool_calls'].includes(finish))throw Error('Incomplete model turn');
    const values=[...calls.entries()].sort(([a],[b])=>a-b).map(([,call])=>call);
    if(finish==='tool_calls'&&!values.length||finish==='stop'&&values.length)throw Error('Incomplete tool calls');
    const seen=new Set();
    for(const call of values){if(!call.id||!call.function.name||seen.has(call.id))throw Error('Invalid tool identity');seen.add(call.id);JSON.parse(call.function.arguments);}
    return {type:'calls',calls:values};
  }
  try {
    while(true){
      const {value,done}=await reader.read();total+=value?.length||0;if(total>4194304)throw Error('Model stream too large');
      buffer+=value?decoder.decode(value,{stream:true}):decoder.decode();if(buffer.length>65536)throw Error('SSE frame too large');
      let match;
      while((match=/\r\n|\n|\r(?!$)/.exec(buffer))){
        const line=buffer.slice(0,match.index);buffer=buffer.slice(match.index+match[0].length);
        if(line){if(line.startsWith('data:')){const part=line.slice(5).replace(/^ /,'');frameSize+=part.length;if(frameSize>65536)throw Error('SSE frame too large');data.push(part);}continue;}
        const raw=data.join('\n');data=[];frameSize=0;if(!raw)continue;
        if(raw==='[DONE]'){yield result();return;}
        const packet=JSON.parse(raw);if(packet.error)throw Error('Model error');
        const choice=packet.choices?.find(c=>c.index===0);if(!choice)continue;
        const delta=choice.delta||{};
        if(typeof delta.content==='string'&&delta.content){textSize+=delta.content.length;if(textSize>16384)throw Error('Model text too large');received=true;yield {type:'text',text:delta.content};}
        for(const piece of delta.tool_calls||[]){
          if(!Number.isInteger(piece.index)||piece.index<0||piece.index>2||piece.type&&piece.type!=='function')throw Error('Unsupported tool call');
          let call=calls.get(piece.index);if(!call){call={id:'',type:'function',function:{name:'',arguments:''}};calls.set(piece.index,call);}
          if(piece.id){if(typeof piece.id!=='string'||call.id&&call.id!==piece.id)throw Error('Tool identity changed');call.id=piece.id;}
          if(piece.function?.name){if(typeof piece.function.name!=='string')throw Error('Invalid tool name');call.function.name+=piece.function.name;}
          if(piece.function?.arguments!==undefined){if(typeof piece.function.arguments!=='string')throw Error('Invalid tool arguments');call.function.arguments+=piece.function.arguments;}
          if(call.id.length>128||call.function.name.length>64||call.function.arguments.length>4096)throw Error('Tool call too large');received=true;
        }
        if(choice.finish_reason){if(finish&&finish!==choice.finish_reason)throw Error('Model finish changed');finish=choice.finish_reason;}
      }
      if(done){yield result();return;}
    }
  }finally{await reader.cancel().catch(()=>{});}
}
