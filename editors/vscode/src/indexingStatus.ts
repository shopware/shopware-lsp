import * as vscode from 'vscode';

interface IndexingState {
  name: string;
  indexing: boolean;
  summary?: string;
}

export class IndexingStatus implements vscode.Disposable {
  private readonly states = new Map<string, IndexingState>();
  private hideTimer: NodeJS.Timeout | undefined;

  constructor(private readonly item: vscode.StatusBarItem) {}

  started(key: string, name: string): void {
    this.states.set(key, {name, indexing: true});
    this.refresh();
  }

  completed(key: string, name: string, seconds: number): void {
    this.states.set(key, {
      name,
      indexing: false,
      summary: `Indexed in ${seconds} seconds`,
    });
    this.refresh();
  }

  failed(key: string, name: string, message: string): void {
    this.states.set(key, {name, indexing: false, summary: `Failed: ${message}`});
    this.refresh();
  }

  remove(key: string): void {
    this.states.delete(key);
    this.refresh();
  }

  dispose(): void {
    if (this.hideTimer) clearTimeout(this.hideTimer);
    this.states.clear();
    this.item.hide();
  }

  private refresh(): void {
    if (this.hideTimer) {
      clearTimeout(this.hideTimer);
      this.hideTimer = undefined;
    }
    const values = [...this.states.values()];
    const active = values.filter(state => state.indexing);
    if (active.length > 0) {
      this.item.text = active.length === 1
        ? '$(sync~spin) Shopware: Indexing…'
        : `$(sync~spin) Shopware: Indexing (${active.length})…`;
      this.item.tooltip = active.map(state => `${state.name}: indexing`).join('\n');
      this.item.show();
      return;
    }
    if (values.length === 0) {
      this.item.hide();
      return;
    }
    this.item.text = '$(check) Shopware: Indexed';
    this.item.tooltip = values.map(state =>
      `${state.name}: ${state.summary ?? 'ready'}`).join('\n');
    this.item.show();
    this.hideTimer = setTimeout(() => this.item.hide(), 10_000);
  }
}
