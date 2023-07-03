import axios, { AxiosResponse } from "axios";
import { Channel, Message, SyncOptions, WKSDK } from "wukongimjssdk/lib/sdk";
import { Convert } from "./convert";


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