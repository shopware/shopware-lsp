const assert = require('node:assert/strict');
const path = require('node:path');
const {test} = require('node:test');
const {
  createMcpProcessDefinition,
  normalizeMemoryLimitMiB,
} = require('../dist/mcpServerModel.js');
const {
  resolveServerExecutable,
  serverExecutableName,
} = require('../dist/serverExecutable.js');

test('builds a workspace-scoped MCP stdio process', () => {
  const definition = createMcpProcessDefinition({
    serverPath: '/extension/shopware-lsp',
    workspaceRoot: '/workspace/shopware',
    label: 'Shopware LSP',
    version: '0.2.0',
    memoryLimitMiB: 512.9,
  });
  assert.deepEqual(definition, {
    label: 'Shopware LSP',
    command: '/extension/shopware-lsp',
    args: ['-root', '/workspace/shopware', 'mcp'],
    cwd: '/workspace/shopware',
    env: {GOMEMLIMIT: '512MiB'},
    version: '0.2.0',
  });
});

test('omits an inactive memory limit and normalizes invalid values', () => {
  assert.equal(normalizeMemoryLimitMiB(undefined), 0);
  assert.equal(normalizeMemoryLimitMiB(Number.NaN), 0);
  assert.equal(normalizeMemoryLimitMiB(-1), 0);
  assert.deepEqual(createMcpProcessDefinition({
    serverPath: '/extension/shopware-lsp',
    workspaceRoot: '/workspace/shopware',
    label: 'Shopware LSP',
    version: 'dev',
  }).env, {});
});

test('passes editor diagnostic and MCP tool overrides to the VS Code launched MCP server', () => {
  const definition = createMcpProcessDefinition({
    serverPath: '/extension/shopware-lsp',
    workspaceRoot: '/workspace/shopware',
    label: 'Shopware LSP',
    version: 'dev',
    editorConfiguration: {
      diagnostics: {overrides: [{files: ['custom/plugins/Test/**'], enabled: false}]},
      mcp: {tools: {shopware_scaffold: false}},
    },
  });
  assert.deepEqual(JSON.parse(definition.env.SHOPWARE_LSP_EDITOR_CONFIGURATION), {
    diagnostics: {overrides: [{files: ['custom/plugins/Test/**'], enabled: false}]},
    mcp: {tools: {shopware_scaffold: false}},
  });
});

test('passes the unsupported-project override before the MCP command', () => {
  const definition = createMcpProcessDefinition({
    serverPath: '/extension/shopware-lsp',
    workspaceRoot: '/workspace/library',
    label: 'Shopware LSP',
    version: 'dev',
    allowUnsupportedProject: true,
  });
  assert.deepEqual(definition.args, [
    '-root', '/workspace/library', '-allow-unsupported-project', 'mcp',
  ]);
});

test('uses a configured executable without probing and finds packaged binaries', () => {
  assert.equal(resolveServerExecutable({
    configuredPath: '/custom/shopware-lsp',
    extensionPath: '/extension',
    exists: () => false,
  }), '/custom/shopware-lsp');

  const packaged = path.join('/extension', 'shopware-lsp.exe');
  assert.equal(resolveServerExecutable({
    extensionPath: '/extension',
    workspaceRoot: '/workspace/shopware',
    platform: 'win32',
    exists: candidate => candidate === packaged,
  }), packaged);
  assert.equal(serverExecutableName('win32'), 'shopware-lsp.exe');
  assert.equal(serverExecutableName('darwin'), 'shopware-lsp');
});
