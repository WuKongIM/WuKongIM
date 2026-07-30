import Image from 'next/image';
import Link from 'next/link';

export default function LanguageChooserPage() {
  return (
    <main className="mx-auto flex min-h-screen max-w-3xl flex-col items-center justify-center px-6 text-center">
      <Image src="/logo.png" alt="WuKongIM" width={80} height={80} className="rounded-3xl shadow-lg" />
      <p className="mt-8 text-sm font-semibold uppercase tracking-[0.24em] text-fd-muted-foreground">
        WuKongIM Docs · v3 Beta
      </p>
      <h1 className="mt-4 text-4xl font-bold tracking-tight sm:text-5xl">Choose your language</h1>
      <p className="mt-4 max-w-xl text-balance text-fd-muted-foreground">
        选择文档语言。Choose the language for the WuKongIM documentation.
      </p>
      <div className="mt-8 flex flex-wrap justify-center gap-3">
        <Link
          href="/zh"
          className="rounded-full bg-fd-primary px-6 py-3 font-semibold text-fd-primary-foreground transition hover:opacity-90"
        >
          中文
        </Link>
        <Link
          href="/en"
          className="rounded-full border border-fd-border bg-fd-card px-6 py-3 font-semibold transition hover:bg-fd-accent"
        >
          English
        </Link>
      </div>
    </main>
  );
}
