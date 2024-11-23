const monitor: Menu.MenuOptions = {
  name: 'monitor',
  path: '/monitor',
  meta: {
    icon: 'i-wk-monitor',
    isAffix: true,
    isFull: false,
    isHide: false,
    isKeepAlive: true,
    isLink: '',
    title: '监控'
  },
  children: [
    {
      name: 'monitor_app',
      path: '/monitor/app',
      meta: {
        icon: 'i-wk-application-one',
        isAffix: false,
        isFull: false,
        isHide: false,
        isKeepAlive: true,
        isLink: '',
        title: '应用'
      }
    },
    {
      name: 'monitor_system',
      path: '/monitor/system',
      meta: {
        icon: 'i-wk-system',
        isAffix: false,
        isFull: false,
        isHide: false,
        isKeepAlive: true,
        isLink: '',
        title: '系统'
      }
    },
    {
      name: 'monitor_cluster',
      path: '/monitor/cluster',
      meta: {
        icon: 'i-wk-connection-box',
        isAffix: false,
        isFull: false,
        isHide: false,
        isKeepAlive: true,
        isLink: '',
        title: '分布式'
      }
    },
    {
      name: 'monitor_trace',
      path: '/monitor/trace',
      meta: {
        icon: 'i-wk-trace',
        isAffix: false,
        isFull: false,
        isHide: false,
        isKeepAlive: true,
        isLink: '',
        title: '消息追踪'
      }
    }
  ]
};
export default monitor;
