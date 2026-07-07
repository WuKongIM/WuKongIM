# WuKongIM Web Editorial Console Redesign

## Goal

Redesign the `web` manager UI according to `web/DESIGN.md` while preserving the practical density of a WuKongIM operations console. The result should feel like a restrained enterprise control surface: mostly white, rule-separated, flat, precise, and readable under high-frequency operational use.

The redesign uses the "Editorial Console" direction:

- White canvas as the default surface.
- Thin borders and hairline separators instead of glow, heavy shadow, or broad gradients.
- Near-black and deep green only for strong navigation states, login/product bands, and limited emphasis.
- Large but restrained headings; mono labels for technical category and runtime markers.
- Dense tables, lists, and monitor grids where operators need fast scanning.

## Scope

This is a focused B-scope redesign.

In scope:

- Global visual tokens in `web/src/styles/globals.css`.
- Shell and navigation: `Topbar`, `SidebarNav`, and app layout surfaces.
- Shared UI and shell components used broadly across pages:
  - `Button`
  - `Card`
  - `PageContainer`
  - `PageHeader`
  - `SectionCard`
  - `StatusBadge`
  - common manager controls where needed for visual alignment
- Dedicated layout polish for:
  - `cluster-monitor`
  - `nodes`
  - `plugins`
  - `login`

Out of scope:

- Backend API changes.
- Route, permission, or auth semantics changes.
- New charting, component, or design-system dependencies.
- Full page-by-page redesign for every `web/src/pages` route.
- Marketing-style landing pages or decorative hero sections inside operational pages.

Other pages should benefit from shared component changes, but they should not be individually restructured unless a local change is necessary to keep layout readable.

## Design System

### Color

The light theme becomes the primary design target.

- Background: `#ffffff` or a near-white operational canvas.
- Text: near-black/ink, with muted slate for metadata and descriptions.
- Borders: hairline grays based on the `DESIGN.md` `hairline` and `border-light` palette.
- Primary action: near-black pill button.
- Deep green: reserved for active product/cluster emphasis and a few branded bands.
- Coral/blue: available for limited category/link accents, not broad surface fills.
- Status colors stay semantic and compact: success, warning, destructive, muted.

Remove broad radial glow backgrounds from the app shell and login page. Gradients should not be normal UI fills.

### Typography

Use existing bundled fonts rather than adding proprietary font files.

- Display/UI: Geist as the practical fallback for the `DESIGN.md` Unica-style UI voice.
- Mono: Geist Mono for uppercase technical labels and numeric/runtime markers.
- Headings should use size and spacing more than bold weight.
- Letter spacing should be neutral for normal copy; only technical labels use positive tracking.
- Avoid viewport-scaled type.

### Shape And Depth

- Data cells, controls, cards: 4px-8px radius.
- Major branded/login panels: up to 22px radius.
- Primary buttons and compact filter pills: pill radius.
- No heavy drop shadows in normal page content.
- Use borders, rules, spacing, and surface contrast to create hierarchy.

## Shell Design

### Topbar

The topbar remains a persistent operational header with:

- WuKongIM brand.
- Primary section navigation.
- Current page context where space allows.
- Locale switcher, theme switcher, username, and logout.

Visual changes:

- White background with a thin bottom border.
- Remove glowing logo treatment and stacked rounded containers.
- Use near-black or deep-green active state for the selected top section.
- Keep controls compact and accessible.

### Sidebar

The sidebar stays section-aware and shows the active section's routes.

Visual changes:

- Rule-separated vertical list instead of card-heavy navigation.
- Active route uses a strong but flat state.
- Current section and single-node cluster status remain visible, but with compact labels and list rows.
- Mobile behavior keeps horizontal overflow where needed, but should avoid large card blocks.

### Main Surface

`AppShell` should be a flat white/near-white surface. The main scroll container remains responsible for page scrolling. Page content should use a consistent max width and responsive padding through `PageContainer`.

## Shared Components

### PageHeader

`PageHeader` becomes an open title block:

- Optional mono eyebrow.
- Large title.
- Short description.
- Optional actions aligned to the right on desktop and wrapping below on smaller screens.
- Optional child toolbar area separated by a hairline rule.

It should no longer render as a large rounded shadow card.

### SectionCard

`SectionCard` becomes a restrained content container:

- White background.
- 1px border.
- 8px radius.
- No shadow by default.
- Compact section header with mono or small UI label.

It should support existing pages without forcing nested-card visuals.

### Button

Primary buttons use the `DESIGN.md` primary pill language:

- Near-black background on light surfaces.
- White text.
- Pill radius.
- Compact 14px label.

Secondary and outline variants are lighter and flatter. Destructive actions remain visually distinct but should not dominate rows until confirmation is needed.

### StatusBadge

Status badges remain small, semantic, and scannable:

- Pill or compact rounded shape.
- Thin border.
- Muted fill.
- Capitalized readable text.

They should not create large colored areas inside dense tables.

## Key Pages

### Cluster Monitor

The monitor page becomes a workbench rather than a card wall.

Layout:

- Open `PageHeader`.
- Compact toolbar directly below the header.
- Snapshot strip as a rule-separated key-value row.
- Metric grid with restrained cards and stable chart heights.

Controls:

- Time range, category, node selector, refresh interval, and manual refresh stay in one compact toolbar on desktop.
- Toolbar wraps cleanly on mobile.

States:

- Loading, disabled, and unavailable states use white bordered notices.
- Prometheus error details stay visible without large warning panels.

### Nodes

The node page prioritizes list scanning.

Layout:

- Open `PageHeader`.
- Summary row for node totals, active nodes, controller voters, and runtime unknown counts.
- Main node list/table with identity, health, slot/runtime/controller summaries, and actions.
- Logs tab remains separate from list view.

Behavior:

- Existing lifecycle and controller voter actions remain unchanged.
- Dangerous and lifecycle actions continue to use confirmation dialogs.
- Controller logs are linked or tabbed, not blended into the primary list.

### Plugins

The plugin page separates inventory management from binding lookup.

Layout:

- Open `PageHeader`.
- Inventory section with node selector and filters.
- Compact plugin rows/table showing status, enabled state, methods, version, last error, and actions.
- Binding query section separated from inventory actions.

Behavior:

- Detail, config, restart, uninstall, add binding, and delete binding flows keep current API behavior.
- Dialogs and sheets adopt the white, thin-border, low-shadow visual language.

### Login

The login page may be more branded than the app shell, but still follows `DESIGN.md`.

Layout:

- Left side: WuKongIM identity, large sign-in heading, short operational description, and a few compact proof points.
- Right side: 22px-radius white login panel.
- Optional deep-green or near-black product band for brand contrast.

Controls:

- Inputs use 4px-8px radius and thin borders.
- Submit uses primary pill button.
- Errors use compact red bordered notices.

Avoid background glows and decorative gradients.

## Accessibility And Responsiveness

- Preserve accessible names for navigation, page actions, row actions, dialogs, and form controls.
- Keep touch targets comfortable for topbar, sidebar, filters, and row menus.
- Ensure text does not overflow compact buttons or badges.
- Mobile layouts should stack toolbar groups and key metric rows without horizontal clipping.
- Do not rely on color alone for critical status; keep text labels visible.

## Implementation Boundaries

Implementation should proceed from broad to narrow:

1. Tokens and global CSS.
2. App shell, topbar, sidebar.
3. Shared UI/shell components.
4. Cluster monitor page.
5. Nodes page.
6. Plugins page.
7. Login page.

Avoid unrelated refactors. If a page has structural problems unrelated to the visual redesign, record them in `docs/development/CODE_QUALITY.md` only when they materially matter and continue the current redesign.

## Verification

Run focused frontend checks:

- `cd web && bun run test -- src/app/layout/sidebar-nav.test.tsx src/app/layout/topbar.test.tsx src/components/shell/shell-components.test.tsx`
- `cd web && bun run test -- src/pages/login/page.test.tsx src/pages/cluster-monitor/page.test.tsx src/pages/nodes/page.test.tsx src/pages/plugins/page.test.tsx`
- `cd web && bunx tsc -b`
- `cd web && bun run build`
- `git diff --check`

If the Vite build changes generated assets such as `web/dist/index.html`, restore generated hash churn unless the implementation intentionally changes checked-in build output.

## Risks

- Token changes affect the whole app. Mitigation: keep semantic variables stable and visually conservative.
- Page tests may depend on DOM structure. Mitigation: preserve accessible behavior and update tests only when layout changes require it.
- The console could become too spacious. Mitigation: keep dense rows and compact metric grids for operational surfaces.
- Dark theme may drift. Mitigation: align dark tokens enough to remain usable, but treat light mode as the acceptance target for this redesign.
