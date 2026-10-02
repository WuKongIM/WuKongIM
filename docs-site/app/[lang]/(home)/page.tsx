import { domains, parseLocale } from '@/lib/navigation';
import { canonicalUrl, getRobotsMetadata } from '@/lib/shared';
import { FullSearchTrigger } from 'fumadocs-ui/layouts/shared/slots/search-trigger';
import {
  ArrowRight,
  ArrowUpRight,
  Blocks,
  BookOpenText,
  Braces,
  Check,
  Code2,
  GitBranch,
  MessageSquare,
  Radio,
  Server,
  ServerCog,
  Terminal,
} from 'lucide-react';
import type { Metadata } from 'next';
import Link from 'next/link';
import { notFound } from 'next/navigation';

const domainIcons = {
  guide: BookOpenText,
  server: ServerCog,
  sdk: Blocks,
  api: Braces,
};
const domainCategories = {
  guide: { zh: '了解与入门', en: 'LEARN' },
  server: { zh: '部署与运维', en: 'DEPLOY' },
  sdk: { zh: '连接与集成', en: 'INTEGRATE' },
  api: { zh: '接口与协议', en: 'REFERENCE' },
};
const platforms = [
  { label: 'JavaScript / Web', slug: 'javascript' },
  { label: 'Android', slug: 'android' },
  { label: 'iOS', slug: 'ios' },
  { label: 'Flutter', slug: 'flutter' },
  { label: 'HarmonyOS', slug: 'harmonyos' },
];

const copy = {
  zh: {
    eyebrow: 'WuKongIM 开发者文档',
    title: '让每一条消息，',
    titleAccent: '可靠抵达。',
    description:
      '为你的应用构建实时通信。从第一条消息开始，探索客户端接入、集群部署和完整的 API。',
    quickstart: '体验 Demo',
    overview: '接入应用',
    search: '搜索文档',
    searchHint: '查找概念、配置或 API',
    codeComment: '// 连接成功后，向 Bob 发送消息',
    codeCaption: 'SDK 代码片段',
    codeGuide: '查看完整接入流程',
    sender: '发送端',
    cluster: '集群',
    receiver: '接收端',
    flowNote: '分别验证服务器发送结果与接收端消息事件',
    platforms: '选择你的平台',
    exploreEyebrow: 'EXPLORE THE DOCS',
    exploreTitle: '找到你的下一步',
    exploreDescription: '从了解概念到上线运行，每一步都有清晰的路径。',
    exploreLink: '阅读文档',
    startEyebrow: 'YOUR FIRST MESSAGE',
    startTitle: '第一次使用？\n先跑通一条消息。',
    startDescription: '用两个测试用户完成一次消息收发，再逐步接入你的业务。',
    startLink: '打开快速开始',
    steps: [
      {
        title: '启动集群',
        description: '用 Docker 在电脑上启动单节点集群。',
        path: 'server/deployment/docker',
        icon: Terminal,
      },
      {
        title: '体验消息收发',
        description: '打开内嵌 Demo，让 Alice 和 Bob 互发消息。',
        path: 'guide/quick-start/first-message',
        icon: Radio,
      },
      {
        title: '接入你的应用',
        description: '选择平台 SDK，复用已经跑通的收发流程。',
        path: 'sdk',
        icon: MessageSquare,
      },
    ],
    resourcesEyebrow: 'KEEP BUILDING',
    resourcesTitle: '开发时，常用这些',
    resources: [
      {
        title: '核心概念',
        description: '消息、频道、用户与会话',
        path: 'guide/core-concepts',
        icon: BookOpenText,
      },
      {
        title: 'Docker 部署',
        description: '镜像、配置与持久化存储',
        path: 'server/deployment/docker',
        icon: Server,
      },
      {
        title: '体验四个 Demo',
        description: '聊天、流式回复、客服与 Agent',
        path: 'guide/quick-start/chat-demo',
        icon: MessageSquare,
      },
      {
        title: '配置参考',
        description: 'TOML、环境变量与默认值',
        path: 'server/configuration/reference',
        icon: ServerCog,
      },
      {
        title: '健康检查与监控',
        description: '就绪状态、指标与告警',
        path: 'server/operations/health-and-monitoring',
        icon: Radio,
      },
      {
        title: 'WuKongIM HTTP API',
        description: '服务端接口与请求示例',
        path: 'api/product-http',
        icon: Braces,
      },
    ],
    openSource: '开源。可自托管。为你的应用而构建。',
    openSourceDescription: '从单节点集群到多节点部署，沿着同一套集群模型扩展。',
    github: '在 GitHub 上探索',
    footer: 'WuKongIM · 实时通信基础设施',
    footerGuide: '文档',
    footerDemo: '聊天演示',
    footerReleases: '版本发布',
  },
  en: {
    eyebrow: 'WuKongIM Developer Docs',
    title: 'Every message.',
    titleAccent: 'Delivered reliably.',
    description:
      'Build real-time communication into your application. Start with your first message, then explore client SDKs, cluster deployment, and the complete API.',
    quickstart: 'Try the demo',
    overview: 'Integrate your app',
    search: 'Search documentation',
    searchHint: 'Find a concept, setting, or API',
    codeComment: '// Once connected, send a message to Bob',
    codeCaption: 'SDK code excerpt',
    codeGuide: 'Read the full quickstart',
    sender: 'SENDER',
    cluster: 'CLUSTER',
    receiver: 'RECIPIENT',
    flowNote: 'Verify the server send result and recipient event separately',
    platforms: 'CHOOSE YOUR PLATFORM',
    exploreEyebrow: 'EXPLORE THE DOCS',
    exploreTitle: 'Find your next step',
    exploreDescription:
      'A clear path from learning the concepts to running in production.',
    exploreLink: 'Explore docs',
    startEyebrow: 'YOUR FIRST MESSAGE',
    startTitle: 'New here?\nStart with one message.',
    startDescription:
      'Exchange a message between two test users, then bring the same flow into your application.',
    startLink: 'Open the quick start',
    steps: [
      {
        title: 'Start a cluster',
        description: 'Start a single-node cluster on your computer with Docker.',
        path: 'server/deployment/docker',
        icon: Terminal,
      },
      {
        title: 'Try messaging',
        description: 'Open the embedded Demo and exchange messages as Alice and Bob.',
        path: 'guide/quick-start/first-message',
        icon: Radio,
      },
      {
        title: 'Integrate your app',
        description:
          'Choose a platform SDK and bring the working flow into your app.',
        path: 'sdk',
        icon: MessageSquare,
      },
    ],
    resourcesEyebrow: 'KEEP BUILDING',
    resourcesTitle: 'Keep these within reach',
    resources: [
      {
        title: 'Core concepts',
        description: 'Messages, channels, users, and conversations',
        path: 'guide/core-concepts',
        icon: BookOpenText,
      },
      {
        title: 'Docker deployment',
        description: 'Images, configuration, and persistent storage',
        path: 'server/deployment/docker',
        icon: Server,
      },
      {
        title: 'Try the four Demos',
        description: 'Chat, streaming replies, support, and Agent',
        path: 'guide/quick-start/chat-demo',
        icon: MessageSquare,
      },
      {
        title: 'Configuration reference',
        description: 'TOML, environment variables, and defaults',
        path: 'server/configuration/reference',
        icon: ServerCog,
      },
      {
        title: 'Health & monitoring',
        description: 'Readiness, metrics, and alerts',
        path: 'server/operations/health-and-monitoring',
        icon: Radio,
      },
      {
        title: 'WuKongIM HTTP API',
        description: 'Server endpoints and request examples',
        path: 'api/product-http',
        icon: Braces,
      },
    ],
    openSource: 'Open source. Self-hosted. Built for your app.',
    openSourceDescription:
      'Grow from a single-node cluster to multiple nodes with the same cluster model.',
    github: 'Explore on GitHub',
    footer: 'WuKongIM · Real-time communication infrastructure',
    footerGuide: 'Documentation',
    footerDemo: 'Chat demo',
    footerReleases: 'Releases',
  },
} as const;

export async function generateMetadata({
  params,
}: {
  params: Promise<{ lang: string }>;
}): Promise<Metadata> {
  const locale = parseLocale((await params).lang);
  if (!locale) notFound();
  return {
    title: locale === 'zh' ? 'WuKongIM v3 文档' : 'WuKongIM v3 Documentation',
    description: copy[locale].description,
    alternates: {
      canonical: canonicalUrl(`/${locale}`),
      languages: { zh: canonicalUrl('/zh'), en: canonicalUrl('/en') },
    },
    robots: getRobotsMetadata(true),
  };
}

export default async function HomePage({
  params,
}: {
  params: Promise<{ lang: string }>;
}) {
  const locale = parseLocale((await params).lang);
  if (!locale) notFound();
  const content = copy[locale];

  return (
    <div className="docs-home">
      <section
        className="home-hero home-container"
        aria-labelledby="home-title"
      >
        <div className="home-hero-copy">
          <p className="home-eyebrow">
            <span className="home-brand-dot" />
            {content.eyebrow}
            <span className="home-version">v3 Beta</span>
          </p>
          <h1 id="home-title">
            {content.title}
            <span>{content.titleAccent}</span>
          </h1>
          <p className="home-hero-description">{content.description}</p>
          <div className="home-actions">
            <Link
              className="home-button home-button-primary"
              href={`/${locale}/guide/quick-start`}
            >
              {content.quickstart}
              <ArrowRight size={17} aria-hidden="true" />
            </Link>
            <Link
              className="home-button home-button-secondary"
              href={`/${locale}/sdk`}
            >
              <BookOpenText size={17} aria-hidden="true" />
              {content.overview}
            </Link>
          </div>
          <div className="home-search-wrap">
            <FullSearchTrigger
              className="home-search"
              aria-label={content.search}
            />
            <span>{content.searchHint}</span>
          </div>
        </div>

        <figure className="home-preview">
          <div className="home-preview-backdrop" aria-hidden="true" />
          <div className="home-code-window">
            <div className="home-window-header">
              <div className="home-window-dots" aria-hidden="true">
                <i />
                <i />
                <i />
              </div>
              <span>first-message.ts</span>
              <span className="home-language">TypeScript</span>
            </div>
            <div className="home-code-body">
              <div className="home-code-label">
                <Code2 size={14} aria-hidden="true" />
                {content.codeCaption}
              </div>
              <pre>
                <code>
                  <span className="home-syntax-comment">
                    {content.codeComment}
                  </span>
                  {'\n\n'}
                  <span className="home-syntax-purple">await</span>
                  {' sdk.chatManager.'}
                  <span className="home-syntax-blue">send</span>
                  {'(\n  '}
                  <span className="home-syntax-purple">new</span>{' '}
                  <span className="home-syntax-yellow">MessageText</span>
                  {'('}
                  <span className="home-syntax-green">
                    &apos;Hello, WuKongIM!&apos;
                  </span>
                  {'),\n  '}
                  <span className="home-syntax-purple">new</span>{' '}
                  <span className="home-syntax-yellow">Channel</span>
                  {'('}
                  <span className="home-syntax-green">&apos;bob&apos;</span>
                  {', ChannelTypePerson),\n);'}
                </code>
              </pre>
              <Link
                className="home-code-guide"
                href={`/${locale}/sdk/javascript/quickstart`}
              >
                {content.codeGuide}
                <ArrowUpRight size={14} aria-hidden="true" />
              </Link>
            </div>
            <div className="home-message-flow">
              <div className="home-flow-endpoint">
                <span className="home-avatar">A</span>
                <strong>Alice</strong>
                <span>{content.sender}</span>
              </div>
              <div className="home-flow-line" aria-hidden="true">
                <span />
                <ArrowRight size={14} />
              </div>
              <div className="home-flow-cluster">
                <span>
                  <GitBranch size={22} aria-hidden="true" />
                </span>
                <strong>WuKongIM</strong>
                <span>{content.cluster}</span>
              </div>
              <div className="home-flow-line" aria-hidden="true">
                <span />
                <ArrowRight size={14} />
              </div>
              <div className="home-flow-endpoint">
                <span className="home-avatar home-avatar-bob">B</span>
                <strong>Bob</strong>
                <span>{content.receiver}</span>
              </div>
            </div>
          </div>
          <figcaption>
            <Check size={14} aria-hidden="true" />
            {content.flowNote}
          </figcaption>
        </figure>
      </section>

      <div className="home-platforms home-container">
        <span>{content.platforms}</span>
        <div>
          {platforms.map((platform) => (
            <Link
              key={platform.slug}
              href={`/${locale}/sdk/${platform.slug}/quickstart`}
            >
              {platform.label}
              <ArrowUpRight size={12} aria-hidden="true" />
            </Link>
          ))}
        </div>
      </div>

      <section
        className="home-explore home-container"
        aria-labelledby="home-explore-title"
      >
        <div className="home-section-heading">
          <div>
            <p className="home-eyebrow">{content.exploreEyebrow}</p>
            <h2 id="home-explore-title">{content.exploreTitle}</h2>
          </div>
          <p>{content.exploreDescription}</p>
        </div>
        <div className="home-domain-grid">
          {domains.map((domain, index) => {
            const Icon = domainIcons[domain.key];
            return (
              <Link
                className="home-domain-card"
                key={domain.key}
                href={`/${locale}/${domain.key}`}
              >
                <div className="home-domain-top">
                  <span className="home-domain-icon">
                    <Icon size={22} aria-hidden="true" />
                  </span>
                  <span className="home-card-number">0{index + 1}</span>
                </div>
                <p className="home-domain-category">
                  {domainCategories[domain.key][locale]}
                </p>
                <h3>{domain.label[locale]}</h3>
                <p className="home-domain-description">
                  {domain.description[locale]}
                </p>
                <span className="home-card-link">
                  {content.exploreLink}
                  <ArrowRight size={16} aria-hidden="true" />
                </span>
              </Link>
            );
          })}
        </div>
      </section>

      <section
        className="home-start home-container"
        aria-labelledby="home-start-title"
      >
        <div className="home-start-intro">
          <span className="home-start-icon">
            <Terminal size={24} aria-hidden="true" />
          </span>
          <p className="home-eyebrow">{content.startEyebrow}</p>
          <h2 id="home-start-title">{content.startTitle}</h2>
          <p>{content.startDescription}</p>
          <Link
            className="home-text-link"
            href={`/${locale}/guide/quick-start`}
          >
            {content.startLink}
            <ArrowRight size={17} aria-hidden="true" />
          </Link>
        </div>
        <ol className="home-steps">
          {content.steps.map((step, index) => {
            const Icon = step.icon;
            return (
              <li key={step.path}>
                <Link href={`/${locale}/${step.path}`}>
                  <span className="home-step-number">0{index + 1}</span>
                  <div>
                    <h3>{step.title}</h3>
                    <p>{step.description}</p>
                  </div>
                  <Icon
                    className="home-step-icon"
                    size={20}
                    aria-hidden="true"
                  />
                  <ArrowUpRight
                    className="home-step-arrow"
                    size={18}
                    aria-hidden="true"
                  />
                </Link>
              </li>
            );
          })}
        </ol>
      </section>

      <section
        className="home-resources home-container"
        aria-labelledby="home-resources-title"
      >
        <div className="home-section-heading">
          <div>
            <p className="home-eyebrow">{content.resourcesEyebrow}</p>
            <h2 id="home-resources-title">{content.resourcesTitle}</h2>
          </div>
        </div>
        <div className="home-resource-grid">
          {content.resources.map((resource) => {
            const Icon = resource.icon;
            return (
              <Link
                className="home-resource"
                key={resource.path}
                href={`/${locale}/${resource.path}`}
              >
                <Icon size={20} aria-hidden="true" />
                <div>
                  <h3>{resource.title}</h3>
                  <p>{resource.description}</p>
                </div>
                <ArrowUpRight
                  className="home-resource-arrow"
                  size={17}
                  aria-hidden="true"
                />
              </Link>
            );
          })}
        </div>
      </section>

      <section
        className="home-community home-container"
        aria-labelledby="home-community-title"
      >
        <div className="home-community-mark" aria-hidden="true">
          <GitBranch size={26} />
        </div>
        <div>
          <h2 id="home-community-title">{content.openSource}</h2>
          <p>{content.openSourceDescription}</p>
        </div>
        <Link
          className="home-button home-button-secondary"
          href="https://github.com/WuKongIM/WuKongIM"
        >
          {content.github}
          <ArrowUpRight size={16} aria-hidden="true" />
        </Link>
      </section>
      <footer className="home-footer home-container">
        <span>{content.footer}</span>
        <nav aria-label={locale === 'zh' ? '页脚导航' : 'Footer navigation'}>
          <Link href={`/${locale}/guide`}>{content.footerGuide}</Link>
          <a href="https://demo.githubim.com/">{content.footerDemo}</a>
          <a href="https://github.com/WuKongIM/WuKongIM/releases">
            {content.footerReleases}
          </a>
        </nav>
      </footer>
    </div>
  );
}
