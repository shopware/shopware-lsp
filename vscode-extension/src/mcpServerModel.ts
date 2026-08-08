export interface McpProcessOptions {
  serverPath: string;
  workspaceRoot: string;
  label: string;
  version: string;
  memoryLimitMiB?: number;
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
  return {
    label: options.label,
    command: options.serverPath,
    args: ['-root', options.workspaceRoot, 'mcp'],
    cwd: options.workspaceRoot,
    env: memoryLimitMiB > 0 ? {GOMEMLIMIT: `${memoryLimitMiB}MiB`} : {},
    version: options.version,
  };
}
