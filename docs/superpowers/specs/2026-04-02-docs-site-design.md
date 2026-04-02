# WuKongIM `docs-site` Design

## Goal

Create a new `docs-site/` directory at the repository root that provides a minimal, runnable documentation site for WuKongIM using Fumadocs on top of a Vite-based stack. The first version must run locally and build to a static `dist/` directory, with only one or two placeholder pages.

## Constraints

- The site must live in a new root-level `docs-site/` directory.
- The site must use Fumadocs.
- The site must use Vite rather than Next.js.
- The site must be deployable as an independent site rooted at `/`, not under `/docs`.
- The first version should not migrate existing docs from `docs/` or `WuKongIMDocs/`.
- The first version should prioritize a working skeleton over advanced features.

## Chosen Approach

Build `docs-site/` as an independent React application using:

- `vite`
- `react`
- `react-router`
- `fumadocs-*` packages needed for a React Router integration
- `typescript`

This keeps the new docs site isolated from the existing `web/` Vue app and avoids introducing cross-project coupling before the structure is proven.

## Why This Approach

### Option 1: React Router + Vite + Fumadocs

This is the recommended approach.

- Matches the requirement to use Vite and Fumadocs.
- Keeps the runtime and build model small.
- Produces a static output suitable for `dist/`.
- Fits the stated goal of "first get a basic skeleton running."

### Option 2: TanStack Start + Vite + Fumadocs

Rejected for now.

- Also valid technically.
- Heavier initial scaffold than needed for a first pass.
- Adds complexity before there is a clear requirement for it.

### Option 3: Plain Vite docs site without Fumadocs

Rejected.

- Does not satisfy the requirement to use Fumadocs.

## Directory Structure

The new app should look roughly like this:

```text
docs-site/
  package.json
  tsconfig.json
  vite.config.ts
  index.html
  src/
    main.tsx
    app.tsx
    routes/
    components/
    lib/
  content/
    docs/
      index.mdx
      getting-started.mdx
  public/
```

The exact file names may vary slightly based on the final Fumadocs integration pattern, but the site should stay small and focused.

## Routing

- `/` should render the documentation homepage, not redirect to `/docs`.
- The initial navigation should support the placeholder docs pages directly from the root-oriented site shell.
- The routing model should leave room for adding more documentation pages later without reworking the app foundation.

## Content

The first version should contain only placeholder content:

- a home/introduction page
- a getting started page

The content is only meant to prove:

- MDX or content rendering works
- the Fumadocs layout works
- navigation works
- static build works

Existing repository documentation is intentionally out of scope for this change.

## Scripts

`docs-site/package.json` should provide at least:

- `dev`
- `build`
- `preview`

`build` must emit deployable files into `docs-site/dist/`.

## Non-Goals

The first version should not include:

- search
- internationalization
- versioned docs
- migration of current repository docs
- integration with the existing Go server runtime
- integration with the existing `web/` Vue frontend

## Verification

The implementation will be considered complete when:

1. Dependencies install successfully in `docs-site/`.
2. The local dev server starts successfully.
3. The site renders a basic Fumadocs-based shell.
4. At least two placeholder pages are reachable.
5. The production build succeeds.
6. `docs-site/dist/` is generated.

## Risks

- Fumadocs React Router integration details may differ from the Next.js-focused examples, so the exact package combination may need small adjustments during implementation.
- Static-root routing must be checked carefully so the generated site works when deployed at `/`.
- Because the repo already contains documentation in multiple locations, future migration work should be handled separately to avoid mixing scaffold work with content restructuring.

## Follow-Up Work

After this scaffold lands, likely next steps are:

- align branding with WuKongIM
- migrate selected existing docs into the new site
- add deployment instructions
- add search/versioning only if needed
