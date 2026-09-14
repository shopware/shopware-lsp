export interface McpProcessOptions {
  serverPath: string;
  workspaceRoot: string;
  label: string;
  version: string;
  memoryLimitMiB?: number;
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

export function normalizeMemoryLimitMiB(value: number | undefined): number {
  return typeof value === 'number' && Number.isFinite(value)
    ? Math.max(0, Math.floor(value))
    : 0;
}

export function createMcpProcessDefinition(options: McpProcessOptions): McpProcessDefinition {
  const memoryLimitMiB = normalizeMemoryLimitMiB(options.memoryLimitMiB);
  const env: Record<string, string | number> = {};
  if (memoryLimitMiB > 0) env.GOMEMLIMIT = `${memoryLimitMiB}MiB`;
  if (options.editorConfiguration && typeof options.editorConfiguration === 'object' &&
    Object.keys(options.editorConfiguration).length > 0) {
    env.SHOPWARE_LSP_EDITOR_CONFIGURATION = JSON.stringify(options.editorConfiguration);
  }
  const args = ['-root', options.workspaceRoot];
  if (options.allowUnsupportedProject) args.push('-allow-unsupported-project');
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
