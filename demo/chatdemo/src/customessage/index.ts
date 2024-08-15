import  { MessageContent } from "wukongimjssdk";


 export const orderMessage = 56; // 自定义消息类型，这里随便定义一个数字，不要和已有的消息类型冲突，1000-2000 之间是预设的系统消息类型，不要使用这个范围内的数字

// 如果是普通消息需要继承 MessageContent 类，如果是包含文件的消息需要继承 MediaMessageContent 类
export class CustomMessage extends MessageContent {

    orderNo!: string; // 订单号
    title!: string; // 标题
    imgUrl!: string; // 图片地址
    num!: number; // 数量
    price!: number; // 价格


    // 重写 encodeJSON
    // 实现消息的编码方法，将消息内容编码成 JSON 格式字符串发送给对方
    encodeJSON() {
        return {
            orderNo: this.orderNo,
            title: this.title,
            imgUrl: this.imgUrl,
            num: this.num,
            price: this.price
        };
    }

     // 重写 decodeJSON
    // 实现消息的解码方法，根据接收到的 JSON 格式字符串解析消息内容
    decodeJSON(data: any) {
        this.orderNo = data.orderNo;
        this.title = data.title;
        this.imgUrl = data.imgUrl;
        this.num = data.num;
        this.price = data.price;
    }

    // 重写 contentType, 指定消息类型
    get contentType() {
        return orderMessage
    }


    // 重写 conversationDigest，这个是现实在最近会话列表的消息摘要
    get conversationDigest() {

        return "[订单消息]"
    }
  
}

