# Web Admin Shell Monochrome Simplification Design

- Date: 2026-04-22
- Scope: simplify the existing `web/` admin shell into a light monochrome, table-oriented management UI
- Related files:
  - `web/src/styles/globals.css`
  - `web/src/lib/navigation.ts`
  - `web/src/app/layout/sidebar-nav.tsx`
  - `web/src/app/layout/topbar.tsx`
  - `web/src/components/shell/page-container.tsx`
  - `web/src/components/shell/page-header.tsx`
  - `web/src/components/shell/section-card.tsx`
  - `web/src/components/shell/metric-placeholder.tsx`
  - `web/src/components/shell/placeholder-block.tsx`
  - `web/src/pages/dashboard/page.tsx`
  - `web/src/pages/nodes/page.tsx`
  - `web/src/pages/channels/page.tsx`
  - `web/src/pages/connections/page.tsx`
  - `web/src/pages/slots/page.tsx`
  - `web/src/pages/network/page.tsx`
  - `web/src/pages/topology/page.tsx`
  - `web/src/app/layout/sidebar-nav.test.tsx`
  - `web/src/app/layout/topbar.test.tsx`
  - `web/src/pages/page-shells.test.tsx`

## 1. Background

The current `web/` shell already has the right routing structure and reusable component boundaries, but the visual language is too decorative for the intended admin-console use case. The current shell leans on:

- gradients in the page background and placeholder surfaces
- glassmorphism-style translucency and blur
- blue brand accents and glow-based active states
- hero-like page headers and badge-heavy top chrome
- placeholder copy that sounds like a design showcase instead of an operator tool

The requested direction is different:

- light theme only as the primary experience
- black / white / gray as the dominant visual system
- table-like, form-like management console tone
- quiet navigation and chrome
- denser, more utilitarian content containers

This design keeps the existing app structure and route map, and only redefines the shell language and placeholder grammar.

## 2. Goals and Non-Goals

### 2.1 Goals

This simplification pass must:

1. preserve the existing SPA architecture, routes, and reusable shell boundaries
2. remove gradients, glow, glass effects, and saturated accent-led emphasis
3. establish a light monochrome design token set in `globals.css`
4. convert the shell from a showcase-like dashboard tone into a table-oriented operator console
5. make the left navigation denser by removing item descriptions
6. add monochrome icons to child navigation items to keep scanability without reintroducing visual noise
7. compress the top bar and move page-specific controls into page-level header/tool rows
8. redefine placeholder containers so they read like tables, lists, and status rows instead of polished marketing cards
9. align shell copy with neutral operational language

### 2.2 Non-Goals

This pass does not:

- add real data fetching, forms, filters, or manager API integration
- change routing, page inventory, or navigation grouping
- introduce dark mode as a parallel polished theme
- redesign `ui/` legacy static assets
- add charts, topology engines, or live operational status behavior
- change the broader frontend stack or component library choices

## 3. Design Direction

### 3.1 Overall Tone

The target UI is a quiet operations console, not a brand-heavy dashboard. The desired impression is:

- closer to an inventory tool or runtime console
- closer to tables, filter bars, and structured status panels
- farther from glassmorphism, launch screens, and "hero" sections

Visual emphasis must come from:

- border hierarchy
- spacing rhythm
- font weight
- local background contrast between white and controlled grays

Visual emphasis must not come from:

- gradients
- glow
- blurred cards
- colorful badges
- overly large, decorative KPI blocks

### 3.2 Palette and Surface Rules

The visual system should use neutral tones only:

- page background: white or near-white
- secondary surfaces: very light gray
- text: black / charcoal
- borders: medium neutral gray
- accent: limited to black or dark charcoal emphasis

Blue brand accents currently used for pills, active highlights, and placeholder overlays should be removed from the default shell language.

### 3.3 Shape, Border, and Shadow Rules

- corners should be tighter than the current rounded shell
- borders become the main separation tool
- shadows should be removed or reduced to near-invisible levels
- any remaining emphasis should feel like a document/tool layout, not floating cards

## 4. Shell Structure

### 4.1 Sidebar

`SidebarNav` keeps the current overall role but changes its presentation.

Required changes:

- switch the rail to a pale gray background with a simple right border
- remove decorative brand card treatment from the top section
- keep a concise product header only
- keep navigation group labels
- remove navigation item descriptions entirely
- add one small monochrome icon to each child item
- use border + background change for the active item
- remove glow bars, colored highlights, and floating-card active treatment
- keep the bottom status area simple and textual

The sidebar should feel like a navigation rail in a management product, not like a marketing side panel.

### 4.2 Top Bar

`Topbar` becomes a thin global chrome row.

Required changes:

- keep only route context and global actions
- remove display pills such as `Control plane`, `Manager shell`, and `Precision Console`
- reduce vertical height
- use plain bordered buttons instead of accent-driven controls
- avoid promotional or high-concept copy

The top bar should answer "where am I?" and "what global actions are available?" only.

### 4.3 Page Header

`PageHeader` should stop behaving like a hero card and start behaving like a standard tool-page header block.

Required changes:

- use a flat bordered container
- title + one short description on the left
- page-local actions on the right
- optional secondary tool row under the header for filters or scope controls
- remove eyebrow pills unless a page has a strong operational need for one
- remove decorative top borders and gradient fills

The header should read as a structured tool surface that prepares the table/list area below it.

## 5. Content Grammar

The dashboard and all route shells should converge on three reusable container grammars.

### 5.1 Metric Cell

Current `MetricPlaceholder` surfaces are too decorative. They should become compact metric cells:

- one short uppercase label
- one primary value or placeholder
- optional single neutral note line
- no green "Ready" badge
- no gradient content block
- no diagonal or arrow decoration

These cells should resemble summary rows or compact table-adjacent counters.

### 5.2 Table Container

The default primary content container should look like a table or data pane:

- bordered outer shell
- flat title bar
- header row styling that suggests columns
- neutral placeholder lines aligned like rows

This grammar should drive `Dashboard`, `Nodes`, `Channels`, `Connections`, `Slots`, and parts of `Network`.

### 5.3 List / Status Row Container

Secondary surfaces should read like:

- compact operator lists
- dense status rows
- stacked detail blocks

These should use neutral gray row blocks or bordered items, not illustrative placeholder art.

### 5.4 Placeholder Philosophy

`PlaceholderBlock` should become structural wireframe content rather than decorative ambiance.

Required changes:

- remove blue-tinted gradients and radial backgrounds
- remove faux "ambient" overlays
- align placeholder lines with table, list, and detail rhythms
- keep the placeholder useful for layout comprehension

## 6. Page-Level Adjustments

### 6.1 Dashboard

The landing page should remain an overview entry point, but its language must become more operational:

- keep a summary strip
- keep one primary table-like section
- keep one or two secondary list/status surfaces
- avoid showcase-style names and descriptions
- avoid descriptive cards that read like product demos

The dashboard should feel like an operations workbench.

### 6.2 Other Pages

`Nodes`, `Channels`, `Connections`, `Slots`, `Network`, and `Topology` should all follow the same shell contract:

- simpler page titles, such as `Nodes` instead of `Nodes workspace`
- a short neutral operational description
- right-aligned operator actions
- one optional filter/tool row
- one primary table or primary structured content surface

This keeps the route family consistent and prevents each page from inventing a new visual grammar.

## 7. Copy Changes

Several current copy patterns should be removed because they amplify the "too designed" feeling:

- `workspace`
- `shell`
- `lane`
- `Precision Console`
- `Manager shell`
- other highly stylized placeholder phrases

Replacement guidance:

- use neutral entity names: `Dashboard`, `Nodes`, `Channels`, `Connections`, `Slots`, `Network`, `Topology`
- use concise operational descriptions
- prefer "list", "table", "status", "summary", "detail", "scope", and "filters"
- keep wording compatible with future real runtime data

## 8. File-by-File Implementation Contract

### 8.1 `web/src/styles/globals.css`

- replace the current blue-driven token system with neutral monochrome values
- remove the page background gradients
- remove the fixed grid/mask overlay
- reduce radius values
- minimize shadow-driven styling

### 8.2 `web/src/lib/navigation.ts`

- keep existing route inventory and groups
- retain descriptions only if needed for page metadata
- stop rendering descriptions inside the sidebar itself

### 8.3 `web/src/app/layout/sidebar-nav.tsx`

- remove item descriptions from rendered menu rows
- add monochrome item icons
- simplify active styling
- simplify the top brand block
- simplify the bottom runtime status block

### 8.4 `web/src/app/layout/topbar.tsx`

- remove pills and showcase labels
- keep route title/description context in a quieter form
- simplify button variants used in the header

### 8.5 `web/src/components/shell/page-header.tsx`

- flatten the header container
- remove hero-specific visuals
- support a secondary filter/tool row pattern

### 8.6 `web/src/components/shell/section-card.tsx`

- flatten the title row
- remove decorative gradient and blur language
- make the component feel like a data panel shell

### 8.7 `web/src/components/shell/metric-placeholder.tsx`

- convert to a compact metric cell
- remove status pill, gradient body, and directional arrow

### 8.8 `web/src/components/shell/placeholder-block.tsx`

- replace decorative overlays with table/list wireframe placeholders
- align kinds with structural meaning instead of ambient styling

### 8.9 `web/src/pages/*`

- simplify titles and descriptions
- remove badge-like placeholder subcontent where it is only decorative
- arrange sections according to the approved table/list/status-row grammar

## 9. Testing Impact

The current tests are coupled to the old chrome language and must be updated.

### 9.1 `web/src/app/layout/sidebar-nav.test.tsx`

Update assertions to reflect:

- current route active state still works
- sidebar still renders persistent cluster context
- navigation item descriptions are no longer expected
- new monochrome icon-bearing rows remain accessible by link name

### 9.2 `web/src/app/layout/topbar.test.tsx`

Update assertions to reflect:

- route title/description still appear
- old pills such as `Control plane` and `Manager shell` are removed
- top bar continues to expose the relevant global actions

### 9.3 `web/src/pages/page-shells.test.tsx`

Update expectations for:

- simplified page headings
- renamed section titles where needed
- dashboard sections matching the new table-first copy

## 10. Rollout Notes

- keep the current architecture and component boundaries intact
- prefer shell-level refactors over page-by-page one-off styling forks
- change shared shell components first, then adjust page content to fit the new grammar
- run focused frontend tests after each shell-level update to keep the simplification safe

## 11. Approved Decisions

The following decisions were explicitly approved during brainstorming:

1. use a full light monochrome shell instead of a high-contrast black sidebar layout
2. aim for a form-like / table-like admin temperament rather than a showcase dashboard
3. remove sidebar menu descriptions
4. add monochrome icons to child navigation items
5. keep the shell minimal and operational across sidebar, top bar, page header, and content containers

## 12. Next Step

After the user reviews this spec, the next action is to create an implementation plan for the approved shell simplification. No implementation work should start before that plan exists.
