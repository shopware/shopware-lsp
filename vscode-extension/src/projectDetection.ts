import {execFile} from 'child_process';

export type ProjectKind = 'shopware' | 'symfony' | 'configured' | 'unknown';
export type ActivationMode = 'auto' | 'always' | 'never';

export interface ProjectEvidence {
  path: string;
  reason: string;
}

export interface ProjectInfo {
  supported: boolean;
  kind: ProjectKind;
  evidence: ProjectEvidence[];
}

export interface ActivationDecision {
  enabled: boolean;
  allowUnsupportedProject: boolean;
}

export interface InactiveServerProject {
  active: false;
  reason: 'unsupportedProject';
}

type ProjectInfoRunner = (serverPath: string, workspaceRoot: string) => Promise<string>;

export function normalizeActivationMode(value: unknown): ActivationMode {
  return value === 'always' || value === 'never' ? value : 'auto';
}

export function decideActivation(
  mode: ActivationMode,
  project?: ProjectInfo,
): ActivationDecision {
  if (mode === 'never') return {enabled: false, allowUnsupportedProject: false};
  if (mode === 'always') return {enabled: true, allowUnsupportedProject: true};
  return {enabled: project?.supported === true, allowUnsupportedProject: false};
}

export function inactiveServerProject(experimental: unknown): InactiveServerProject | undefined {
  if (!experimental || typeof experimental !== 'object') return undefined;
  const shopware = (experimental as Record<string, unknown>).shopwareLSP;
  if (!shopware || typeof shopware !== 'object') return undefined;
  const state = shopware as Record<string, unknown>;
  if (state.active !== false || state.reason !== 'unsupportedProject') return undefined;
  return {active: false, reason: 'unsupportedProject'};
}

export function parseProjectInfo(source: string): ProjectInfo {
  const value: unknown = JSON.parse(source);
  if (!value || typeof value !== 'object') throw new Error('project-info returned no object');
  const record = value as Record<string, unknown>;
  const kinds: ProjectKind[] = ['shopware', 'symfony', 'configured', 'unknown'];
  if (typeof record.supported !== 'boolean' || !kinds.includes(record.kind as ProjectKind) ||
    !Array.isArray(record.evidence)) {
    throw new Error('project-info returned an invalid result');
  }
  const evidence = record.evidence.map((item: unknown) => {
    if (!item || typeof item !== 'object') throw new Error('project-info returned invalid evidence');
    const entry = item as Record<string, unknown>;
    if (typeof entry.path !== 'string' || typeof entry.reason !== 'string') {
      throw new Error('project-info returned invalid evidence');
    }
    return {path: entry.path, reason: entry.reason};
  });
  const kind = record.kind as ProjectKind;
  if (record.supported !== (kind !== 'unknown')) {
    throw new Error('project-info returned inconsistent support state');
  }
  return {supported: record.supported, kind, evidence};
}

export class ProjectDetector {
  private readonly cache = new Map<string, Promise<ProjectInfo>>();

  constructor(private readonly runProjectInfo: ProjectInfoRunner = executeProjectInfo) {}

  detect(serverPath: string, workspaceRoot: string): Promise<ProjectInfo> {
    const key = this.cacheKey(serverPath, workspaceRoot);
    let pending = this.cache.get(key);
    if (!pending) {
      const request = this.runProjectInfo(serverPath, workspaceRoot).then(parseProjectInfo);
      pending = request;
      this.cache.set(key, request);
      request.catch(() => {
        if (this.cache.get(key) === request) this.cache.delete(key);
      });
    }
    return pending;
  }

  invalidate(workspaceRoot?: string): void {
    if (!workspaceRoot) {
      this.cache.clear();
      return;
    }
    for (const key of this.cache.keys()) {
      if (key.endsWith(`\0${workspaceRoot}`)) this.cache.delete(key);
    }
  }

  private cacheKey(serverPath: string, workspaceRoot: string): string {
    return `${serverPath}\0${workspaceRoot}`;
  }
}

function executeProjectInfo(serverPath: string, workspaceRoot: string): Promise<string> {
  return new Promise((resolve, reject) => {
    execFile(
      serverPath,
      ['-root', workspaceRoot, '-json', 'project-info'],
      {timeout: 5000, maxBuffer: 1024 * 1024, windowsHide: true},
      (error, stdout, stderr) => {
        if (!error) {
          resolve(stdout);
          return;
        }
        const detail = stderr.trim();
        reject(new Error(detail ? `${error.message}: ${detail}` : error.message));
      },
    );
  });
}
