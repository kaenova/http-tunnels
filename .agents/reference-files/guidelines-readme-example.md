# Dashboard Implementation Patterns

This directory contains reusable implementation patterns and conventions for the `apps/dashboard` Next.js application.

**Read this file first**, then open only the guidelines relevant to the task.

---

## Index

| Pattern | File | Description |
|---------|------|-------------|
| Creating Tables | [`creating_table.md`](./creating_table.md) | Searchable and paginated table patterns, empty states, status badges, and dropdown-based row actions built from shared table primitives. |
| Toast & Modal UX | [`toast_modal.md`](./toast_modal.md) | `sonner` toast feedback plus controlled `Dialog` and `AlertDialog` patterns for forms, confirmations, scaffolded mutations, and `ActionResult`-driven server-action UX. |
| New Page Scaffolding | [`new_page_scaffolding.md`](./new_page_scaffolding.md) | Protected page shell structure, page-level authorization, metadata rules, breadcrumbs, headers, content sections, and sidebar navigation updates. |
| Data Fetching & Mutations | [`data_fetching_and_mutation.md`](./data_fetching_and_mutation.md) | Server Component → guarded `actions.ts` → Service Layer patterns for protected fetching, UI mapping, `ActionResult` mutation responses, revalidation, and RBAC-aware data flow. |
| RBAC Governance | [`rbac.md`](./rbac.md) | RBAC architecture, permission families, `lib/rbac.ts` usage, `can()` vs `requirePermission()` patterns, and extension rules for governance/resource surfaces. |
| Auditing | [`auditing.md`](./auditing.md) | Centralized audit-log schema, write helpers, approval workflow logging, audit pages, snapshot conventions, and export/detail patterns. |
| Testing | [`testing.md`](./testing.md) | Vitest patterns, test placement, node vs jsdom environments, mocking strategy, redirect tests, and current dashboard test conventions. |
| Environment Variables | [`env.md`](./env.md) | Server-only env handling, browser-safe status exposure, local defaults, feature flags, and cross-service wiring. |
| Page Metadata Templating | [`metadata_template.md`](./metadata_template.md) | `rootMetadata`, `createPageMetadata()`, `generateMetadata()`, canonical path handling, and shared social metadata output. |
| Authentication | [`authentication.md`](./authentication.md) | NextAuth v5 providers, JWT session enrichment, bootstrap-aware route protection, login/logout flows, and auth status exposure. |
| UI Baseline and Styling | [`ui_styling.md`](./ui_styling.md) | Shared UI selection order, shadcn/ui baseline, HugeIcons rules, Base UI boundaries, and Tailwind token-driven styling. |
| Branding | [`branding.md`](./branding.md) | Branding source of truth, app-name and metadata updates, favicon/icon assets, and shadcn preset touchpoints for brand-led styling changes. |
| Folder Scopes and Functionality | [`folder_scopes_and_functionality.md`](./folder_scopes_and_functionality.md) | Code placement guideline for routes, components, shared primitives, helpers, services, tests, and feature/domain boundaries. |

---

## What to Read Next

Read this `README.md` first. After that, do **not** open every guideline by default — follow the workflow below and only pull in the guidelines that match the work.

### Default application development workflow

Most dashboard work should follow this reading order:

1. **Placement and ownership** → `folder_scopes_and_functionality.md`
   - Read this when creating, moving, or splitting routes, components, services, helpers, tests, or types.
   - This is the default first read for most non-trivial tasks.

2. **Route and page shape** → `new_page_scaffolding.md`
   - Read this when adding or changing a page, detail page, breadcrumb, page header, content section, action bar, sidebar item, or page-level authorization.
   - This is the main page-shell + page-authorization guideline for protected dashboard routes.

3. **Data flow and mutations** → `data_fetching_and_mutation.md`
   - Read this when a page fetches protected data, introduces `actions.ts`, calls the service layer, uses `notFound()`, performs mutations, or needs `revalidatePath()` / `router.refresh()`.
   - This is the main server-component → guarded-server-action → service workflow guideline.

4. **UI baseline** → `ui_styling.md`
   - Read this before building or refactoring UI so the implementation starts from shared primitives, HugeIcons, and token-based styling.

5. **Task-specific guidelines** → open only what the feature needs
   - `creating_table.md` for tables, client-side search/filter, pagination, and row action menus.
   - `toast_modal.md` for `Dialog`, `AlertDialog`, and `sonner` feedback patterns.
   - `metadata_template.md` for `metadata`, `generateMetadata()`, canonical paths, and social metadata.
   - `branding.md` for app naming, favicon/icon assets, and brand-led styling changes.
   - `authentication.md` for login/logout, provider gating, bootstrap, protected layouts, and session-aware flows.
   - `env.md` for server-only config, browser-safe `/api/status` exposure, feature flags, and cross-service wiring.
   - `rbac.md` for permission families, `requirePermission()` patterns, schema-backed RBAC scope, and future governance/resource extensions.
   - `auditing.md` for centralized audit-log schema, snapshot conventions, approval workflow logging, and audit page/export patterns.
   - `testing.md` for Vitest conventions, mocking patterns, redirect tests, and where new tests should live.

### Quick reading paths by task

#### New protected page

1. `folder_scopes_and_functionality.md`
2. `new_page_scaffolding.md`
3. `data_fetching_and_mutation.md` if the page reads or writes protected data
4. `rbac.md` if the page is not for all authenticated users
5. `ui_styling.md`
6. `metadata_template.md`
7. Add `creating_table.md` / `toast_modal.md` only if the page needs them

#### New list page or table page

1. `folder_scopes_and_functionality.md`
2. `new_page_scaffolding.md`
3. `data_fetching_and_mutation.md`
4. `ui_styling.md`
5. `creating_table.md`
6. `toast_modal.md` if row actions open dialogs or confirmations
7. `metadata_template.md`

#### New detail page

1. `folder_scopes_and_functionality.md`
2. `new_page_scaffolding.md`
3. `data_fetching_and_mutation.md`
4. `ui_styling.md`
5. `metadata_template.md`
6. `toast_modal.md` if the detail page includes edit/delete/reset flows
7. `rbac.md` if the page shows governance assignments or group-derived access

#### Mutation-heavy feature or form flow

1. `folder_scopes_and_functionality.md` if you are adding a new route action, service method, or component split
2. `data_fetching_and_mutation.md`
3. `toast_modal.md`
4. `ui_styling.md`
5. `creating_table.md` if the mutation starts from a row action menu
6. `env.md` if the mutation depends on runtime config or another service

#### Authentication, bootstrap, or protected routing work

1. `authentication.md`
2. `env.md`
3. `metadata_template.md` if login/initialize/logout routes or redirects are involved
4. `folder_scopes_and_functionality.md` if new auth-related files, routes, or helpers are being added
5. `new_page_scaffolding.md` if a page surface is being redesigned

#### Runtime config, browser-safe status, or cross-service integration

1. `env.md`
2. `data_fetching_and_mutation.md` if config affects server actions or service calls
3. `authentication.md` if login capability, provider gating, or session-driven UI changes
4. `metadata_template.md` if `NEXTAUTH_URL` or route metadata output is affected
5. `folder_scopes_and_functionality.md` if new helpers or endpoints are introduced

#### Governance or RBAC work

1. `rbac.md`
2. `data_fetching_and_mutation.md`
3. `auditing.md` for centralized audit rows, approval workflow logging, and audit-log pages
4. `new_page_scaffolding.md` if the work changes a page, detail page, or page-level authorization
5. `testing.md` when adding or updating authorization tests
6. `creating_table.md` for assignment lists and searchable RBAC sections
7. `toast_modal.md` for add/remove/confirm flows
8. `folder_scopes_and_functionality.md` if new service methods, guarded `actions.ts`, or domain components are added

#### Testing work

1. `testing.md`
2. `folder_scopes_and_functionality.md`
3. Add `rbac.md` when testing authorization behavior
4. Add `authentication.md` when testing login/logout/bootstrap behavior
5. Add `new_page_scaffolding.md` or `data_fetching_and_mutation.md` when the test is tied to page/data-flow behavior

#### Pure UI refactor or component work

1. `ui_styling.md`
2. `folder_scopes_and_functionality.md` if you are extracting or relocating components
3. `creating_table.md` if the UI is table-based
4. `toast_modal.md` if the UI uses overlays or toast feedback
5. `new_page_scaffolding.md` if the refactor changes page-level structure
6. `testing.md` if component behavior tests are added or updated

### Rule of thumb

Read guidelines in this order whenever possible:

**where the code goes** → **how the route/page is shaped** → **how data flows** → **how the UI is built** → **domain-specific rules**

For most feature work, the minimum useful set is:

`folder_scopes_and_functionality.md` → `new_page_scaffolding.md` → `data_fetching_and_mutation.md` → one or two task-specific guidelines.

---

## Notes

- These files document the **current implemented patterns**, not just intended design.
- Prefer linking higher-level docs to this index instead of duplicating large guidance blocks.
- If a pattern does not exist yet, implement the feature and consider adding a new guideline so future agents can follow the same conventions.
