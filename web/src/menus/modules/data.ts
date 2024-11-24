const data: Menu.MenuOptions = {
  name: 'data',
  path: '/data',
  meta: {
    icon: 'i-wk-data-one',
    isAffix: true,
    isFull: false,
    isHide: false,
    isKeepAlive: true,
    isLink: '',
    title: '数据'
  },
  children: [
    {
      name: 'data_connection',
      path: '/data/connection',
      meta: {
        icon: 'i-wk-api',
        isAffix: false,
        isFull: false,
        isHide: false,
        isKeepAlive: true,
        isLink: '',
        title: '链接'
      }
    },
    {
      name: 'data_user',
      path: '/data/user',
      meta: {
        icon: 'i-wk-user',
        isAffix: false,
        isFull: false,
        isHide: false,
        isKeepAlive: true,
        isLink: '',
        title: '用户'
      }
    },
    {
      name: 'data_device',
      path: '/data/device',
      meta: {
        icon: 'i-wk-devices',
        isAffix: false,
        isFull: false,
        isHide: false,
        isKeepAlive: true,
        isLink: '',
        title: '设备'
      }
    },
    {
      name: 'data_message',
      path: '/data/message',
      meta: {
        icon: 'i-wk-message',
        isAffix: false,
        isFull: false,
        isHide: false,
        isKeepAlive: true,
        isLink: '',
        title: '消息'
      }
    },
    {
      name: 'data_channel',
      path: '/data/channel',
      meta: {
        icon: 'i-wk-broadcast',
        isAffix: false,
        isFull: false,
        isHide: false,
        isKeepAlive: true,
        isLink: '',
        title: '频道'
      }
    },
    {
      name: 'data_conversation',
      path: '/data/conversation',
      meta: {
        icon: 'i-wk-communication',
        isAffix: false,
        isFull: false,
        isHide: false,
        isKeepAlive: true,
        isLink: '',
        title: '会话'
      }
    }
  ]
};
export default data;
