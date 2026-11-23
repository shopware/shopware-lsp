/** The extension that validates and completes YAML against a JSON schema. */
export const YAML_EXTENSION_ID = 'redhat.vscode-yaml';

/**
 * Whether `path` is a file the server ships a JSON schema for: the project
 * configuration or the description of the Administration modules.
 */
export function hasShopwareSchema(path: string): boolean {
  const normalized = path.split('\\').join('/');
  return normalized.endsWith('/.config/shopware-lsp.yaml')
    || normalized.endsWith('/Resources/shopware-lsp/admin-modules.yaml');
}
