import Image from 'next/image';
import type { BaseLayoutProps } from 'fumadocs-ui/layouts/shared';
import { uiTranslations } from 'fumadocs-ui/i18n';
import { domains, type Locale } from './navigation';
import { i18n } from './i18n';
import { appName, gitConfig } from './shared';

export const translations = i18n
  .translations()
  .extend(uiTranslations())
  .add({
    zh: {
      displayName: '中文',
      'Choose a language(language switcher)': '选择语言',
      'Choose a language(language switcher)(aria-label)': '选择语言',
      'Close Search(search dialog)(aria-label)': '关闭搜索',
      'Close Sidebar(aria-label)': '关闭侧栏',
      'Close Sidebar(sidebar)(aria-label)': '关闭侧栏',
      'Collapse Sidebar(sidebar)(aria-label)': '收起侧栏',
      'Copy Markdown(page actions)': '复制 Markdown',
      'Dark(theme switcher)(aria-label)': '深色',
      'Hide Sidebar(sidebar)': '隐藏侧栏',
      'Light(theme switcher)(aria-label)': '浅色',
      'Next Page(pagination)': '下一页',
      'No Headings(table of contents)': '本页没有标题',
      'No results found(search dialog)': '没有找到结果',
      'On this page(table of contents)': '本页内容',
      'Open Search(search trigger)(aria-label)': '打开搜索',
      'Open Sidebar(aria-label)': '打开侧栏',
      'Open Sidebar(sidebar)(aria-label)': '打开侧栏',
      'Previous Page(pagination)': '上一页',
      'Search(search dialog)': '搜索文档',
      'Search(search trigger)': '搜索',
      'Show Sidebar(sidebar)': '显示侧栏',
      'System(theme switcher)(aria-label)': '跟随系统',
      'Toggle Menu(home layout header)(aria-label)': '切换菜单',
      'Toggle Theme(theme switcher)(aria-label)': '切换主题',
      'View as Markdown(page actions)': '查看 Markdown',
    },
    en: {
      displayName: 'English',
    },
  });

function BrandTitle() {
  return (
    <span className="flex items-center gap-2.5">
      <Image src="/logo.png" alt="" width={28} height={28} className="rounded-lg" />
      <span className="font-semibold tracking-tight">{appName}</span>
      <span className="hidden rounded-full border border-orange-200 bg-orange-50 px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider text-orange-700 sm:inline-flex dark:border-orange-900 dark:bg-orange-950 dark:text-orange-300">
        v3 Beta
      </span>
    </span>
  );
}

export function baseOptions(locale: Locale): BaseLayoutProps {
  return {
    nav: {
      title: <BrandTitle />,
      url: `/${locale}`,
      transparentMode: 'top',
    },
    links: [
      ...domains.map((domain) => ({
        text: domain.label[locale],
        url: `/${locale}/${domain.key}`,
        active: 'nested-url' as const,
      })),
      {
        type: 'menu',
        text: locale === 'zh' ? '资源' : 'Resources',
        items: [
          {
            text: 'GitHub',
            url: `https://github.com/${gitConfig.user}/${gitConfig.repo}`,
            external: true,
          },
          {
            text: locale === 'zh' ? '官网' : 'Website',
            url: 'https://githubim.com',
            external: true,
          },
          {
            text: locale === 'zh' ? '聊天演示' : 'Chat Demo',
            url: 'https://imdemo.githubim.com',
            external: true,
          },
          {
            text: locale === 'zh' ? 'Manager 演示' : 'Manager Demo',
            url: 'https://monitor.githubim.com/web/',
            external: true,
          },
          {
            text: 'Releases',
            url: 'https://github.com/WuKongIM/WuKongIM/releases',
            external: true,
          },
          {
            text: locale === 'zh' ? 'v2 旧版文档' : 'v2 Legacy Docs',
            url: 'https://oldv2.githubim.com',
            external: true,
          },
        ],
      },
    ],
  };
}
