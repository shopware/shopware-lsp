/**
 * Document-selector data for the language client, kept free of `vscode`
 * imports so the selection rules stay unit-testable.
 *
 * The selector matches by language ID first, but some file types cannot rely
 * on one: VS Code has no built-in Twig language, and users often reassociate
 * *.twig with HTML through `files.associations`. Matching only
 * `language: 'twig'` would detach the server from those documents entirely —
 * no didOpen, so completion, definition, hover, and diagnostics silently do
 * nothing. Pattern-only filters attach by path regardless of language ID; the
 * server parses from the file extension and ignores the client's language ID.
 */
export const documentSelectorLanguages = [
  'php',
  'xml',
  'yml',
  'yaml',
  'twig',
  'vue',
  'json',
  'scss',
  'javascript',
  'typescript',
  'dotenv',
  'dockerfile',
];

export const documentSelectorFilePatterns = [
  '**/*.twig',
  '**/*.vue',
  '**/.env*',
  '**/*.env',
  '**/Dockerfile*',
];
