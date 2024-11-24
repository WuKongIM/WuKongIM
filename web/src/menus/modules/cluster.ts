const cluster: Menu.MenuOptions = {
  name: 'cluster',
  path: '/cluster',
  meta: {
    icon: 'i-wk-branch-two',
    isAffix: true,
    isFull: false,
    isHide: false,
    isKeepAlive: true,
    isLink: '',
    title: '分布式'
  },
  children: [
    {
      name: 'cluster_nodes',
      path: '/cluster/nodes',
      meta: {
        icon: 'i-wk-connection-point',
        isAffix: false,
        isFull: false,
        isHide: false,
        isKeepAlive: true,
        isLink: '',
        title: '节点'
      }
    },
    {
      name: 'cluster_slots',
      path: '/cluster/slots',
      meta: {
        icon: 'i-wk-insert-card',
        isAffix: false,
        isFull: false,
        isHide: false,
        isKeepAlive: true,
        isLink: '',
        title: '分区（槽）'
      }
    },
    {
      name: 'cluster_channels',
      path: '/cluster/channels',
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
      name: 'cluster_config',
      path: '/cluster/config',
      meta: {
        icon: 'i-wk-setting-config',
        isAffix: false,
        isFull: false,
        isHide: false,
        isKeepAlive: true,
        isLink: '',
        title: '配置'
      }
    },
    {
      name: 'cluster_log',
      path: '/cluster/log',
      meta: {
        icon: 'i-wk-log',
        isAffix: false,
        isFull: false,
        isHide: true,
        isKeepAlive: true,
        isLink: '',
        title: '日志'
      }
    }
  ]
};
export default cluster;
