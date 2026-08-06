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

function pruneEmpty(value: Record<string, unknown>): boolean {
  for (const [key, child] of Object.entries(value)) {
    if (child && typeof child === 'object' && !Array.isArray(child) &&
      pruneEmpty(child as Record<string, unknown>)) {
      delete value[key];
    }
  }
  return Object.keys(value).length === 0;
}
