import { domains, parseLocale } from '@/lib/navigation';
import {
  ArrowRight,
  Blocks,
  BookOpenText,
  Braces,
  Code2,
  RadioTower,
  ServerCog,
  ShieldCheck,
  Smartphone,
  Wrench,
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

const copy = {
  zh: {
    eyebrow: 'WuKongIM v3 · 公开文档',
    title: '从第一条消息，走向可靠的大规模通信',
    description:
      '面向应用开发者、服务端部署者和运维人员的统一文档入口。当前第一阶段已完成信息架构，规划内容会清晰标注。',
    quickstart: '开始快速上手',
    browseApi: '浏览 API 结构',
    domainsTitle: '按工作内容进入',
    domainsDescription: '四个文档域共享同一套术语和版本规则，各自保持清晰的阅读路径。',
    rolesTitle: '按你的角色开始',
    planned: '菜单骨架已就绪，正文规划中',
    roles: [
      {
        title: '应用开发者',
        description: '理解消息模型，选择 SDK，并完成身份、连接和消息收发。',
        href: '/zh/guide/quick-start',
        icon: Smartphone,
      },
      {
        title: '服务端部署者',
        description: '选择部署方式，规划集群配置，并完成生产检查。',
        href: '/zh/server/deployment',
        icon: RadioTower,
      },
      {
        title: '运维人员',
        description: '通过监控、备份、扩缩容和诊断工具维护集群。',
        href: '/zh/server/operations',
        icon: Wrench,
      },
    ],
    trustTitle: '为生产环境规划',
    trustDescription: '所有部署都遵循集群语义，文档默认考虑 256 个 Hash Slot 和高规模业务负载。',
    github: '查看 GitHub',
  },
  en: {
    eyebrow: 'WuKongIM v3 · Public Documentation',
    title: 'From the first message to dependable communication at scale',
    description:
      'One documentation home for application developers, server deployers, and operators. The phase-one information architecture is ready, and planned content is clearly marked.',
    quickstart: 'Start the quickstart',
    browseApi: 'Browse the API structure',
    domainsTitle: 'Choose your area',
    domainsDescription:
      'Four documentation domains share one vocabulary and version policy while preserving focused reading paths.',
    rolesTitle: 'Start from your role',
    planned: 'Navigation ready · detailed content planned',
    roles: [
      {
        title: 'Application developer',
        description: 'Learn the message model, choose an SDK, and implement identity, connection, and messaging.',
        href: '/en/guide/quick-start',
        icon: Smartphone,
      },
      {
        title: 'Server deployer',
        description: 'Choose a deployment, plan cluster configuration, and complete production checks.',
        href: '/en/server/deployment',
        icon: RadioTower,
      },
      {
        title: 'Operator',
        description: 'Maintain clusters with monitoring, backups, scaling, and diagnostic tools.',
        href: '/en/server/operations',
        icon: Wrench,
      },
    ],
    trustTitle: 'Planned for production',
    trustDescription:
      'Every deployment follows cluster semantics, with documentation designed around 256 hash slots and high-scale workloads.',
    github: 'View on GitHub',
  },
} as const;

export async function generateMetadata({
  params,
}: {
  params: Promise<{ lang: string }>;
}): Promise<Metadata> {
  const values = await params;
  const locale = parseLocale(values.lang);
  if (!locale) notFound();

  return {
    title: locale === 'zh' ? 'WuKongIM v3 文档' : 'WuKongIM v3 Documentation',
    description: copy[locale].description,
    alternates: {
      canonical: `/${locale}`,
      languages: { zh: '/zh', en: '/en' },
    },
  };
}

export default async function HomePage({ params }: { params: Promise<{ lang: string }> }) {
  const value = (await params).lang;
  const locale = parseLocale(value);
  if (!locale) notFound();
  const content = copy[locale];

  return (
    <main className="overflow-hidden">
      <section className="relative border-b border-fd-border">
        <div className="docs-grid absolute inset-0 opacity-60" aria-hidden="true" />
        <div className="relative mx-auto max-w-6xl px-6 py-20 sm:py-28 lg:px-8 lg:py-32">
          <div className="max-w-4xl">
            <div className="inline-flex items-center gap-2 rounded-full border border-orange-200 bg-orange-50 px-3 py-1 text-xs font-semibold uppercase tracking-[0.16em] text-orange-700 dark:border-orange-900 dark:bg-orange-950/70 dark:text-orange-300">
              <span className="size-1.5 rounded-full bg-orange-500" />
              {content.eyebrow}
            </div>
            <h1 className="mt-7 max-w-4xl text-balance text-5xl font-bold tracking-[-0.04em] sm:text-6xl lg:text-7xl">
              {content.title}
            </h1>
            <p className="mt-6 max-w-2xl text-pretty text-lg leading-8 text-fd-muted-foreground">
              {content.description}
            </p>
            <div className="mt-9 flex flex-wrap gap-3">
              <Link
                href={`/${locale}/guide/quick-start`}
                className="inline-flex items-center gap-2 rounded-full bg-fd-primary px-5 py-3 text-sm font-semibold text-fd-primary-foreground transition hover:-translate-y-0.5 hover:shadow-lg"
              >
                {content.quickstart}
                <ArrowRight className="size-4" />
              </Link>
              <Link
                href={`/${locale}/api`}
                className="inline-flex items-center gap-2 rounded-full border border-fd-border bg-fd-card/80 px-5 py-3 text-sm font-semibold backdrop-blur transition hover:-translate-y-0.5 hover:bg-fd-accent"
              >
                {content.browseApi}
                <Braces className="size-4" />
              </Link>
            </div>
          </div>
        </div>
      </section>

      <section className="mx-auto max-w-6xl px-6 py-18 lg:px-8">
        <div className="max-w-2xl">
          <h2 className="text-3xl font-bold tracking-tight">{content.domainsTitle}</h2>
          <p className="mt-3 text-fd-muted-foreground">{content.domainsDescription}</p>
        </div>
        <div className="mt-8 grid gap-4 md:grid-cols-2">
          {domains.map((domain) => {
            const Icon = domainIcons[domain.key];
            return (
              <Link
                key={domain.key}
                href={`/${locale}/${domain.key}`}
                className="group relative overflow-hidden rounded-2xl border border-fd-border bg-fd-card p-6 transition hover:-translate-y-1 hover:border-orange-300 hover:shadow-xl hover:shadow-orange-950/5 dark:hover:border-orange-800"
              >
                <div className="flex items-start justify-between gap-6">
                  <div className="flex size-11 items-center justify-center rounded-xl bg-orange-50 text-orange-600 dark:bg-orange-950 dark:text-orange-300">
                    <Icon className="size-5" />
                  </div>
                  <ArrowRight className="size-5 text-fd-muted-foreground transition group-hover:translate-x-1 group-hover:text-orange-500" />
                </div>
                <h3 className="mt-8 text-xl font-semibold">{domain.label[locale]}</h3>
                <p className="mt-2 text-sm leading-6 text-fd-muted-foreground">
                  {domain.description[locale]}
                </p>
                <p className="mt-5 text-xs font-medium text-orange-600 dark:text-orange-300">
                  {content.planned}
                </p>
              </Link>
            );
          })}
        </div>
      </section>

      <section className="border-y border-fd-border bg-fd-muted/40">
        <div className="mx-auto max-w-6xl px-6 py-18 lg:px-8">
          <h2 className="text-3xl font-bold tracking-tight">{content.rolesTitle}</h2>
          <div className="mt-8 grid gap-4 lg:grid-cols-3">
            {content.roles.map((role) => {
              const Icon = role.icon;
              return (
                <Link
                  key={role.title}
                  href={role.href}
                  className="rounded-2xl border border-fd-border bg-fd-background p-6 transition hover:border-orange-300 dark:hover:border-orange-800"
                >
                  <Icon className="size-5 text-orange-500" />
                  <h3 className="mt-5 font-semibold">{role.title}</h3>
                  <p className="mt-2 text-sm leading-6 text-fd-muted-foreground">{role.description}</p>
                </Link>
              );
            })}
          </div>
        </div>
      </section>

      <section className="mx-auto grid max-w-6xl gap-6 px-6 py-18 lg:grid-cols-[1fr_auto] lg:items-center lg:px-8">
        <div>
          <div className="flex size-11 items-center justify-center rounded-xl bg-orange-50 text-orange-600 dark:bg-orange-950 dark:text-orange-300">
            <ShieldCheck className="size-5" />
          </div>
          <h2 className="mt-5 text-2xl font-bold tracking-tight">{content.trustTitle}</h2>
          <p className="mt-3 max-w-2xl text-fd-muted-foreground">{content.trustDescription}</p>
        </div>
        <Link
          href="https://github.com/WuKongIM/WuKongIM"
          className="inline-flex w-fit items-center gap-2 rounded-full border border-fd-border px-5 py-3 text-sm font-semibold transition hover:bg-fd-accent"
        >
          <Code2 className="size-4" />
          {content.github}
        </Link>
      </section>
    </main>
  );
}
