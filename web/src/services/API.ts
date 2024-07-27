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
    public nodeChannelConfigs(req:{
        nodeId: number
        channelId?: string
        channelType?: number
        running?: boolean
        currentPage?: number
        limit?: number
    }): Promise<any> {
        return APIClient.shared.get(`/cluster/nodes/${req.nodeId}/channels`,{
            param:{
                channel_id:req.channelId,
                channel_type:req.channelType,
                running:req.running?1:0,
                current_page:req.currentPage||1,
                limit:req.limit
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
        nodeId?: number,
        fromUid?: string,
        channelId?: string,
        payload?: string,
        messageId?: number,
        limit?: number,
        offsetMessageId?: number,
        offsetMessageSeq?: number,
        pre?: boolean,
        channelType?: number,
        clientMsgNo?: string
    }): Promise<any> {
        return APIClient.shared.get(`/cluster/messages?node_id=${req.nodeId || 0}&from_uid=${req.fromUid || ''}&channel_id=${req.channelId || ''}&channel_type=${req.channelType || 0}&payload=${req.payload || ''}&message_id=${req.messageId || 0}&limit=${req.limit || 20}&offset_message_id=${req.offsetMessageId || 0}&offset_message_seq=${req.offsetMessageSeq || 0}&pre=${req.pre?1:0}&client_msg_no=${req.clientMsgNo || ''}`)
    }

    // 搜索频道
    public searchChannels(req: {
        channelId?: string,
        channelType?: number,
    }): Promise<any> {
        return APIClient.shared.get(`/cluster/channels`, {
            param: {
                channel_id: req.channelId,
                channel_type: req.channelType,
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
    public users(req:{uid?:string,offsetId:number, limit?: number,pre?:boolean}): Promise<any> {
        return APIClient.shared.get(`/cluster/users?uid=${req.uid || ''}&offset_id=${req.offsetId}&pre=${req.pre?1:0}&limit=${req.limit || 20}`)
    }

    // 获取用户的设备列表
    public devices(req: { uid?: string }): Promise<any> {
        return APIClient.shared.get(`/cluster/devices`, {
            param: {
                uid: req.uid
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
    
}