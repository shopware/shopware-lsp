const assert = require('node:assert/strict');
const Module = require('node:module');
const {test} = require('node:test');

const vscode = {
  workspace: {workspaceFolders: []},
  window: {
    activeTextEditor: undefined,
    showQuickPick: async items => items[0],
  },
};
const originalLoad = Module._load;
Module._load = function(request, parent, isMain) {
  if (request === 'vscode') return vscode;
  return originalLoad.call(this, request, parent, isMain);
};
const {WorkspaceClientManager} = require('../dist/workspaceClientManager.js');
Module._load = originalLoad;

function folder(name, fsPath) {
  const value = `file://${fsPath}`;
  return {
    name,
    uri: {scheme: 'file', fsPath, toString: () => value},
  };
}

function deferred() {
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  return {promise, resolve};
}

function plan(workspaceFolder, lifecycle = {}) {
  const key = workspaceFolder.uri.toString();
  return {
    key,
    fsPath: workspaceFolder.uri.fsPath,
    enabled: true,
    folder: workspaceFolder,
    async start() {
      lifecycle.started = (lifecycle.started ?? 0) + 1;
      lifecycle.onStart?.();
      if (lifecycle.wait) await lifecycle.wait;
      return {
        key,
        folder: workspaceFolder,
        client: {name: workspaceFolder.name},
        async dispose() {
          lifecycle.disposed = (lifecycle.disposed ?? 0) + 1;
        },
      };
    },
  };
}

test('starts disjoint roots and suppresses an overlapping child', async () => {
  const shop = folder('shop', '/workspace/shop');
  const plugin = folder('plugin', '/workspace/shop/custom/plugins/Test');
  const other = folder('other', '/workspace/other');
  vscode.workspace.workspaceFolders = [plugin, other, shop];
  const manager = new WorkspaceClientManager(async item => plan(item), () => {});

  await manager.reconcile();

  assert.deepEqual(
    manager.runningEntries().map(entry => entry.folder.name).sort(),
    ['other', 'shop'],
  );
  assert.equal(manager.entryForUri({
    scheme: 'file',
    fsPath: '/workspace/shop/custom/plugins/Test/src/Test.php',
  }).folder.name, 'shop');
  await manager.stopAll();
});

test('starts a supported child when its parent is inactive', async () => {
  const shop = folder('shop', '/workspace/shop');
  const plugin = folder('plugin', '/workspace/shop/custom/plugins/Test');
  vscode.workspace.workspaceFolders = [shop, plugin];
  const manager = new WorkspaceClientManager(
    async item => item === shop ? undefined : plan(item),
    () => {},
  );

  await manager.reconcile();

  assert.deepEqual(manager.runningEntries().map(entry => entry.folder.name), ['plugin']);
  await manager.stopAll();
});

test('disposes a stale client when workspace folders change during startup', async () => {
  const first = folder('first', '/workspace/first');
  const second = folder('second', '/workspace/second');
  const gate = deferred();
  const started = deferred();
  const firstLifecycle = {wait: gate.promise, onStart: started.resolve};
  const secondLifecycle = {};
  vscode.workspace.workspaceFolders = [first];
  const manager = new WorkspaceClientManager(
    async item => plan(item, item === first ? firstLifecycle : secondLifecycle),
    () => {},
  );

  const initial = manager.reconcile();
  await started.promise;
  vscode.workspace.workspaceFolders = [second];
  const replacement = manager.reconcile();
  gate.resolve();
  await Promise.all([initial, replacement]);

  assert.equal(firstLifecycle.disposed, 1);
  assert.equal(secondLifecycle.started, 1);
  assert.deepEqual(manager.runningEntries().map(entry => entry.folder.name), ['second']);
  await manager.stopAll();
});

test('uses the active editor and prompts only when multiple clients remain', async () => {
  const first = folder('first', '/workspace/first');
  const second = folder('second', '/workspace/second');
  vscode.workspace.workspaceFolders = [first, second];
  vscode.window.activeTextEditor = {
    document: {uri: {scheme: 'file', fsPath: '/workspace/second/src/Test.php'}},
  };
  let prompted = false;
  vscode.window.showQuickPick = async items => {
    prompted = true;
    return items[0];
  };
  const manager = new WorkspaceClientManager(async item => plan(item), () => {});
  await manager.reconcile();

  const entry = await manager.resolveEntry(undefined, 'Test command');

  assert.equal(entry.folder.name, 'second');
  assert.equal(prompted, false);
  vscode.window.activeTextEditor = undefined;
  vscode.window.showQuickPick = async items => {
    prompted = true;
    return items.find(item => item.entry.folder.name === 'second');
  };
  const selected = await manager.resolveEntry(undefined, 'Test command');
  assert.equal(selected.folder.name, 'second');
  assert.equal(prompted, true);
  await manager.stopAll();
});
