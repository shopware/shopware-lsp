import * as path from 'node:path';

export interface WorkspaceRootCandidate {
  key: string;
  fsPath: string;
  enabled: boolean;
}

function normalizedPath(value: string): string {
  const normalized = path.resolve(value);
  return process.platform === 'win32' ? normalized.toLocaleLowerCase() : normalized;
}

export function pathWithinRoot(root: string, candidate: string): boolean {
  const normalizedRoot = normalizedPath(root);
  const normalizedCandidate = normalizedPath(candidate);
  const relative = path.relative(normalizedRoot, normalizedCandidate);
  return relative === '' || relative !== '..' && !relative.startsWith(`..${path.sep}`) &&
    !path.isAbsolute(relative);
}

export function selectOutermostWorkspaceRoots<T extends WorkspaceRootCandidate>(
  candidates: readonly T[],
): T[] {
  const selected: T[] = [];
  const enabled = candidates.filter(candidate => candidate.enabled).sort((left, right) => {
    const length = normalizedPath(left.fsPath).length - normalizedPath(right.fsPath).length;
    return length || left.key.localeCompare(right.key);
  });
  for (const candidate of enabled) {
    if (selected.some(root => pathWithinRoot(root.fsPath, candidate.fsPath))) continue;
    selected.push(candidate);
  }
  return selected.sort((left, right) => left.key.localeCompare(right.key));
}

export function workspaceRootForPath<T extends WorkspaceRootCandidate>(
  roots: readonly T[],
  candidatePath: string,
): T | undefined {
  return roots
    .filter(root => root.enabled && pathWithinRoot(root.fsPath, candidatePath))
    .sort((left, right) => {
      const length = normalizedPath(right.fsPath).length - normalizedPath(left.fsPath).length;
      return length || left.key.localeCompare(right.key);
    })[0];
}
