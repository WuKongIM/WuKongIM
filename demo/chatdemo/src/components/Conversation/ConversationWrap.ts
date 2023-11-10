import { Conversation, Message } from "wukongimjssdk"

export class ConversationWrap {
    conversation: Conversation
    constructor(conversation: Conversation) {
        this.conversation = conversation
    }

    avatarHashTag?: string
    // channel: Channel;
    // private _channelInfo;
    // unread: number;
    // timestamp: number;
    // lastMessage: Message;
    // isMentionMe: boolean;
    // constructor();
    // get channelInfo(): ChannelInfo;
    // isEqual(c: Conversation): boolean;


    public get channel() {
        return this.conversation.channel
    }

    public get channelInfo() {
        return this.conversation.channelInfo
    }
    public get unread() {
        return this.conversation.unread
    }
  
    public get timestamp() {
        return this.conversation.timestamp
    }
    public set timestamp(timestamp:number) {
        this.conversation.timestamp = timestamp
    }

    public get timestampString() {
        return getTimeStringAutoShort2(this.timestamp*1000,true)
    }

    public get lastMessage() {
        return this.conversation.lastMessage
    }
    public set lastMessage(lastMessage: Message | undefined) {
        this.conversation.lastMessage = lastMessage
        
    }

    public get isMentionMe()  {
        return this.conversation.isMentionMe
    }

    public get remoteExtra() {
        return this.conversation.remoteExtra
    }

    public set isMentionMe(isMentionMe:boolean | undefined) {
        this.conversation.isMentionMe = isMentionMe
    }

    public get reminders() {
        return this.conversation.reminders
    }

    public get simpleReminders() {
        return this.conversation.simpleReminders
    }

    reloadIsMentionMe(): void {
        return this.conversation.reloadIsMentionMe()
    }

    public get extra() {
        if(!this.conversation.extra) {
            this.conversation.extra = {}
        }
        return this.conversation.extra
    }
   


    isEqual(c: ConversationWrap): boolean {
        return this.conversation.isEqual(c.conversation)
    }
}

function getTimeStringAutoShort2(timestamp:number, mustIncludeTime:boolean) {

    // 当前时间
    var currentDate = new Date();
    // 目标判断时间
    var srcDate = new Date(timestamp);

    var currentYear = currentDate.getFullYear();
    var currentMonth = (currentDate.getMonth() + 1);
    var currentDateD = currentDate.getDate();

    var srcYear = srcDate.getFullYear();
    var srcMonth = (srcDate.getMonth() + 1);
    var srcDateD = srcDate.getDate();

    var ret = "";

    // 要额外显示的时间分钟
    var timeExtraStr = (mustIncludeTime ? " " + _formatDate(srcDate, "hh:mm") : "");

    // 当年
    if (currentYear === srcYear) {
        var currentTimestamp = currentDate.getTime();
        var srcTimestamp = timestamp;
        // 相差时间（单位：毫秒）
        var deltaTime = (currentTimestamp - srcTimestamp);

        // 当天（月份和日期一致才是）
        if (currentMonth === srcMonth && currentDateD === srcDateD) {
            // 时间相差60秒以内
            if (deltaTime < 60 * 1000)
                ret = "刚刚";
            // 否则当天其它时间段的，直接显示“时:分”的形式
            else
                ret = _formatDate(srcDate, "hh:mm");
        }
        // 当年 && 当天之外的时间（即昨天及以前的时间）
        else {
            // 昨天（以“现在”的时候为基准-1天）
            var yesterdayDate = new Date();
            yesterdayDate.setDate(yesterdayDate.getDate() - 1);

            // 前天（以“现在”的时候为基准-2天）
            var beforeYesterdayDate = new Date();
            beforeYesterdayDate.setDate(beforeYesterdayDate.getDate() - 2);

            // 用目标日期的“月”和“天”跟上方计算出来的“昨天”进行比较，是最为准确的（如果用时间戳差值
            // 的形式，是不准确的，比如：现在时刻是2019年02月22日1:00、而srcDate是2019年02月21日23:00，
            // 这两者间只相差2小时，直接用“deltaTime/(3600 * 1000)” > 24小时来判断是否昨天，就完全是扯蛋的逻辑了）
            if (srcMonth === (yesterdayDate.getMonth() + 1) && srcDateD === yesterdayDate.getDate())
                ret = "昨天" + timeExtraStr;// -1d
            // “前天”判断逻辑同上
            else if (srcMonth === (beforeYesterdayDate.getMonth() + 1) && srcDateD === beforeYesterdayDate.getDate())
                ret = "前天" + timeExtraStr;// -2d
            else {
                // 跟当前时间相差的小时数
                var deltaHour = (deltaTime / (3600 * 1000));

                // 如果小于或等 7*24小时就显示星期几
                if (deltaHour <= 7 * 24) {
                    var weekday = new Array(7);
                    weekday[0] = "星期日";
                    weekday[1] = "星期一";
                    weekday[2] = "星期二";
                    weekday[3] = "星期三";
                    weekday[4] = "星期四";
                    weekday[5] = "星期五";
                    weekday[6] = "星期六";

                    // 取出当前是星期几
                    var weedayDesc = weekday[srcDate.getDay()];
                    ret = weedayDesc + timeExtraStr;
                }
                // 否则直接显示完整日期时间
                else
                    ret = _formatDate(srcDate, "yyyy/M/d") + timeExtraStr;
            }
        }
    }
    // 往年
    else {
        ret = _formatDate(srcDate, "yyyy/M/d") + timeExtraStr;
    }

    return ret;
};
function dateFormat(date:Date, fmt:string) {

    return _formatDate(date,fmt)
}

var _formatDate = function (date:Date, fmt:string) {
    var o:any = {
        "M+": date.getMonth() + 1, //月份
        "d+": date.getDate(), //日
        "h+": date.getHours(), //小时
        "m+": date.getMinutes(), //分
        "s+": date.getSeconds(), //秒
        "q+": Math.floor((date.getMonth() + 3) / 3), //季度
        "S": date.getMilliseconds() //毫秒
    };
    if (/(y+)/.test(fmt)) fmt = fmt.replace(RegExp.$1, (date.getFullYear() + "").substr(4 - RegExp.$1.length));
    for (var k in o)
        if (new RegExp("(" + k + ")").test(fmt)) fmt = fmt.replace(RegExp.$1, (RegExp.$1.length === 1) ? (o[k]) : (("00" + o[k]).substr(("" + o[k]).length)));
    return fmt;
};