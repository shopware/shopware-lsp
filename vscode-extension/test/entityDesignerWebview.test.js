const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {test} = require('node:test');
const {JSDOM} = require('jsdom');

const webviewScript = fs.readFileSync(path.join(__dirname, '..', 'dist', 'entityDesignerWebview.js'), 'utf8');

function createWebview(initialState) {
  const messages = [];
  let persistedState = initialState;
  const dom = new JSDOM('<!doctype html><html><body><div id="app"></div></body></html>', {
    runScripts: 'dangerously',
    url: 'https://entity-designer.test/',
    beforeParse(window) {
      window.acquireVsCodeApi = () => ({
        postMessage(message) { messages.push(structuredClone(message)); },
        setState(state) { persistedState = structuredClone(state); },
        getState() { return structuredClone(persistedState); },
      });
      window.structuredClone = structuredClone;
      window.CSS ??= {};
      window.CSS.escape ??= value => String(value).replace(/[^a-zA-Z0-9_-]/g, character => `\\${character}`);
      window.HTMLDialogElement.prototype.showModal = function showModal() { this.setAttribute('open', ''); };
      window.HTMLDialogElement.prototype.close = function close() { this.removeAttribute('open'); };
    },
  });
  dom.window.eval(webviewScript);
  assert.equal(messages.shift().type, 'ready');
  return {dom, document: dom.window.document, messages, getPersistedState: () => persistedState};
}

function send(dom, data) {
  dom.window.dispatchEvent(new dom.window.MessageEvent('message', {data}));
}

function bootstrapValue() {
  return {
    plugin: {composerName: 'acme/example', shopwareVersion: '6.7.1.0', serviceUris: []},
    graph: {snapshotCount: 1, needsReconciliation: false},
    fieldTypes: [
      {kind: 'id', label: 'ID', stored: true},
      {kind: 'string', label: 'String', stored: true},
      {kind: 'int', label: 'Integer', stored: true},
      {kind: 'one-to-many', label: 'One to many', stored: false},
    ],
    existing: [{
      entityName: 'product', definitionClass: 'Shopware\\Core\\Content\\Product\\ProductDefinition',
      entityClass: 'Shopware\\Core\\Content\\Product\\ProductEntity',
      collectionClass: 'Shopware\\Core\\Content\\Product\\ProductCollection',
      fields: [{propertyName: 'id', storageName: 'id', primary: true}, {propertyName: 'parentId', storageName: 'parent_id'}],
    }],
    spec: {
      mode: 'new', pluginRootUri: 'file:///plugin', directoryUri: 'file:///plugin/src/Entity', namespace: 'Acme\\Example',
      className: 'Example', entityName: 'acme_example', createMigration: true,
      fields: [
        {id: 'id', kind: 'id', propertyName: 'id', storageName: 'id', required: true, primary: true, editable: true},
        {id: 'name', kind: 'string', propertyName: 'name', storageName: 'name', maxLength: 255, editable: true},
        {id: 'children', kind: 'one-to-many', propertyName: 'children', targetEntityName: 'product', targetDefinitionClass: 'Shopware\\Core\\Content\\Product\\ProductDefinition', targetCollectionClass: 'Shopware\\Core\\Content\\Product\\ProductCollection', referenceStorageName: 'parent_id', sourceColumn: 'id', editable: true},
      ],
      indexes: [{name: 'idx.acme_example.name', kind: 'index', columns: ['name']}],
    },
  };
}

function requestPreview(document, messages) {
  document.getElementById('refresh').click();
  const message = messages.pop();
  assert.equal(message.type, 'preview');
  return message;
}

test('renders field inspector, inline issues, and typed index columns', () => {
  const {dom, document, messages} = createWebview();
  send(dom, {type: 'bootstrap', value: bootstrapValue()});
  assert.match(document.querySelector('[data-spec="serviceUri"]').textContent, /Create services\.yaml/);

  document.querySelector('[data-select-field="children"]').click();
  assert.match(document.querySelector('.inspector').textContent, /Target foreign-key column/);
  assert.match(document.querySelector('.inspector').textContent, /Delete behavior/);
  assert.equal(document.querySelectorAll('.field-details').length, 0);
  assert.deepEqual(Array.from(document.querySelectorAll('[data-index-column]')).map(input => input.value), ['id', 'name']);

  const request = requestPreview(document, messages);
  send(dom, {type: 'preview', requestId: request.requestId, value: {
    revision: 'revision-1', destructive: false, drift: false, files: [], diff: {},
    issues: [{code: 'entity.relation.reference.invalid', message: 'Invalid target column', fieldId: 'children', severity: 'error'}],
  }});
  assert.ok(document.querySelector('[data-field-row="children"]').classList.contains('invalid'));
  assert.match(document.querySelector('.inspector .issue').textContent, /Invalid target column/);
  dom.window.close();
});

test('keeps rename decision identifiers intact and sends a structured decision', () => {
  const {dom, document, messages} = createWebview();
  send(dom, {type: 'bootstrap', value: bootstrapValue()});
  const request = requestPreview(document, messages);
  send(dom, {type: 'preview', requestId: request.requestId, value: {
    revision: 'revision-rename', destructive: true, drift: false, files: [],
    issues: [{code: 'entity.column.rename.decision', message: 'Choose rename', severity: 'error'}],
    diff: {renameQuestions: [{entity: 'acme_example', added: 'new_name', candidates: [{from: 'old_name', score: 90}]}]},
  }});

  const select = document.querySelector('[data-rename]');
  assert.equal(select.dataset.renameEntity, 'acme_example');
  assert.equal(select.dataset.renameAdded, 'new_name');
  assert.equal(select.outerHTML.includes('\0'), false);
  select.value = 'old_name';
  select.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  const next = messages.pop();
  assert.equal(next.type, 'preview');
  assert.deepEqual(next.decisions, [{kind: 'columnRename', entity: 'acme_example', from: 'old_name', to: 'new_name'}]);
  dom.window.close();
});

test('separates relation search from preview state and prevents duplicate apply', () => {
  const {dom, document, messages} = createWebview();
  send(dom, {type: 'bootstrap', value: bootstrapValue()});
  const request = requestPreview(document, messages);
  send(dom, {type: 'preview', requestId: request.requestId, value: {
    revision: 'revision-clean', snapshotId: 'abcdef1234567890', destructive: false, drift: false, issues: [], diff: {}, files: [],
  }});

  document.querySelector('[data-relation="children"]').click();
  const query = document.getElementById('relationQuery');
  query.value = 'product';
  document.getElementById('relationSearch').click();
  const search = messages.pop();
  assert.equal(search.type, 'search');
  assert.equal(search.query, 'product');
  assert.equal(typeof search.requestId, 'number');

  send(dom, {type: 'search', requestId: search.requestId, value: bootstrapValue().existing});
  document.querySelector('dialog').close();
  document.getElementById('apply').click();
  const apply = messages.pop();
  assert.deepEqual(apply, {type: 'apply', allowDestructive: false});
  send(dom, {type: 'applied', snapshotId: 'abcdef1234567890'});
  assert.equal(document.getElementById('apply').disabled, true);
  assert.match(document.querySelector('.success').textContent, /Applied snapshot abcdef123456/);
  dom.window.close();
});

test('restores an unapplied draft for the same plugin directory', () => {
  const first = createWebview();
  send(first.dom, {type: 'bootstrap', value: bootstrapValue()});
  first.document.querySelector('[data-select-field="name"]').click();
  const property = first.document.querySelector('[data-field-property="name"]');
  property.value = 'displayName';
  property.dispatchEvent(new first.dom.window.Event('change', {bubbles: true}));
  const saved = first.getPersistedState();
  first.dom.window.close();

  const second = createWebview(saved);
  send(second.dom, {type: 'bootstrap', value: bootstrapValue()});
  assert.equal(second.document.querySelector('[data-field-property="name"]').value, 'displayName');
  assert.ok(second.document.querySelector('[data-field-row="name"]').classList.contains('selected'));
  second.dom.window.close();
});

test('requires explicit destructive confirmation before apply', () => {
  const {dom, document, messages} = createWebview();
  send(dom, {type: 'bootstrap', value: bootstrapValue()});
  const request = requestPreview(document, messages);
  send(dom, {type: 'preview', requestId: request.requestId, value: {
    revision: 'revision-destructive', destructive: true, drift: false, issues: [], diff: {removedColumns: [{entity: 'acme_example', before: {name: 'old', sqlType: 'VARCHAR(255)', notNull: false}}]}, files: [],
  }});
  const apply = document.getElementById('apply');
  assert.equal(apply.disabled, true);
  const confirmation = document.getElementById('confirmDestructive');
  confirmation.checked = true;
  confirmation.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  assert.equal(apply.disabled, false);
  apply.click();
  assert.deepEqual(messages.pop(), {type: 'apply', allowDestructive: true});
  dom.window.close();
});
