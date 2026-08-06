# Shopware Language Server for VS Code

Supercharge your Shopware development with intelligent code completion, instant navigation, real-time validation, and more — right inside VS Code.

## Highlights

- **Smart Completions** — Context-aware suggestions for services, routes, snippets, config keys, Twig templates, admin components, and more
- **Click-to-Navigate** — Jump to any definition: service classes, route controllers, snippet files, template paths, component sources
- **Inline Diagnostics** — Catch missing snippets, invalid icons, outdated block overrides, and broken component references before runtime
- **8 File Types** — Full support across PHP, Twig, XML, YAML, JSON, JavaScript, TypeScript, and SCSS

## Features

### Symfony Services

Autocomplete service IDs, tags, parameters, and class names in XML and YAML service definitions. Navigate to any service definition with a single click. PHP files show a code lens with service usage counts.

### Twig Templates

Complete template paths in `extends`, `include`, `sw_extends`, and `sw_include` tags. Autocomplete Twig filters and functions. Navigate to any template file from Twig or PHP (`renderStorefront`). See block usage at a glance with code lens indicators.

### Twig Block Versioning

Keep your template overrides in sync. The LSP tracks content hashes of Storefront blocks and warns you when the original block has changed. Quick actions let you view the diff, update the version hash, or scaffold a new block override in your extension.

### Icons

Get autocompletion for icon names and packs in `sw_icon` tags. Hover over an icon to see an inline SVG preview. Missing icons are flagged as errors instantly.

### Snippets (Translations)

Autocomplete translation keys everywhere they're used:

- **Storefront:** `{{ 'key'|trans }}` in Twig, `$this->trans('key')` in PHP
- **Administration:** `{{ $t('key') }}` in Twig, `this.$t('key')` in JS/TS

Hover over any key to see all available translations across locales. Missing keys are flagged as errors with a quick fix to create them — or select text and create a snippet directly from your selection.

### Routes

Autocomplete route names in PHP (`redirectToRoute`) and Twig (`url`, `path`, `seoUrl`). Jump to the controller method that handles any route. Find all references to see everywhere a route is used.

### Feature Flags

Autocomplete and navigate to feature flag definitions from PHP (`Feature::isActive()`), Twig (`feature()`), and SCSS.

### System Configuration

Autocomplete system config keys in PHP when calling `SystemConfigService` methods (`get`, `getInt`, `getString`, `getFloat`, `getBool`, `set`, `getDomain`) and in Twig with the `config()` function. Navigate to the config XML definition.

### Theme Configuration

Autocomplete theme config variables in SCSS (as `$variables`) and in Twig (`theme_config()` function). Navigate to the field definition in `theme.json`.

### Administration Components

Full intelligence for the Shopware Admin built with Vue.js:

- **Tag completion** — Type `<sw-` and get a list of all registered components
- **Prop completion** — See each prop's type, whether it's required, and its default value
- **Slot completion** — Autocomplete available slots in `<template #slot-name>`
- **Event completion** — Autocomplete emitted events with `@event`
- **Hover** — See the full component API (props, events, methods, computed properties, slots)
- **Diagnostics** — Warnings for missing required props, errors for invalid blocks or non-existent parent components
- **Quick fixes** — Add missing props with type-appropriate default values in one click

### Diagnostics Overview

| What's checked | Severity | Where |
|---|---|---|
| Missing snippet keys | Error | Twig, JS/TS |
| Missing icons | Error | Twig |
| Missing required component props | Warning | Twig (admin) |
| Invalid block references | Error | Twig (admin) |
| Non-existent parent component | Error | JS/TS (admin) |
| Outdated block version | Warning | Twig |
| Missing block version comment | Warning | Twig |

## Resource usage

`shopwareLSP.memoryLimitMiB` applies a soft memory limit to the language-server
process. Its default value of `0` uses the server's balanced GC policy. For
large Shopware workspaces, `512` is an opt-in low-memory starting point when
lower peak memory matters more than indexing CPU. Very low limits can cause
frequent garbage collection and substantially increase CPU use. Restart the
language server after changing this setting.
