import { createHtmlPlugin } from 'vite-plugin-html';
import dayjs from 'dayjs';
import { __APP_INFO__ } from '../utils';

const t = dayjs(__APP_INFO__.PROJECT_BUILD_TIME).format('YYYYMMDDHHmmss');

const getAppConfigSrc = () => {
  return `/tsdd-config.js?v=${__APP_INFO__.pkg.version}-${t}`;
};

export default function createHtml() {
  const tags = [];
  if (process.env.IS_CONFIG) {
    tags.push({
      tag: 'script',
      attrs: {
        src: getAppConfigSrc()
      }
    });
  }
  return createHtmlPlugin({
    inject: {
      data: {
        title: __APP_INFO__.APP_TITLE,
        __APP_INFO__: JSON.stringify(__APP_INFO__)
      },
      tags
    }
  });
}