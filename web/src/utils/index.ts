import * as buffer from 'buffer';
/**
 * @description 获取浏览器默认语言
 * @returns
 */
export function getBrowserLang() {
  const browserLang = navigator.language ? navigator.language : (navigator as any).browserLanguage;
  let defaultBrowserLang = '';
  if (['cn', 'zh', 'zh-cn'].includes(browserLang.toLowerCase())) {
    defaultBrowserLang = 'zh';
  } else {
    defaultBrowserLang = 'en';
  }
  return defaultBrowserLang;
}

/**
 * @description 使用递归扁平化菜单，方便添加动态路由
 * @param {Array} menuList 菜单列表
 * @returns {Array}
 */
export function getFlatMenuList(menuList: Menu.MenuOptions[]): Menu.MenuOptions[] {
  const newMenuList: Menu.MenuOptions[] = JSON.parse(JSON.stringify(menuList));
  return newMenuList.flatMap(item => [item, ...(item.children ? getFlatMenuList(item.children) : [])]);
}

/**
 * @description 使用递归过滤出需要渲染在左侧菜单的列表 (需剔除 isHide == true 的菜单)
 * @param {Array} menuList 菜单列表
 * @returns
 * */
export function getShowMenuList(menuList: Menu.MenuOptions[]) {
  const newMenuList: Menu.MenuOptions[] = JSON.parse(JSON.stringify(menuList));
  return newMenuList.filter(item => {
    item.children?.length && (item.children = getShowMenuList(item.children));
    return !item.meta?.isHide;
  });
}

/**
 * @description 使用递归找出所有面包屑存储到 pinia/vuex 中
 * @param {Array} menuList 菜单列表
 * @param {Array} parent 父级菜单
 * @param {Object} result 处理后的结果
 * @returns {Object}
 */
export const getAllBreadcrumbList = (menuList: Menu.MenuOptions[], parent = [], result: { [key: string]: any } = {}) => {
  for (const item of menuList) {
    result[item.path] = [...parent, item];
    if (item.children) getAllBreadcrumbList(item.children, result[item.path], result);
  }
  return result;
};

/**
 * @description 使用递归处理路由菜单 path，生成一维数组 (第一版本地路由鉴权会用到，该函数暂未使用)
 * @param {Array} menuList 所有菜单列表
 * @param {Array} menuPathArr 菜单地址的一维数组 ['**','**']
 * @returns {Array}
 */
export function getMenuListPath(menuList: Menu.MenuOptions[], menuPathArr: string[] = []): string[] {
  for (const item of menuList) {
    if (typeof item === 'object' && item.path) menuPathArr.push(item.path);
    if (item.children?.length) getMenuListPath(item.children, menuPathArr);
  }
  return menuPathArr;
}

/**
 * @description 递归查询当前 path 所对应的菜单对象 (该函数暂未使用)
 * @param {Array} menuList 菜单列表
 * @param {String} path 当前访问地址
 * @returns {Object | null}
 */
export function findMenuByPath(menuList: Menu.MenuOptions[], path: string): Menu.MenuOptions | null {
  for (const item of menuList) {
    if (item.path === path) return item;
    if (item.children) {
      const res = findMenuByPath(item.children, path);
      if (res) return res;
    }
  }
  return null;
}

/**
 * @description 使用递归过滤需要缓存的菜单 name (该函数暂未使用)
 * @param {Array} menuList 所有菜单列表
 * @param {Array} keepAliveNameArr 缓存的菜单 name ['**','**']
 * @returns {Array}
 * */
export function getKeepAliveRouterName(menuList: Menu.MenuOptions[], keepAliveNameArr: string[] = []) {
  menuList.forEach(item => {
    item.meta.isKeepAlive && item.name && keepAliveNameArr.push(item.name);
    item.children?.length && getKeepAliveRouterName(item.children, keepAliveNameArr);
  });
  return keepAliveNameArr;
}

export const formatNumber = (num: number, fractionDigits: number = 4) => {
  if (num < 1000) {
    return num.toFixed(fractionDigits).toString();
  } else {
    const suffixes = ['', 'K', 'M', 'G', 'T', 'P', 'E', 'Z', 'Y'];
    let quotient = num;
    let suffixIndex = 0;
    while (quotient >= 1000 && suffixIndex < suffixes.length - 1) {
      quotient = Math.floor(quotient / 1000);
      suffixIndex++;
    }
    const suffix = suffixes[suffixIndex];
    const formattedQuotient = quotient.toString();
    if (suffix === '') {
      return formattedQuotient;
    } else {
      const formattedRemainder = Math.floor((num % 1000) / 100).toString();
      return `${formattedQuotient}.${formattedRemainder}${suffix}`;
    }
  }
};
export const formatDate = (date: Date) => {
  const hours = date.getHours();
  const minutes = date.getMinutes();
  const seconds = date.getSeconds();
  return hours + ':' + minutes + ':' + seconds;
};

// 将内容转换为中间省略的形式，如"1234567890" => "1234...7890"，可以指定显示的长度
export const ellipsis = (content: string, length: number = 10): string => {
  if (content.length <= length) {
    return content;
  } else {
    const head = content.slice(0, Math.floor(length / 2));
    const tail = content.slice(content.length - Math.ceil(length / 2));
    return head + '...' + tail;
  }
};

// base64解码, 支持包含中文内容的数据

export const base64Decode = (data: string): string => {
  if (!data || data.length == 0) {
    return '';
  }
  return buffer.Buffer.from(data, 'base64').toString();
};

// base64编码, 支持包含中文内容的数据

export const base64Encode = (data: string): string => {
  console.log(data);
  return buffer.Buffer.from(data).toString('base64');
};

export const setSeries = (field: string, data: any, results: Array<any>) => {
  const result = data[field];
  if (result && result.length > 0) {
    for (let index = 0; index < result.length; index++) {
      const label = result[index];
      let exist: any;
      for (let index = 0; index < results.length; index++) {
        const element = results[index];
        if (element.name == label.label) {
          exist = element;
          break;
        }
      }
      if (exist) {
        exist.data.push({ timestamp: data.timestamp, value: label.value });
      } else {
        results.push({ name: label.label, data: [{ timestamp: data.timestamp, value: label.value }] });
      }
    }
  }
};
