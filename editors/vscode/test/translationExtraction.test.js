const assert = require('node:assert/strict');
const {test} = require('node:test');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

function fixture(changeSource = false) {
  const callbacks = new Map();
  const errors = [];
  const applied = [];
  const requests = [];
  const sourceURI = 'file:///template.twig';
  const targetURI = 'file:///messages.en.yaml';
  const uri = value => ({toString: () => value});
  const source = {uri: uri(sourceURI), version: 3, getText: () => '<p>Hello</p>'};
  const target = {uri: uri(targetURI), version: 4};
  const vscode = {
    Uri: {parse: uri},
    commands: {registerCommand: (id, callback) => {callbacks.set(id, callback); return {}; }},
    workspace: {
      textDocuments: [source, target],
      openTextDocument: async () => source,
      asRelativePath: value => value.toString(),
      applyEdit: async edit => {applied.push(edit); return true;},
    },
    window: {
      showErrorMessage: value => errors.push(value),
      showInformationMessage: () => {},
      showInputBox: async () => {if (changeSource) source.version++; return 'new.key';},
      showQuickPick: async (items, options) => {
        if (options.canPickMany) {target.version++; return items;}
        return items[0];
      },
    },
  };
  const client = {
    protocol2CodeConverter: {asWorkspaceEdit: async edit => edit},
    sendRequest: async (method, params) => {
      requests.push({method, params});
      if (method.endsWith('/prepare')) return {workspaceEdits: true, text: 'Hello', range: params.range, domains: ['messages'], defaultDomain: 'messages'};
      if (params.preview) return {targets: [{fileUri: targetURI, file: 'messages.en.yaml', format: 'yaml'}]};
      assert.deepEqual(Array.from(params.targetUris), [targetURI]);
      assert.equal(target.version, 5, 'final generation follows locale selection');
      return {edit: {documentChanges: [source, target].map(document => ({textDocument: {uri: document.uri.toString(), version: document.version}, edits: []}))}};
    },
  };
  const module = {exports: {}};
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../dist/symfonyGenerationCommands.js'), 'utf8'), {
    module, exports: module.exports,
    require: name => {assert.equal(name, 'vscode'); return vscode;},
  });
  module.exports.registerSymfonyGenerationCommands({subscriptions: []}, {clientForUri: () => client});
  return {
    errors, applied, requests,
    run: () => callbacks.get('shopware.symfony.extractTwigTranslation')(sourceURI, {start: {line: 0, character: 3}, end: {line: 0, character: 8}}),
  };
}

test('translation extraction builds a versioned plan after selecting locale files', async () => {
  const state = fixture();
  await state.run();
  assert.deepEqual(state.errors, []);
  assert.equal(state.requests.length, 3);
  assert.equal(state.applied.length, 1);
  assert.equal(state.applied[0].documentChanges[1].textDocument.version, 5);
});

test('translation extraction aborts when the source changes during prompts', async () => {
  const state = fixture(true);
  await state.run();
  assert.equal(state.requests.length, 1);
  assert.equal(state.applied.length, 0);
  assert.match(state.errors[0], /Twig document changed/);
});
