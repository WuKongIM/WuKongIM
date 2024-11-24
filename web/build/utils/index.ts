import path from 'path';
import dayjs from 'dayjs';
import { name, version, engines } from '../../package.json';

export const __APP_INFO__ = {
  pkg: { name, version, engines },
  APP_TITLE: '悟空IM分布式管理系统',
  APP_TITLE_SHORT: 'WK',
  APP_DOCS: 'https://githubim.com/',
  PROJECT_BUILD_TIME: dayjs(new Date()).format('YYYY-MM-DD HH:mm:ss')
};
/**
 * 获取项目根路径
 * @descrition 末尾不带斜杠
 */
export function getRootPath() {
  return path.resolve(process.cwd());
}

/**
 * 获取项目src路径
 * @param srcName - src目录名称(默认: "src")
 * @descrition 末尾不带斜杠
 */
export function getSrcPath(srcName = 'src') {
  const rootPath = getRootPath();

  return `${rootPath}/${srcName}`;
}
