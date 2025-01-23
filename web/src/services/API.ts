import { Config } from "../components/Task"
import APIClient from "./APIClient"

export default class API {
    private constructor() {

    }
    public static shared = new API()


    public login(username: string, password: string): Promise<any> {
        return APIClient.shared.post("/manager/login", {
            username: username,
            password: password
        })
    }

    // 获取集群配置
    public clusterConfig(): Promise<any> {
        return APIClient.shared.get("/cluster/info")
    }
    // 获取集群节点信息
    public nodes(): Promise<any> {
        return APIClient.shared.get("/cluster/nodes")
    }
    // 获取简单节点信息
    public simpleNodes(): Promise<any> {
        return APIClient.shared.get("/cluster/simpleNodes")
    }

    // 获取所有槽
    public slots(): Promise<any> {
        return APIClient.shared.get("/cluster/allslot")
    }

    // 迁移槽
    public migrateSlot(req: {
        slot: number,
        migrateFrom: number,
        migrateTo: number
    }) {
        return APIClient.shared.post(`/cluster/slots/${req.slot}/migrate`, {
            migrate_from: req.migrateFrom,
            migrate_to: req.migrateTo
        })
    }

    // 获取节点的频道配置列表
    public nodeChannelConfigs(req: {
        nodeId: number
        channelId?: string
        channelType?: number
        offsetCreatedAt?: number
        limit?: number
        pre ?: boolean
    }): Promise<any> {
        return APIClient.shared.get(`/cluster/nodes/${req.nodeId}/channels`, {
            param: {
                channel_id: req.channelId,
                channel_type: req.channelType,
                limit: req.limit,
                offset_created_at: req.offsetCreatedAt || 0,
                pre: req.pre ? 1 : 0,
            }
        })
    }

    // 获取连接列表
    public connections(nodeId: number, limit: number, sort?: string, uid?: string): Promise<any> {
        return APIClient.shared.get(`/connz?node_id=${nodeId}&limit=${limit || 0}&sort=${sort || ""}&uid=${uid || ""}`)
    }

    // 获取app监控数据
    public apppMetrics(nodeId?: number, latest?: number): Promise<any> {
        return APIClient.shared.get(`/metrics/app?node_id=${nodeId || 0}&latest=${latest || 0}`)
    }

    // 获取分布式监控数据
    public clusterMetrics(latest?: number): Promise<any> {
        return APIClient.shared.get(`/metrics/cluster?latest=${latest || 0}`)
    }

    // 获取系统监控数据
    public systemMetrics(latest?: number): Promise<any> {
        return APIClient.shared.get(`/metrics/system?latest=${latest || 0}`)
    }

    // 搜索消息
    public searchMessages(req: {
        nodeId?: number
        fromUid?: string
        channelId?: string
        payload?: string
        messageId?: number
        limit?: number
        offsetMessageId?: number
        offsetMessageSeq?: number
        pre?: boolean
        channelType?: number
        clientMsgNo?: string
    }): Promise<any> {
        return APIClient.shared.get(`/cluster/messages?node_id=${req.nodeId || 0}&from_uid=${req.fromUid || ''}&channel_id=${req.channelId || ''}&channel_type=${req.channelType || 0}&payload=${req.payload || ''}&message_id=${req.messageId || 0}&limit=${req.limit || 20}&offset_message_id=${req.offsetMessageId || 0}&offset_message_seq=${req.offsetMessageSeq || 0}&pre=${req.pre ? 1 : 0}&client_msg_no=${req.clientMsgNo || ''}`)
    }

    // 搜索频道
    public searchChannels(req: {
        channelId?: string
        channelType?: number
        limit?: number
        offsetCreatedAt?: string
        pre?: boolean
    }): Promise<any> {
        return APIClient.shared.get(`/cluster/channels`, {
            param: {
                channel_id: req.channelId,
                channel_type: req.channelType,
                limit: req.limit,
                offset_created_at: req.offsetCreatedAt || 0,
                pre: req.pre ? 1 : 0
            }
        })
    }

    // 获取频道订阅者
    public subscribers(channelId: string, channelType: number): Promise<any> {
        return APIClient.shared.get(`/cluster/channels/${channelId}/${channelType}/subscribers`)
    }

    // 获取频道黑名单列表
    public denylist(channelId: string, channelType: number): Promise<any> {
        return APIClient.shared.get(`/cluster/channels/${channelId}/${channelType}/denylist`)
    }

    // 获取频道白名单列表
    public allowlist(channelId: string, channelType: number): Promise<any> {
        return APIClient.shared.get(`/cluster/channels/${channelId}/${channelType}/allowlist`)
    }

    // 搜索用户
    public users(req: {
        uid?: string
        limit?: number
        pre?: boolean
        offsetCreatedAt?: number
    }): Promise<any> {
        return APIClient.shared.get(`/cluster/users`,{
            param: {
                offset_created_at: req.offsetCreatedAt,
                uid: req.uid,
                limit: req.limit,
                pre: req.pre ? 1 : 0
            }
        })
    }

    // 获取用户的设备列表
    public devices(req: {
        uid?: string
        limit?: number
        offsetCreatedAt?: number
        pre?: boolean
    }): Promise<any> {
        return APIClient.shared.get(`/cluster/devices`, {
            param: {
                uid: req.uid,
                limit: req.limit,
                offset_created_at: req.offsetCreatedAt || 0,
                pre: req.pre ? 1 : 0

            }
        })
    }

    // 获取用户的会话列表
    public conversations(req: { uid?: string }): Promise<any> {
        return APIClient.shared.get(`/cluster/conversations`, {
            param: {
                uid: req.uid
            }
        })
    }

    // 迁移频道
    public migrateChannel(req: {
        channelId: string,
        channelType: number
        migrateFrom: number
        migrateTo: number
    }): Promise<any> {
        return APIClient.shared.post(`/cluster/channels/${req.channelId}/${req.channelType}/migrate`, {
            migrate_from: req.migrateFrom,
            migrate_to: req.migrateTo,
        })
    }

    // 获取频道分布式配置
    public channelClusterConfig(req: {
        channelId: string,
        channelType: number
        nodeId?: number
    }) {
        return APIClient.shared.get(`/cluster/channels/${req.channelId}/${req.channelType}/config?node_id=${req.nodeId || 0}`)
    }

    // 开启频道
    public channelStart(req: {
        channelId: string,
        channelType: number
        nodeId?: number
    }) {
        console.log(req)
        return APIClient.shared.post(`/cluster/channels/${req.channelId}/${req.channelType}/start`, {
            node_id: req.nodeId
        })
    }

    // 停止频道
    public channelStop(req: {
        channelId: string,
        channelType: number
        nodeId?: number
    }) {
        return APIClient.shared.post(`/cluster/channels/${req.channelId}/${req.channelType}/stop`, {
            node_id: req.nodeId
        })
    }

    // 频道副本列表
    public channelReplicas(req: {
        channelId: string,
        channelType: number
    }) {
        return APIClient.shared.get(`/cluster/channels/${req.channelId}/${req.channelType}/replicas`)
    }

    // 分布式日志
    public clusterLogs(req: {
        nodeId?: number,
        slot?: number,
        pre?: number,
        next?: number,
        logType?: string
    }) {
        return APIClient.shared.get(`/cluster/logs`, {
            param: {
                node_id: req.nodeId,
                pre: req.pre,
                next: req.next,
                slot: req.slot,
                log_type: req.logType
            }
        })
    }

    // 请求消息轨迹日志
    public messageTraces(req: {
        clientMsgNo?: string,
        messageId?: number,
        width?: number, 
        height?: number,
        since?: number,
    }) {
        return APIClient.shared.get(`/cluster/message/trace`, {
            param: {
                client_msg_no: req.clientMsgNo,
                message_id: req.messageId,
                width: req.width,
                height: req.height,
                since: req.since,
            }
        })
    }

    // 请求消息回执轨迹日志
    public messageRecvackTraces(req: {
        nodeId: string,
        messageId: string,
        since?: number,
    }) {
        return APIClient.shared.get(`/cluster/message/trace/recvack`,{
            param: {
                node_id: req.nodeId,
                message_id: req.messageId,
                since: req.since,
            }
        })
    }

    // 获取系统设置
    public async systemSettings(): Promise<SystemSetting> {
        const resp = await APIClient.shared.get(`/varz/setting`)
        return SystemSetting.fromJSON(resp)
    }

    // 添加压测机
    public addTester(req: {
        addr: string,
    }) {
        return APIClient.shared.post(`/stress/add`, req)
    }

    // 移除压测机
    public removeTester(no:string) {

        return APIClient.shared.post(`/stress/remove`,{no:no})
    }

    // 获取压测机列表
    public testers() {
        return APIClient.shared.get(`/stress/infoList`)
    }

    // 开始压测
    public testerStart(no:string,cfg:Config) {

        let req:any = {
            no: no,
            online: cfg.onlineCount,
            channels: new Array<any>(),
        }
        if (cfg.channels) {
            for(let i=0;i<cfg.channels.length;i++) {
                const ch = cfg.channels[i]
                req.channels.push({
                    count: ch.count,
                    type: 2, // 群聊
                    subscriber: {
                        count: ch.subscriberCount,
                        online: ch.subscriberOnlineCount,
                    },
                    msg_rate: ch.msgRate,
                })
            }
        }

        if(cfg.p2pItem) {
            req.p2p = {}
            req.p2p.count = cfg.p2pItem.count
            req.p2p.msg_rate = cfg.p2pItem.msgRate
        }

        return APIClient.shared.post(`/stress/start`,req)
    }

    // 停止压测
    public testerStop(no:string) {
        return APIClient.shared.post(`/stress/stop`,{no:no})
    }

    // 压测报告
    public testerReport(no:string) {
        return APIClient.shared.post(`/stress/report`,{no:no})
    }

    // 测试模版
    public testTemplates() {
        return APIClient.shared.get(`/stress/templates`)
    }

    // 获取节点的标签
    public tags(req:{
        nodeId?:number,
        channelId?:string,
        channelType?:number,
        tagKey?:string,

    }) {
        return APIClient.shared.get(`/tags`,{
            param: {
                node_id: req.nodeId,
                channel_id: req.channelId,
                channel_type: req.channelType,
                tag_key: req.tagKey,
            }
        })

    }
    // 移除节点的标签
    public removeTag(req:{nodeId:number,key:string}) {
        return APIClient.shared.post(`/tag/remove`,{
            node_id: req.nodeId,
            tag_key: req.key
        })
    }

    // 断开
    public disconnect(req:{
        uid:string,
        nodeId:number,
        connId:number,
        opNodeId:number,
    }) {
        return APIClient.shared.post(`/conn/remove`,{
            node_id: req.nodeId,
            uid: req.uid,
            conn_id: req.connId,
            op_node_id: req.opNodeId,
        })
    }

    // 踢出
    public kick(req:{uid:string,nodeId:number,connId:number}) {
        return APIClient.shared.post(`/conn/kick`,{
            node_id: req.nodeId,
            uid: req.uid,
            conn_id: req.connId,
        })
    }

}

export class SystemSetting {
    traceOn: boolean = false // 日志是否开启trace
    lokiOn: boolean = false // 日志是否开启loki
    prometheusOn : boolean = false // 是否开启Prometheus
    StressOn: boolean = false // 是否开启压测

    static fromJSON(json: any): SystemSetting {
        const setting = new SystemSetting()
        setting.traceOn = json.logger.trace_on
        setting.lokiOn = json.logger.loki_on
        setting.prometheusOn = json.prometheus_on
        setting.StressOn = json.stress_on
        return setting
    }

    // 是否开启消息轨迹功能
    get messageTraceOn() {
        return this.traceOn && this.lokiOn
    }
}

