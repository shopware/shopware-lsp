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
    definitionKinds: ['entity', 'mapping', 'extension', 'bulk-extension'],
    fieldTypes: [
      {kind: 'id', label: 'ID', stored: true, definitionKinds: ['entity', 'mapping']},
      {kind: 'version', label: 'Version ID', stored: true, definitionKinds: ['entity']},
      {kind: 'foreign-key', label: 'Foreign key', stored: true, definitionKinds: ['entity', 'mapping']},
      {kind: 'string', label: 'String', stored: true, definitionKinds: ['entity', 'mapping', 'extension', 'bulk-extension']},
	  {kind: 'enum', label: 'Backed enum', stored: true, definitionKinds: ['entity', 'mapping', 'extension', 'bulk-extension']},
      {kind: 'int', label: 'Integer', stored: true, definitionKinds: ['entity', 'mapping', 'extension', 'bulk-extension']},
      {kind: 'created-at', label: 'Created at', stored: true, definitionKinds: ['entity', 'mapping'], requiresDefaultFieldsOverride: true},
      {kind: 'updated-at', label: 'Updated at', stored: true, definitionKinds: ['entity', 'mapping'], requiresDefaultFieldsOverride: true},
      {kind: 'many-to-one', label: 'Many to one', stored: true, definitionKinds: ['entity', 'mapping', 'extension', 'bulk-extension']},
      {kind: 'one-to-many', label: 'One to many', stored: false, definitionKinds: ['entity', 'extension', 'bulk-extension']},
      {kind: 'hierarchy', label: 'Parent / children hierarchy', stored: true, definitionKinds: ['entity']},
    ],
    existing: [{
      entityName: 'product', definitionClass: 'Shopware\\Core\\Content\\Product\\ProductDefinition',
      entityClass: 'Shopware\\Core\\Content\\Product\\ProductEntity',
      collectionClass: 'Shopware\\Core\\Content\\Product\\ProductCollection',
      fields: [{propertyName: 'id', storageName: 'id', primary: true}, {propertyName: 'parentId', storageName: 'parent_id'}],
    }, {
      entityName: 'tag', definitionClass: 'Shopware\\Core\\System\\Tag\\TagDefinition',
      entityClass: 'Shopware\\Core\\System\\Tag\\TagEntity',
      collectionClass: 'Shopware\\Core\\System\\Tag\\TagCollection',
      fields: [{propertyName: 'id', storageName: 'id', primary: true}],
    }],
    editable: [{
      entityName: 'acme_existing', definitionClass: 'Acme\\Example\\ExistingDefinition',
      definitionKind: 'entity', fileUri: 'file:///plugin/src/ExistingDefinition.php',
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
  assert.deepEqual(Array.from(document.querySelectorAll('[data-index-column]')).map(input => input.value), ['id', 'name', 'created_at', 'updated_at']);

  const request = requestPreview(document, messages);
  send(dom, {type: 'preview', requestId: request.requestId, value: {
    revision: 'revision-1', destructive: false, drift: false, files: [], diff: {},
    issues: [{code: 'entity.relation.reference.invalid', message: 'Invalid target column', fieldId: 'children', severity: 'error'}],
  }});
  assert.ok(document.querySelector('[data-field-row="children"]').classList.contains('invalid'));
  assert.match(document.querySelector('.inspector .issue').textContent, /Invalid target column/);
  dom.window.close();
});

test('loads an indexed class with its resolved definition kind', () => {
  const {dom, document, messages} = createWebview();
  send(dom, {type: 'bootstrap', value: bootstrapValue()});
  const selector = document.getElementById('loadExisting');
  selector.value = 'Acme\\Example\\ExistingDefinition';
  selector.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  assert.deepEqual(messages.pop(), {
    type: 'load',
    definitionClass: 'Acme\\Example\\ExistingDefinition',
    definitionKind: 'entity',
    fileUri: 'file:///plugin/src/ExistingDefinition.php',
  });
  dom.window.close();
});

test('edits to-one association autoload metadata', () => {
  const {dom, document, messages, getPersistedState} = createWebview();
  const bootstrap = bootstrapValue();
  bootstrap.spec.fields.push({
    id: 'product', kind: 'many-to-one', propertyName: 'product', foreignKeyPropertyName: 'productId', storageName: 'product_id',
    targetEntityName: 'product', targetDefinitionClass: 'Shopware\\Core\\Content\\Product\\ProductDefinition',
    targetEntityClass: 'Shopware\\Core\\Content\\Product\\ProductEntity', referenceField: 'id', referenceStorageName: 'id', editable: true,
  });
  send(dom, {type: 'bootstrap', value: bootstrap});

  document.querySelector('[data-select-field="product"]').click();
  const autoload = document.querySelector('[data-association-autoload="product"]');
  assert.ok(autoload);
  assert.equal(autoload.checked, false);
  autoload.checked = true;
  autoload.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  assert.equal(getPersistedState().spec.fields.find(field => field.id === 'product').associationAutoload, true);

  const request = requestPreview(document, messages);
  assert.equal(request.spec.fields.find(field => field.id === 'product').associationAutoload, true);
  dom.window.close();
});

test('creates a specialized Shopware field from its typed template', () => {
  const {dom, document, messages, getPersistedState} = createWebview();
  const bootstrap = bootstrapValue();
  bootstrap.fieldTypes.push({
    id: 'specialized:CustomFields', kind: 'json', label: 'Custom Fields (Shopware)', stored: true,
    definitionKinds: ['entity', 'mapping', 'extension'],
    template: {
      id: '', kind: 'json', propertyName: 'customFields', storageName: 'custom_fields', editable: true,
      implementation: {
        class: 'Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\CustomFields',
        constructorMode: 'storage-property', entityType: 'array', manageEntity: true,
      },
    },
  });
  send(dom, {type: 'bootstrap', value: bootstrap});

  document.getElementById('newKind').value = 'specialized:CustomFields';
  document.getElementById('addField').click();
  const created = getPersistedState().spec.fields.at(-1);
  assert.equal(created.kind, 'json');
  assert.equal(created.propertyName, 'customFields');
  assert.equal(created.storageName, 'custom_fields');
  assert.equal(created.implementation.class, 'Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\CustomFields');
  assert.equal(document.querySelector(`[data-field-kind="${created.id}"]`).value, 'specialized:CustomFields');
  assert.match(document.querySelector('.inspector').textContent, /Shopware field implementation/);

  const request = requestPreview(document, messages);
  assert.equal(request.spec.fields.at(-1).implementation.constructorMode, 'storage-property');
  dom.window.close();
});

test('creates and edits a backed enum field', () => {
  const {dom, document, messages, getPersistedState} = createWebview();
  send(dom, {type: 'bootstrap', value: bootstrapValue()});

  document.getElementById('newKind').value = 'enum';
  document.getElementById('addField').click();
  const created = getPersistedState().spec.fields.at(-1);
  assert.equal(created.kind, 'enum');
  assert.equal(created.enumBackingType, 'string');

  const enumClass = document.querySelector(`[data-enum-class="${created.id}"]`);
  enumClass.value = 'Acme\\Example\\Status';
  enumClass.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  const enumCase = document.querySelector(`[data-enum-case="${created.id}"]`);
  enumCase.value = 'Active';
  enumCase.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  const backing = document.querySelector(`[data-field-choice="${created.id}"][data-choice-key="enumBackingType"]`);
  backing.value = 'int';
  backing.dispatchEvent(new dom.window.Event('change', {bubbles: true}));

  const state = getPersistedState().spec.fields.at(-1);
  assert.equal(state.enumClass, 'Acme\\Example\\Status');
  assert.equal(state.enumCase, 'Active');
  assert.equal(state.enumBackingType, 'int');
  const request = requestPreview(document, messages);
  assert.equal(request.spec.fields.at(-1).enumClass, 'Acme\\Example\\Status');
  dom.window.close();
});

test('edits typed field write protection and allowed scopes', () => {
  const {dom, document, messages, getPersistedState} = createWebview();
  send(dom, {type: 'bootstrap', value: bootstrapValue()});

  document.querySelector('[data-select-field="name"]').click();
  const writeProtected = document.querySelector('[data-field-write-protected="name"]');
  assert.ok(writeProtected);
  writeProtected.checked = true;
  writeProtected.dispatchEvent(new dom.window.Event('change', {bubbles: true}));

  const scopes = document.querySelector('[data-field-write-scopes="name"]');
  assert.ok(scopes);
  scopes.value = 'system, crud, system';
  scopes.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  const field = getPersistedState().spec.fields.find(field => field.id === 'name');
  assert.equal(field.writeProtected, true);
  assert.deepEqual(field.writeProtectedScopes, ['system', 'crud']);

  const request = requestPreview(document, messages);
  assert.equal(request.spec.fields.find(field => field.id === 'name').writeProtected, true);
  assert.deepEqual(request.spec.fields.find(field => field.id === 'name').writeProtectedScopes, ['system', 'crud']);
  dom.window.close();
});

test('edits every zero-argument DAL metadata flag including array and media-search behavior', () => {
  const {dom, document, messages, getPersistedState} = createWebview();
  send(dom, {type: 'bootstrap', value: bootstrapValue()});
  document.querySelector('[data-select-field="name"]').click();
  for (const selector of ['[data-field-as-array="name"]', '[data-field-ignore-unused-media="name"]']) {
    const input = document.querySelector(selector);
    assert.ok(input, selector);
    input.checked = true;
    input.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  }
  const metadata = getPersistedState().spec.fields.find(field => field.id === 'name').metadata;
  assert.equal(metadata.asArray, true);
  assert.equal(metadata.ignoreInUnusedMediaSearch, true);
  const request = requestPreview(document, messages);
  assert.equal(request.spec.fields.find(field => field.id === 'name').metadata.asArray, true);
  assert.equal(request.spec.fields.find(field => field.id === 'name').metadata.ignoreInUnusedMediaSearch, true);
  dom.window.close();
});

test('marks scalar fields as translated and excludes them from parent indexes', () => {
  const {dom, document, messages, getPersistedState} = createWebview();
  send(dom, {type: 'bootstrap', value: bootstrapValue()});
  document.querySelector('[data-select-field="name"]').click();
  const translated = document.querySelector('[data-translated="name"]');
  assert.ok(translated);
  translated.checked = true;
  translated.dispatchEvent(new dom.window.Event('change', {bubbles: true}));

  const state = getPersistedState();
  assert.equal(state.spec.fields.find(field => field.id === 'name').translated, true);
  assert.deepEqual(state.spec.translation, {enabled: true, associationRequired: true});
  assert.deepEqual(Array.from(document.querySelectorAll('[data-index-column]')).map(input => input.value), ['id', 'created_at', 'updated_at', 'name']);
  assert.equal(document.querySelector('[data-index-column][value="name"]').checked, true);

  state.spec.indexes[0].columns = [];
  send(dom, {type: 'loaded', value: state.spec});
  document.querySelector('[data-select-field="name"]').click();
  assert.deepEqual(Array.from(document.querySelectorAll('[data-index-column]')).map(input => input.value), ['id', 'created_at', 'updated_at']);
  const request = requestPreview(document, messages);
  assert.equal(request.spec.fields.find(field => field.id === 'name').translated, true);
  assert.equal(request.spec.translation.enabled, true);
  dom.window.close();
});

test('disables and restores the translation bundle with the last translated field', () => {
  const {dom, document, messages, getPersistedState} = createWebview();
  const bootstrap = bootstrapValue();
  bootstrap.spec.fields[1].translated = true;
  bootstrap.spec.translation = {
    enabled: true, associationRequired: true,
    definitionUri: 'file:///plugin/src/Entity/Aggregate/ExampleTranslation/ExampleTranslationDefinition.php',
  };
  send(dom, {type: 'bootstrap', value: bootstrap});
  document.querySelector('[data-select-field="name"]').click();
  const translated = document.querySelector('[data-translated="name"]');
  translated.checked = false;
  translated.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  assert.equal(getPersistedState().spec.translation.enabled, false);
  assert.match(getPersistedState().spec.translation.definitionUri, /ExampleTranslationDefinition/);

  translated.checked = true;
  translated.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  assert.equal(getPersistedState().spec.translation.enabled, true);
  const request = requestPreview(document, messages);
  assert.equal(request.spec.fields.find(field => field.id === 'name').translated, true);
  dom.window.close();
});

test('configures indexes on generated translation-table columns', () => {
  const {dom, document, messages, getPersistedState} = createWebview();
  const bootstrap = bootstrapValue();
  bootstrap.spec.fields[1].translated = true;
  bootstrap.spec.fields.push({id: 'version', kind: 'version', propertyName: 'versionId', storageName: 'version_id', editable: true});
  bootstrap.spec.translation = {enabled: true, associationRequired: true};
  bootstrap.spec.indexes = [];
  send(dom, {type: 'bootstrap', value: bootstrap});

  document.getElementById('addIndex').click();
  const target = document.querySelector('[data-index-target="0"]');
  target.value = 'translation';
  target.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  const values = Array.from(document.querySelectorAll('[data-index-column="0"]')).map(input => input.value);
  assert.deepEqual(values, ['name', 'acme_example_id', 'language_id', 'created_at', 'updated_at', 'acme_example_version_id']);
  const name = document.querySelector('[data-index-name="0"]');
  name.value = 'uniq.acme_example_translation.name_language';
  name.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  for (const value of ['name', 'language_id']) {
    const input = document.querySelector(`[data-index-column="0"][value="${value}"]`);
    input.checked = true;
    input.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  }
  const state = getPersistedState().spec.indexes[0];
  assert.equal(state.translation, true);
  assert.deepEqual(state.columns, ['name', 'language_id']);
  const request = requestPreview(document, messages);
  assert.equal(request.spec.indexes[0].translation, true);
  dom.window.close();
});

test('switches to a mapping definition with primary standalone foreign keys', () => {
  const {dom, document, messages, getPersistedState} = createWebview();
  const bootstrap = bootstrapValue();
  bootstrap.spec.fields = [bootstrap.spec.fields[0]];
  bootstrap.spec.indexes = [];
  send(dom, {type: 'bootstrap', value: bootstrap});
  const definitionKind = document.getElementById('definitionKind');
  definitionKind.value = 'mapping';
  definitionKind.dispatchEvent(new dom.window.Event('change', {bubbles: true}));

  assert.equal(getPersistedState().spec.definitionKind, 'mapping');
  assert.equal(getPersistedState().spec.fields.length, 0);
  assert.deepEqual(Array.from(document.querySelectorAll('#newKind option')).map(option => option.value), ['foreign-key', 'string', 'enum', 'int', 'created-at', 'updated-at', 'many-to-one']);

  document.getElementById('newKind').value = 'foreign-key';
  document.getElementById('addField').click();
  const foreignKey = getPersistedState().spec.fields[0];
  assert.equal(foreignKey.kind, 'foreign-key');
  assert.equal(foreignKey.required, true);
  assert.equal(foreignKey.primary, true);

  document.querySelector(`[data-relation="${foreignKey.id}"]`).click();
  document.querySelector('[data-target="0"]').click();

  document.getElementById('newKind').value = 'many-to-one';
  document.getElementById('addField').click();
  const association = getPersistedState().spec.fields[1];
  assert.equal(association.required, true);
  assert.equal(association.primary, true);
  assert.equal(association.deleteBehavior, 'restrict');
  document.querySelector(`[data-relation="${association.id}"]`).click();
  document.querySelector('[data-target="1"]').click();

  const request = requestPreview(document, messages);
  assert.equal(request.spec.definitionKind, 'mapping');
  assert.equal(request.spec.fields[0].propertyName, 'productId');
  assert.equal(request.spec.fields[0].storageName, 'product_id');
  assert.equal(request.spec.fields[0].targetDefinitionClass, 'Shopware\\Core\\Content\\Product\\ProductDefinition');
  assert.equal(request.spec.fields[1].foreignKeyPropertyName, 'tagId');
  assert.equal(request.spec.fields[1].storageName, 'tag_id');
  assert.equal(request.spec.translation, undefined);
  dom.window.close();
});

test('creates an entity extension for an indexed target without primary fields', () => {
  const {dom, document, messages, getPersistedState} = createWebview();
  send(dom, {type: 'bootstrap', value: bootstrapValue()});
  const definitionKind = document.getElementById('definitionKind');
  definitionKind.value = 'extension';
  definitionKind.dispatchEvent(new dom.window.Event('change', {bubbles: true}));

  let state = getPersistedState();
  assert.equal(state.spec.definitionKind, 'extension');
  assert.equal(state.spec.fields.some(field => field.kind === 'id'), false);
  assert.deepEqual(Array.from(document.querySelectorAll('#newKind option')).map(option => option.value), ['string', 'enum', 'int', 'many-to-one', 'one-to-many']);
  assert.equal(document.querySelector('[data-field-primary="name"]').disabled, true);
	assert.equal(state.spec.fields.find(field => field.id === 'name').behavior.runtime, true);

  document.getElementById('extensionTarget').click();
  const search = messages.pop();
  assert.equal(search.type, 'search');
  assert.equal(search.query, '');
  send(dom, {type: 'search', requestId: search.requestId, value: bootstrapValue().existing});
  document.querySelector('[data-target="0"]').click();

  state = getPersistedState();
  assert.equal(state.spec.entityName, 'product');
  assert.equal(state.spec.extendedDefinitionClass, 'Shopware\\Core\\Content\\Product\\ProductDefinition');
  assert.deepEqual(state.spec.extendedFields, bootstrapValue().existing[0].fields);
  assert.equal(state.spec.entityClass, undefined);
  assert.equal(state.spec.collectionClass, undefined);

  assert.deepEqual(
	Array.from(document.querySelectorAll('[data-index-column]')).map(input => input.value),
	[],
  );
	document.getElementById('newKind').value = 'string';
	document.getElementById('addField').click();
	assert.equal(getPersistedState().spec.fields.at(-1).behavior.runtime, true);

  const request = requestPreview(document, messages);
  assert.equal(request.spec.definitionKind, 'extension');
  assert.equal(request.spec.entityName, 'product');
  assert.equal(request.spec.extendedDefinitionClass, 'Shopware\\Core\\Content\\Product\\ProductDefinition');
  assert.deepEqual(request.spec.extendedFields, bootstrapValue().existing[0].fields);
  assert.equal(request.spec.fields.some(field => field.primary), false);
  dom.window.close();
});

test('retargets an existing entity extension through indexed search', () => {
  const {dom, document, messages, getPersistedState} = createWebview();
  const bootstrap = bootstrapValue();
  bootstrap.spec = {
    mode: 'edit', definitionKind: 'extension', pluginRootUri: 'file:///plugin', directoryUri: 'file:///plugin/src/Extension',
    namespace: 'Acme\\Example\\Extension', className: 'Catalog', entityName: 'product', createMigration: true,
    extendedDefinitionClass: 'Shopware\\Core\\Content\\Product\\ProductDefinition',
    definitionClass: 'Acme\\Example\\Extension\\CatalogExtension',
    definitionUri: 'file:///plugin/src/Extension/CatalogExtension.php',
    fields: [{id: 'note', kind: 'string', propertyName: 'note', storageName: 'acme_note', behavior: {runtime: true}, editable: true}],
  };
  send(dom, {type: 'bootstrap', value: bootstrap});

  const target = document.getElementById('extensionTarget');
  assert.equal(target.disabled, false);
  target.click();
  const search = messages.pop();
  assert.equal(search.type, 'search');
  send(dom, {type: 'search', requestId: search.requestId, value: bootstrap.existing});
  document.querySelector('[data-target="1"]').click();

  const state = getPersistedState();
  assert.equal(state.spec.entityName, 'tag');
  assert.equal(state.spec.extendedDefinitionClass, 'Shopware\\Core\\System\\Tag\\TagDefinition');
  const request = requestPreview(document, messages);
  assert.equal(request.spec.mode, 'edit');
  assert.equal(request.spec.entityName, 'tag');
  assert.equal(request.spec.extendedDefinitionClass, 'Shopware\\Core\\System\\Tag\\TagDefinition');
  dom.window.close();
});

test('authors a multi-target BulkEntityExtension with isolated fields and indexes', () => {
  const {dom, document, messages, getPersistedState} = createWebview();
  send(dom, {type: 'bootstrap', value: bootstrapValue()});

  const definitionKind = document.getElementById('definitionKind');
  definitionKind.value = 'bulk-extension';
  definitionKind.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  let state = getPersistedState();
  assert.equal(state.spec.definitionKind, 'bulk-extension');
  assert.deepEqual(state.spec.fields, []);
  assert.deepEqual(state.spec.bulkExtensions, []);
  assert.match(document.querySelector('.card').parentElement.textContent, /Bulk entity extension/);

  document.getElementById('addBulkTarget').click();
  let search = messages.pop();
  assert.equal(search.type, 'search');
  send(dom, {type: 'search', requestId: search.requestId, value: bootstrapValue().existing});
  document.querySelector('[data-target="0"]').click();
  state = getPersistedState();
  assert.equal(state.spec.bulkExtensions.length, 1);
  assert.equal(state.spec.bulkExtensions[0].entityName, 'product');
  assert.deepEqual(state.spec.bulkExtensions[0].extendedFields, bootstrapValue().existing[0].fields);

  document.getElementById('newKind').value = 'string';
  document.getElementById('addField').click();
  let productTarget = getPersistedState().spec.bulkExtensions[0];
  assert.equal(productTarget.fields[0].behavior.runtime, true);
  assert.equal(document.querySelector(`[data-field-primary="${productTarget.fields[0].id}"]`).disabled, true);

  document.getElementById('newKind').value = 'many-to-one';
  document.getElementById('addField').click();
  productTarget = getPersistedState().spec.bulkExtensions[0];
  const association = productTarget.fields[1];
  document.querySelector(`[data-relation="${association.id}"]`).click();
  document.querySelector('[data-target="1"]').click();
  productTarget = getPersistedState().spec.bulkExtensions[0];
  assert.equal(productTarget.fields[1].foreignKeyPropertyName, 'tagId');
  assert.equal(productTarget.fields[1].storageName, 'tag_id');

  document.getElementById('addIndex').click();
  assert.deepEqual(Array.from(document.querySelectorAll('[data-index-column="0"]')).map(input => input.value), ['tag_id', 'id', 'parent_id']);
  const tagColumn = document.querySelector('[data-index-column="0"][value="tag_id"]');
  tagColumn.checked = true;
  tagColumn.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  assert.deepEqual(getPersistedState().spec.bulkExtensions[0].indexes[0].columns, ['tag_id']);

  document.getElementById('addBulkTarget').click();
  search = messages.pop();
  send(dom, {type: 'search', requestId: search.requestId, value: bootstrapValue().existing});
  document.querySelector('[data-target="1"]').click();
  document.getElementById('newKind').value = 'string';
  document.getElementById('addField').click();
  state = getPersistedState();
  assert.equal(state.spec.bulkExtensions.length, 2);
  assert.equal(state.spec.bulkExtensions[1].entityName, 'tag');
  assert.equal(state.spec.bulkExtensions[1].fields[0].behavior.runtime, true);

  const targetSelector = document.getElementById('bulkTarget');
  targetSelector.value = state.spec.bulkExtensions[0].id;
  targetSelector.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  assert.equal(document.querySelectorAll('[data-field-row]').length, 2);

  const request = requestPreview(document, messages);
  assert.equal(request.spec.definitionKind, 'bulk-extension');
  assert.deepEqual(request.spec.fields, []);
  assert.equal(request.spec.bulkExtensions.length, 2);
  assert.equal(request.spec.bulkExtensions[0].indexes[0].columns[0], 'tag_id');
  assert.equal(request.spec.bulkExtensions[1].fields[0].behavior.runtime, true);
  dom.window.close();
});

test('hides BulkEntityExtension when the server does not advertise it', () => {
  const {dom, document} = createWebview();
  const bootstrap = bootstrapValue();
  bootstrap.plugin.shopwareVersion = '~6.6.9';
  bootstrap.definitionKinds = ['entity', 'mapping', 'extension'];
  send(dom, {type: 'bootstrap', value: bootstrap});

  assert.deepEqual(
    Array.from(document.getElementById('definitionKind').options, option => option.value),
    ['entity', 'mapping', 'extension'],
  );
  dom.window.close();
});

test('uses legacy definition capabilities for an older custom server', () => {
  const {dom, document} = createWebview();
  const bootstrap = bootstrapValue();
  delete bootstrap.definitionKinds;
  send(dom, {type: 'bootstrap', value: bootstrap});

  assert.deepEqual(
    Array.from(document.getElementById('definitionKind').options, option => option.value),
    ['entity', 'mapping', 'extension'],
  );
  dom.window.close();
});

test('renders a custom BulkEntityExtension collect method as locked source', () => {
  const {dom, document, messages} = createWebview();
  const bootstrap = bootstrapValue();
  bootstrap.spec.mode = 'edit';
  bootstrap.spec.definitionKind = 'bulk-extension';
  bootstrap.spec.fields = [];
  bootstrap.spec.indexes = [];
  bootstrap.spec.bulkExtensions = [];
  bootstrap.spec.collectMethodRaw = "public function collect(): \\Generator\n{\n    yield from $this->computed();\n}";
  send(dom, {type: 'bootstrap', value: bootstrap});

  assert.equal(document.getElementById('definitionKind').disabled, true);
  assert.match(document.body.textContent, /non-literal collect\(\) implementation is preserved exactly/);
  assert.match(document.querySelector('pre').textContent, /yield from \$this->computed/);
  assert.equal(document.getElementById('addField').disabled, true);
  assert.equal(document.getElementById('addIndex').disabled, true);
  assert.equal(requestPreview(document, messages).spec.collectMethodRaw, bootstrap.spec.collectMethodRaw);
  dom.window.close();
});

test('authors typed EntityExtension modifyFields flag changes', () => {
  const {dom, document, messages, getPersistedState} = createWebview();
  const bootstrap = bootstrapValue();
  bootstrap.spec.definitionKind = 'extension';
  bootstrap.spec.entityName = 'product';
  bootstrap.spec.extendedDefinitionClass = 'Shopware\\Core\\Content\\Product\\ProductDefinition';
  bootstrap.spec.extendedFields = bootstrap.existing[0].fields;
  bootstrap.spec.fields = [{id: 'runtime', kind: 'string', propertyName: 'runtime', storageName: 'runtime', behavior: {runtime: true}, editable: true}];
  bootstrap.spec.indexes = [];
  send(dom, {type: 'bootstrap', value: bootstrap});

  document.getElementById('addFieldModification').click();
  const property = document.querySelector('[data-modification-property="0"]');
  property.value = 'name';
  property.dispatchEvent(new dom.window.Event('change', {bubbles: true}));

  const addSelect = document.querySelector('[data-add-modification-flag-select="0"]');
  addSelect.value = 'api-aware';
  document.querySelector('[data-add-modification-flag="0"]').click();
  const sources = document.querySelector('[data-modification-flag="0:0"][data-flag-key="apiSources"]');
  sources.value = 'Shopware\\Core\\Framework\\Api\\Context\\AdminApiSource';
  sources.dispatchEvent(new dom.window.Event('change', {bubbles: true}));

  const removeSelect = document.querySelector('[data-remove-modification-flag-select="0"]');
  removeSelect.value = 'required';
  document.querySelector('[data-add-removed-flag="0"]').click();

  const modification = getPersistedState().spec.fieldModifications[0];
  assert.equal(modification.propertyName, 'name');
  assert.deepEqual(modification.addFlags, [{kind: 'api-aware', apiSources: ['Shopware\\Core\\Framework\\Api\\Context\\AdminApiSource']}]);
  assert.deepEqual(modification.removeFlags, ['required']);
  const request = requestPreview(document, messages);
  assert.deepEqual(request.spec.fieldModifications[0], modification);
  dom.window.close();
});

test('changes an existing class-based definition kind while retaining deletion identities', () => {
  const {dom, document, messages, getPersistedState} = createWebview();
  const bootstrap = bootstrapValue();
  bootstrap.spec.mode = 'edit';
  bootstrap.spec.definitionKind = 'entity';
  bootstrap.spec.definitionClass = 'Acme\\Example\\ExampleDefinition';
  bootstrap.spec.definitionUri = 'file:///plugin/src/Entity/ExampleDefinition.php';
  bootstrap.spec.entityClass = 'Acme\\Example\\ExampleEntity';
  bootstrap.spec.collectionClass = 'Acme\\Example\\ExampleCollection';
  bootstrap.spec.entityUri = 'file:///plugin/src/Entity/custom/ExampleEntity.php';
  bootstrap.spec.collectionUri = 'file:///plugin/src/Entity/custom/ExampleCollection.php';
  bootstrap.spec.translation = {
    enabled: true,
    definitionUri: 'file:///plugin/src/Entity/Aggregate/ExampleTranslation/ExampleTranslationDefinition.php',
  };
  bootstrap.spec.fields[1].translated = true;
  send(dom, {type: 'bootstrap', value: bootstrap});

  const selector = document.getElementById('definitionKind');
  assert.equal(selector.disabled, false);
  selector.value = 'extension';
  selector.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  const state = getPersistedState().spec;
  assert.equal(state.definitionKind, 'extension');
  assert.equal(state.definitionClass, 'Acme\\Example\\ExampleDefinition');
  assert.equal(state.definitionUri, 'file:///plugin/src/Entity/ExampleDefinition.php');
  assert.equal(state.entityUri, 'file:///plugin/src/Entity/custom/ExampleEntity.php');
  assert.equal(state.collectionUri, 'file:///plugin/src/Entity/custom/ExampleCollection.php');
  assert.equal(state.translation.enabled, false);
  assert.equal(state.fields.some(field => field.kind === 'id'), false);
	assert.equal(state.fields.find(field => field.id === 'name').behavior.runtime, true);
	assert.deepEqual(state.indexes, []);

  document.getElementById('extensionTarget').click();
  const search = messages.pop();
  send(dom, {type: 'search', requestId: search.requestId, value: bootstrap.existing});
  document.querySelector('[data-target="0"]').click();
  const request = requestPreview(document, messages);
  assert.equal(request.spec.definitionKind, 'extension');
  assert.equal(request.spec.extendedDefinitionClass, 'Shopware\\Core\\Content\\Product\\ProductDefinition');
  dom.window.close();
});

test('adds one atomic version-aware hierarchy bundle', () => {
  const {dom, document, messages, getPersistedState} = createWebview();
  const bootstrap = bootstrapValue();
  bootstrap.spec.fields = bootstrap.spec.fields.slice(0, 2);
  bootstrap.spec.indexes = [];
  send(dom, {type: 'bootstrap', value: bootstrap});

  document.getElementById('newKind').value = 'version';
  document.getElementById('addField').click();
  document.getElementById('newKind').value = 'hierarchy';
  document.getElementById('addField').click();
  const hierarchy = getPersistedState().spec.fields.find(field => field.kind === 'hierarchy');
  assert.ok(hierarchy);
  assert.equal(hierarchy.propertyName, 'children');
  assert.equal(hierarchy.hierarchyParentProperty, 'parent');
  assert.equal(hierarchy.foreignKeyPropertyName, 'parentId');
  assert.equal(hierarchy.storageName, 'parent_id');
  assert.equal(hierarchy.deleteBehavior, 'cascade');
  assert.equal(hierarchy.hierarchyVersionAware, true);
  const childrenProperty = document.querySelector(`[data-field-property="${hierarchy.id}"]`);
  assert.equal(childrenProperty.disabled, false, 'the children property remains customizable');
  childrenProperty.value = 'descendants';
  childrenProperty.dispatchEvent(new dom.window.Event('change'));
  assert.equal(getPersistedState().spec.fields.find(field => field.id === hierarchy.id).propertyName, 'descendants');
  assert.match(document.querySelector('.inspector').textContent, /ParentFkField.*ParentAssociationField.*ChildrenAssociationField.*parent reference version/s);

  const request = requestPreview(document, messages);
  assert.equal(request.spec.fields.filter(field => field.kind === 'hierarchy').length, 1);
  assert.equal(request.spec.fields.some(field => field.storageName === 'parent_version_id'), false, 'the hierarchy owns the derived parent version field');
  dom.window.close();
});

test('enables inheritance with one hierarchy and typed field flags', () => {
  const {dom, document, messages, getPersistedState} = createWebview();
  send(dom, {type: 'bootstrap', value: bootstrapValue()});

  const inheritance = document.querySelector('[data-inheritance-aware]');
  assert.ok(inheritance);
  inheritance.checked = true;
  inheritance.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  let state = getPersistedState();
  assert.equal(state.spec.inheritanceAware, true);
  assert.equal(state.spec.fields.filter(field => field.kind === 'hierarchy').length, 1);

  document.querySelector('[data-select-field="name"]').click();
  const inherited = document.querySelector('[data-inherited="name"]');
  assert.ok(inherited);
  inherited.checked = true;
  inherited.dispatchEvent(new dom.window.Event('change', {bubbles: true}));

  document.querySelector('[data-select-field="children"]').click();
  const associationInherited = document.querySelector('[data-association-inherited="children"]');
  assert.ok(associationInherited);
  associationInherited.checked = true;
  associationInherited.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  const reverse = document.querySelector('[data-reverse-inherited="children"]');
  reverse.value = 'variants';
  reverse.dispatchEvent(new dom.window.Event('change', {bubbles: true}));

  const request = requestPreview(document, messages);
  assert.equal(request.spec.inheritanceAware, true);
  assert.equal(request.spec.fields.find(field => field.id === 'name').inherited, true);
  assert.equal(request.spec.fields.find(field => field.id === 'children').associationInherited, true);
  assert.equal(request.spec.fields.find(field => field.id === 'children').reverseInheritedProperty, 'variants');

  const refreshedInheritance = document.querySelector('[data-inheritance-aware]');
  refreshedInheritance.checked = false;
  refreshedInheritance.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  state = getPersistedState();
  assert.equal(state.spec.inheritanceAware, false);
  assert.equal(state.spec.fields.find(field => field.id === 'name').inherited, false);
  assert.equal(state.spec.fields.find(field => field.id === 'children').associationInherited, false);
  assert.equal(state.spec.fields.find(field => field.id === 'children').reverseInheritedProperty, 'variants', 'reverse inheritance belongs to the target and remains valid');
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

test('resolves a technical table rename without treating it as drop and create', () => {
  const {dom, document, messages} = createWebview();
  send(dom, {type: 'bootstrap', value: bootstrapValue()});
  const request = requestPreview(document, messages);
  send(dom, {type: 'preview', requestId: request.requestId, value: {
    revision: 'revision-table-rename', destructive: true, drift: false, files: [],
    issues: [{code: 'entity.table.rename.decision', message: 'Choose table rename', severity: 'error'}],
    diff: {entityRenameQuestions: [{added: 'acme_renamed', candidates: [{from: 'acme_example', score: 100}]}]},
  }});

  const select = document.querySelector('[data-entity-rename]');
  assert.equal(select.dataset.entityRenameAdded, 'acme_renamed');
  select.value = 'acme_example';
  select.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  const next = messages.pop();
  assert.equal(next.type, 'preview');
  assert.deepEqual(next.decisions, [{kind: 'entityRename', entity: 'acme_renamed', from: 'acme_example', to: 'acme_renamed'}]);
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

test('edits typed class-level definition behavior and metadata', () => {
  const {dom, document, messages, getPersistedState} = createWebview();
  const bootstrap = bootstrapValue();
  bootstrap.spec.definitionBehavior = {
    overrideBaseFields: true,
    baseFields: [{id: 'base-code', kind: 'string', storageName: 'base_code', propertyName: 'baseCode', editable: true}],
  };
  send(dom, {type: 'bootstrap', value: bootstrap});
  assert.match(document.body.textContent, /Custom base fields/);

  const parent = document.querySelector('[data-definition-parent="parent"]');
  parent.value = 'Shopware\\Core\\Content\\Product\\ProductDefinition';
  parent.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  const versionAware = document.querySelector('[data-definition-version-aware="parent"]');
  versionAware.value = 'false';
  versionAware.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  const defaultFields = document.querySelector('[data-definition-default-fields="parent"]');
  defaultFields.checked = true;
  defaultFields.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  assert.ok(Array.from(document.getElementById('newKind').options).some(option => option.value === 'created-at'));
  document.getElementById('newKind').value = 'created-at';
  document.getElementById('addField').click();
  const restrict = document.querySelector('[data-definition-restrict-properties="parent"]');
  restrict.value = 'id, name, id';
  restrict.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  const since = document.querySelector('[data-definition-metadata-text="parent:since"]');
  since.value = '6.7.1.0';
  since.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  const hydrator = document.querySelector('[data-definition-metadata-text="parent:hydratorClass"]');
  hydrator.value = 'Acme\\Example\\ExampleHydrator';
  hydrator.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  document.querySelector('[data-add-definition-default="parent:defaults"]').click();
  const property = document.querySelector('[data-definition-default-property="parent:defaults:0"]');
  property.value = 'active';
  property.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  const expression = document.querySelector('[data-definition-default-expression="parent:defaults:0"]');
  expression.value = 'true';
  expression.dispatchEvent(new dom.window.Event('change', {bubbles: true}));

  const state = getPersistedState().spec;
  assert.equal(state.definitionBehavior.parentDefinitionClass, 'Shopware\\Core\\Content\\Product\\ProductDefinition');
  assert.equal(state.definitionBehavior.versionAware, false);
  assert.equal(state.definitionBehavior.overrideDefaultFields, true);
  assert.equal(state.definitionBehavior.overrideBaseFields, true);
  assert.equal(state.definitionBehavior.baseFields[0].propertyName, 'baseCode');
  assert.equal(state.fields.some(field => field.kind === 'created-at'), true);
  assert.deepEqual(state.definitionBehavior.restrictDeleteMetaProperties, ['id', 'name']);
  assert.equal(state.definitionMetadata.since, '6.7.1.0');
  assert.equal(state.definitionMetadata.hydratorClass, 'Acme\\Example\\ExampleHydrator');
  assert.deepEqual(state.definitionMetadata.defaults, [{propertyName: 'active', valueExpression: 'true'}]);

  const request = requestPreview(document, messages);
  assert.deepEqual(request.spec.definitionBehavior, state.definitionBehavior);
  assert.deepEqual(request.spec.definitionMetadata, state.definitionMetadata);
  dom.window.close();
});

test('edits translation metadata and locks custom definition methods', () => {
  const {dom, document, messages, getPersistedState} = createWebview();
  const bootstrap = bootstrapValue();
  bootstrap.spec.fields[1].translated = true;
  bootstrap.spec.translation = {enabled: true, associationRequired: true};
  bootstrap.spec.definitionBehavior = {
    inheritanceAwareMethodRaw: 'public function isInheritanceAware(): bool { return $this->configured(); }',
  };
  bootstrap.spec.translation.definitionMetadata = {
    defaultsMethodRaw: 'public function getDefaults(): array { return $this->computed(); }',
  };
  send(dom, {type: 'bootstrap', value: bootstrap});

  assert.equal(document.getElementById('definitionKind').disabled, true);
  assert.equal(document.querySelector('[data-inheritance-aware]').disabled, true);
  assert.equal(document.querySelector('[data-add-definition-default="translation:defaults"]'), null);
  assert.match(document.body.textContent, /Custom method preserved/);

  const version = document.querySelector('[data-definition-version-aware="translation"]');
  version.value = 'true';
  version.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  const since = document.querySelector('[data-definition-metadata-text="translation:since"]');
  since.value = '6.7.2.0';
  since.dispatchEvent(new dom.window.Event('change', {bubbles: true}));
  const request = requestPreview(document, messages);
  assert.equal(request.spec.translation.definitionBehavior.versionAware, true);
  assert.equal(request.spec.translation.definitionMetadata.since, '6.7.2.0');
  assert.match(getPersistedState().spec.translation.definitionMetadata.defaultsMethodRaw, /computed/);
  dom.window.close();
});
