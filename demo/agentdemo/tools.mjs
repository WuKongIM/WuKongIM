const schema=(name,description,properties,required)=>({type:'function',function:{name,description,parameters:{type:'object',properties,required,additionalProperties:false}}});
export const toolSchemas=[
  schema('search_knowledge','搜索本 Demo 内置的 WuKongIM 接入资料，返回来源和摘要。',{query:{type:'string',description:'要检索的主题'}},['query']),
  schema('list_todos','读取当前演示中的待办。',{},[]),
  schema('create_todo','创建一条演示待办；必须先取得界面上的用户确认。',{title:{type:'string',description:'简短的待办标题'}},['title']),
];
export const documents=[
  {id:'stream',title:'Agent 流式回复',keywords:['流式','stream','agent','event','增量'],content:'先用 EasySDK 创建 setting.stream=true 的基础消息并等待 SENDACK，再串行写入 /message/event：stream.open → stream.delta → stream.finish。完成、取消和失败都保存完整文本快照。'},
  {id:'sdk',title:'EasySDK 在线消息',keywords:['sdk','消息','在线','接入','wukong'],content:'使用 easyjssdk@2.0.5。WKIMEvent.Message 接收普通消息，CustomEvent 接收实时事件。多个身份使用 singleton:false。工具进度通过普通持久化消息推送，最终回复通过流式事件推送。'},
  {id:'recovery',title:'离线与重连恢复',keywords:['离线','重连','历史','恢复','同步'],content:'连接、重连和加载旧会话时，用 /channel/messagesync 的 event_summary_mode:full 恢复历史及快照。在线期间不轮询历史，也不调用 /message/eventsync。按事件 ID 去重，用 UTF-8 text_offset 合并增量。'},
];
// Validate the complete model-proposed argument object against the allowlist.
export function validateTool(name,args){
  if(!args||Array.isArray(args)||typeof args!=='object')throw Error('工具参数必须为对象。');
  const field=name==='search_knowledge'?'query':name==='create_todo'?'title':name==='list_todos'?null:undefined;
  if(field===undefined)throw Error('此工具不在允许列表中。');
  if(Object.keys(args).some(k=>k!==field))throw Error('工具包含不支持的参数。');
  if(field&&(typeof args[field]!=='string'||!args[field].trim()||args[field].length>(field==='query'?600:160)))throw Error('工具参数为空或超过上限。');
  return field?{[field]:args[field].trim()}:{};
}
export function searchKnowledge(query){
  const normalized=query.toLowerCase();
  return documents.filter(d=>d.keywords.some(k=>normalized.includes(k))).slice(0,3).map(({id,title,content})=>({id,title,content}));
}
