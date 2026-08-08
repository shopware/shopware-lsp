import * as fs from 'fs';
import * as path from 'path';

export interface ServerExecutableOptions {
  configuredPath?: string;
  extensionPath: string;
  workspaceRoot?: string;
  platform?: NodeJS.Platform;
  exists?: (candidate: string) => boolean;
}

export function serverExecutableName(platform: NodeJS.Platform = process.platform): string {
  return platform === 'win32' ? 'shopware-lsp.exe' : 'shopware-lsp';
}

export function resolveServerExecutable(options: ServerExecutableOptions): string | undefined {
  const configuredPath = options.configuredPath?.trim();
  if (configuredPath) {
    return configuredPath;
  }

  const binaryName = serverExecutableName(options.platform);
  const candidates = [
    path.join(options.extensionPath, binaryName),
    path.join(options.extensionPath, '..', binaryName),
  ];
  if (options.workspaceRoot) {
    candidates.push(
      path.join(options.workspaceRoot, '..', binaryName),
      path.join(options.workspaceRoot, binaryName),
    );
  }

  const exists = options.exists ?? fs.existsSync;
  return candidates.find(candidate => exists(candidate));
}
