/** What `shopware/cache/clear` reports. */
export interface ClearReport {
  cleared: unknown[];
  inUse: unknown[];
  freedBytes: number;
}

/** A byte count as people read it: `1.5 GiB`, `320 MiB`, `12 KiB`. */
export function formatBytes(bytes: number): string {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let value = Math.max(0, bytes);
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${unit === 0 ? value : value.toFixed(1)} ${units[unit]}`;
}

/** The message shown after clearing caches of other projects. */
export function describeClearReport(report: ClearReport): string {
  const count = (items: unknown[], noun: string) => `${items.length} ${noun}${items.length === 1 ? '' : 's'}`;
  const kept = report.inUse.length > 0 ? ` ${count(report.inUse, 'cache')} in use by a running server kept.` : '';
  if (report.cleared.length === 0) return `Shopware LSP: No caches to clear.${kept}`;
  return `Shopware LSP: Cleared ${count(report.cleared, 'cache')}, ${formatBytes(report.freedBytes)} freed.${kept}`;
}
