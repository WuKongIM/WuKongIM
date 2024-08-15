import axios, { AxiosResponse } from "axios";
import { Channel, ChannelTypePerson, Conversation, Message, SyncOptions, WKSDK } from "wukongimjssdk";
import { Convert } from "./convert";
import { Buffer } from "buffer";


 export class CMDType {
    static CMDTypeClearUnread = "clearUnread" 
 }

export class APIClientConfig {
    private _apiURL: string =""
    private _token:string = ""
    tokenCallback?:()=>string|undefined
    // private _apiURL: string = "/api/v1/" // 正式打包用此地址
    

    set apiURL(apiURL:string) {
        this._apiURL = apiURL;
        axios.defaults.baseURL = apiURL;
    }
    get apiURL():string {
        return this._apiURL
    }
}

export default class APIClient {
    private constructor() {
        this.initAxios()
    }
    public static shared = new APIClient()
    public config = new APIClientConfig()
    public logoutCallback?:()=>void

    initAxios() {
        const self = this
        axios.interceptors.request.use(function (config) {
            let token:string | undefined
            if(self.config.tokenCallback) {
                token = self.config.tokenCallback()
            }
            if (token && token !== "") {
                config.headers!["token"] = token;
            }
            return config;
        });

        axios.interceptors.response.use(function (response) {
            return response;
        }, function (error) {
            var msg = "";
            switch (error.response && error.response.status) {
                case 400:
                    msg = error.response.data.msg;
                    break;
                case 404:
                    msg = "请求地址没有找到（404）"
                    break;
                case 401:
                    if(self.logoutCallback) {
                        self.logoutCallback()
                    }
                default:
                    msg = "未知错误"
                    break;
            }
            return Promise.reject({ error: error, msg: msg, status: error?.response?.status });
        });
    }

     get<T>(path: string, config?: RequestConfig) {
       return this.wrapResult<T>(axios.get(path, {
        params: config?.param
    }), config)
    }
    post(path: string, data?: any, config?: RequestConfig) {
        return this.wrapResult(axios.post(path, data, {}), config)
    }

    put(path: string, data?: any, config?: RequestConfig) {
        return this.wrapResult(axios.put(path, data, {
            params: config?.param,
        }), config)
    }

    delete(path: string, config?: RequestConfig) {
        return this.wrapResult(axios.delete(path, {
            params: config?.param,
            data: config?.data,
        }), config)
    }

    private async wrapResult<T = APIResp>(result: Promise<AxiosResponse>, config?: RequestConfig): Promise<T|any> {
        if (!result) {
            return Promise.reject()
        }
        
        return  result.then((value) => {
          
            if (!config || !config.resp) {
                
                return Promise.resolve(value.data)
            }
            if (value.data) {
                const results = new Array<T>()
                if (value.data instanceof Array) {
                    for (const data of value.data) {
                        var resp = config.resp()
                        resp.fill(data)
                        results.push(resp as unknown as T)
                    }
                    return results
                } else {
                    var sresp = config.resp()
                    sresp.fill(value.data)
                    return Promise.resolve(sresp)
                }
            }
            return Promise.resolve()
        })
    }
    joinChannel = (channelID:string,channelType:number,uid:string) => {
        APIClient.shared.post('/channel/subscriber_add', {
            channel_id: channelID,
            channel_type: channelType,
            subscribers: [uid]
        }).then((res) => {
            console.log(res)
        }).catch((err) => {
            console.log(err)
            alert(err.msg)
        })
    }

    // 同步频道的消息
    // 仅仅做演示，所以直接调用的WuKongIM的接口，实际项目中，建议调用自己的后台接口，
    // 然后后台接口再调用WuKongIM的接口，这样自己的后台可以返回一些自己的业务数据填充到Message.remoteExtra中
    syncMessages = async (channel: Channel,opts: SyncOptions) => {
        let resultMessages = new Array<Message>()
        const limit = 30;
        const resp = await APIClient.shared.post('/channel/messagesync', {
            login_uid: WKSDK.shared().config.uid,
            channel_id: channel.channelID,
            channel_type: channel.channelType,
            start_message_seq: opts.startMessageSeq,
            end_message_seq: opts.endMessageSeq,
            pull_mode: opts.pullMode,
            limit: limit
        })
        const messageList = resp && resp["messages"]
        if (messageList) {
            messageList.forEach((msg: any) => {
                const message = Convert.toMessage(msg);
                resultMessages.push(message);
            });
        }
        return resultMessages
    }

    // 同步会话列表
    // 仅仅做演示，所以直接调用的WuKongIM的接口，实际项目中，建议调用自己的后台接口，
    // 然后后台接口再调用WuKongIM的接口，这样自己的后台可以返回一些自己的业务数据填充到Conversation.extra中
    syncConversations = async () => {
        let resultConversations = new Array<Conversation>()
        const resp = await APIClient.shared.post('/conversation/sync', {
            uid: WKSDK.shared().config.uid,
            msg_count: 1,
        })
        const conversationList = resp
        if (conversationList) {
            conversationList.forEach((v: any) => {
                const conversation = Convert.toConversation(v);
                resultConversations.push(conversation);
            });
        }
        return resultConversations
    }
    clearUnread = async (channel:Channel) => {
       return APIClient.shared.post('/conversations/setUnread', {
            uid: WKSDK.shared().config.uid,
            channel_id: channel.channelID,
            channel_type: channel.channelType,
            unread: 0,
        }).then((res) => {
            // 这里uid指定的是自己，意味着如果是多端登录，其他端也会收到这条消息
            this.sendCMD(new Channel(WKSDK.shared().config.uid!,ChannelTypePerson),CMDType.CMDTypeClearUnread,channel)
        }).catch((err) => {
            console.log(err)
            alert(err.msg)
        })
    }

    // 此处仅做演示
    // 此方法应该在自己的业务后端调用
    sendCMD = async (channel:Channel,cmd:string,param?:any) => {
        // 转换成base64
        const buffer = Buffer.from(JSON.stringify({
            type: 99, // cmd固定type为99
            cmd: cmd,
            param: param,
        }), 'utf-8');

        const base64 = buffer.toString('base64');

      return  APIClient.shared.post('/message/send', {
            "header": {// 消息头
              "no_persist": 1, // 是否不存储消息 0.存储 1.不存储
              "red_dot": 1, // 是否显示红点计数，0.不显示 1.显示
              "sync_once":1 // 是否是写扩散，这里一般是0，只有cmd消息才是1
            },
            "from_uid": "", // 发送者uid
            "channel_id": channel.channelID, // 接收频道ID 如果channel_type=1 channel_id为个人uid 如果channel_type=2 channel_id为群id
            "channel_type": channel.channelType, // 接收频道类型  1.个人频道 2.群聊频道
            "payload": base64, // 消息内容，base64编码
            "subscribers": [] // 订阅者 如果此字段有值，表示消息只发给指定的订阅者,没有值则发给频道内所有订阅者
          })
    }

    messageStreamStart = (param:any) => {
       return APIClient.shared.post('/streammessage/start',param)
    }
    messageStreamEnd = (param:any) => {
        return APIClient.shared.post('/streammessage/end',param)
     }
}

export class RequestConfig {
    param?: any
    data?:any
    resp?: () => APIResp
}

export interface APIResp {

    fill(data: any): void;
}