interface IItem {
  value: number;
  label: string;
}

export const CHANNEL_TYPE: IItem[] = [
  { value: 1, label: '个人' },
  { value: 2, label: '群聊' },
  { value: 3, label: '客服' },
  { value: 4, label: '社区' },
  { value: 5, label: '话题' },
  { value: 6, label: '资讯' },
  { value: 7, label: '数据' }
];

export const LATEST_TIME: IItem[] = [
  { value: 300, label: '过去5分钟' },
  { value: 1800, label: '过去30分钟' },
  { value: 3600, label: '过去1小时' },
  { value: 21600, label: '过去6小时' },
  { value: 86400, label: '过去1天' },
  { value: 259200, label: '过去3天' },
  { value: 604800, label: '过去7天' }
];

export const APP_DOC = 'https://githubim.com';
export const APP_ISSUES = 'https://github.com/WuKongIM/WuKongIM/issues';

export const ActionWrite = 'w';
export const ActionRead = 'r';
