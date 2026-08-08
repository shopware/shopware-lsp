export function setNested(
  target: Record<string, unknown>,
  path: string[],
  value: unknown,
): void {
  let current = target;
  for (const segment of path.slice(0, -1)) {
    const next = current[segment];
    if (!next || typeof next !== 'object' || Array.isArray(next)) current[segment] = {};
    current = current[segment] as Record<string, unknown>;
  }
  const key = path[path.length - 1];
  if (value === null) delete current[key];
  else current[key] = value;
  pruneEmpty(target);
}

export interface DiagnosticOverrideValue {
  files: string[];
  enabled?: boolean;
  inspections?: Record<string, boolean>;
  rules?: Record<string, string>;
}

export interface DiagnosticOverrideUpdate {
  enabled?: boolean;
  rule?: {id: string; severity: string};
}

export function normalizeConfigurationPattern(value: string): string {
  return value.split('\\').join('/').replace(/^\.\//, '').replace(/^\/+|\/+$/g, '');
}

export function diagnosticPattern(relativePath: string, recursive: boolean): string {
  const normalized = normalizeConfigurationPattern(relativePath);
  if (!normalized || normalized === '.') return '**';
  return recursive ? `${normalized}/**` : normalized;
}

export function upsertDiagnosticOverride(
  overrides: DiagnosticOverrideValue[] | undefined,
  pattern: string,
  update: DiagnosticOverrideUpdate,
): DiagnosticOverrideValue[] {
  const normalized = normalizeConfigurationPattern(pattern) || '**';
  const result: DiagnosticOverrideValue[] = (overrides || []).map(value => {
    const copy: DiagnosticOverrideValue = {...value, files: [...value.files]};
    if (value.inspections) copy.inspections = {...value.inspections};
    if (value.rules) copy.rules = {...value.rules};
    return copy;
  });
  let index = -1;
  for (let current = result.length - 1; current >= 0; current--) {
    if (result[current].files.length === 1 &&
      normalizeConfigurationPattern(result[current].files[0]) === normalized) {
      index = current;
      break;
    }
  }
  if (index < 0) {
    result.push({files: [normalized]});
    index = result.length - 1;
  }
  if (update.enabled !== undefined) result[index].enabled = update.enabled;
  if (update.rule) {
    result[index].rules = {
      ...(result[index].rules || {}),
      [update.rule.id]: update.rule.severity,
    };
  }
  return result;
}

function pruneEmpty(value: Record<string, unknown>): boolean {
  for (const [key, child] of Object.entries(value)) {
    if (child && typeof child === 'object' && !Array.isArray(child) &&
      pruneEmpty(child as Record<string, unknown>)) {
      delete value[key];
    }
  }
  return Object.keys(value).length === 0;
}
