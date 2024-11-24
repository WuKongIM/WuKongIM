import {
  VxeButton,
  VxeButtonGroup,
  VxeCheckbox,
  VxeCheckboxGroup,
  VxeForm,
  VxeFormItem,
  VxeIcon,
  VxeInput,
  VxeList,
  VxeLoading,
  VxeModal,
  VxeOptgroup,
  VxeOption,
  VxePager,
  VxePulldown,
  VxeRadio,
  VxeRadioButton,
  VxeRadioGroup,
  VxeSelect,
  VxeSwitch,
  VxeTextarea,
  VxeTooltip,
  VxeUI
} from 'vxe-pc-ui';
import XEUtils from 'xe-utils';
import dayjs from 'dayjs';

import { VxeColgroup, VxeColumn, VxeGrid, VxeTable } from 'vxe-table';
// element-plus 插件
import VxeUIPluginRenderElement from '@vxe-ui/plugin-render-element';
import '@vxe-ui/plugin-render-element/dist/style.css';
// 导入默认的语言
import zhCN from 'vxe-pc-ui/lib/language/zh-CN';

VxeUI.setI18n('zh-CN', zhCN);
VxeUI.setLanguage('zh-CN');
VxeUI.setConfig({
  size: 'small'
});
VxeUI.use(VxeUIPluginRenderElement);

// 式化日期，默认 yyyy-MM-dd HH:mm:ss
VxeUI.formats.add('formatDate', {
  tableCellFormatMethod({ cellValue }, format?: string) {
    if (cellValue.toString().length <= 10) {
      return dayjs.unix(cellValue).format(format || 'YYYY-MM-DD HH:mm:ss');
    } else {
      return dayjs(cellValue).format(format || 'YYYY-MM-DD HH:mm:ss');
    }
  }
});

// 四舍五入金额，每隔3位逗号分隔，默认2位数
VxeUI.formats.add('formatAmount', {
  tableCellFormatMethod({ cellValue }, digits = 2) {
    return XEUtils.commafy(XEUtils.toNumber(cellValue), { digits });
  }
});

// 格式化银行卡，默认每4位空格隔开
VxeUI.formats.add('formatBankcard', {
  tableCellFormatMethod({ cellValue }) {
    return XEUtils.commafy(XEUtils.toValueString(cellValue), { spaceNumber: 4, separator: ' ' });
  }
});

// 向下舍入,默认两位数
VxeUI.formats.add('formatCutNumber', {
  tableCellFormatMethod({ cellValue }, digits = 2) {
    return XEUtils.toFixed(XEUtils.floor(cellValue, digits), digits);
  }
});

// 四舍五入,默认两位数
VxeUI.formats.add('formatFixedNumber', {
  tableCellFormatMethod({ cellValue }, digits = 2) {
    return XEUtils.toFixed(XEUtils.round(cellValue, digits), digits);
  }
});

// 格式化性别
VxeUI.formats.add('formatSex', {
  tableCellFormatMethod({ cellValue }) {
    return cellValue ? (cellValue === '1' ? '男' : '女') : '';
  }
});

// 内存数字格式化
VxeUI.formats.add('formatMemory', {
  tableCellFormatMethod({ cellValue }) {
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let size = cellValue;
    let unitIndex = 0;
    while (size >= 1024 && unitIndex < units.length - 1) {
      size /= 1024;
      unitIndex++;
    }
    return `${size.toFixed(2)} ${units[unitIndex]}`;
  }
});

import type { App } from 'vue';

// 导入主题变量，也可以重写主题变量
import 'vxe-table/styles/all.scss';
import 'vxe-pc-ui/styles/cssvar.scss';

export function setupVxeUI(app: App) {
  app.use(VxeButton);
  app.use(VxeButtonGroup);
  app.use(VxeCheckbox);
  app.use(VxeCheckboxGroup);
  app.use(VxeForm);
  app.use(VxeFormItem);
  app.use(VxeIcon);
  app.use(VxeInput);
  app.use(VxeList);
  app.use(VxeLoading);
  app.use(VxeModal);
  app.use(VxeOptgroup);
  app.use(VxeOption);
  app.use(VxePager);
  app.use(VxePulldown);
  app.use(VxeRadio);
  app.use(VxeRadioButton);
  app.use(VxeRadioGroup);
  app.use(VxeSelect);
  app.use(VxeSwitch);
  app.use(VxeTextarea);
  app.use(VxeTooltip);
}

export function setupVxeTable(app: App) {
  app.use(VxeTable);
  app.use(VxeColumn);
  app.use(VxeColgroup);
  app.use(VxeGrid);
}
