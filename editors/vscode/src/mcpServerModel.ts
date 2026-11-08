export interface McpProcessOptions {
  serverPath: string;
  workspaceRoot: string;
  label: string;
  version: string;
  editorConfiguration?: unknown;
  allowUnsupportedProject?: boolean;
}

export interface McpProcessDefinition {
  label: string;
  command: string;
  args: string[];
  cwd: string;
  env: Record<string, string | number>;
  version: string;
}

export function createMcpProcessDefinition(options: McpProcessOptions): McpProcessDefinition {
  const env: Record<string, string | number> = {};
  if (options.editorConfiguration && typeof options.editorConfiguration === 'object' &&
    Object.keys(options.editorConfiguration).length > 0) {
    env.SHOPWARE_LSP_EDITOR_CONFIGURATION = JSON.stringify(options.editorConfiguration);
  }
  const args = ['--root', options.workspaceRoot];
  if (options.allowUnsupportedProject) args.push('--allow-unsupported-project');
  args.push('mcp');
  return {
    label: options.label,
    command: options.serverPath,
    args,
    cwd: options.workspaceRoot,
    env,
    version: options.version,
  };
}
