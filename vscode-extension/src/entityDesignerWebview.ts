declare function acquireVsCodeApi(): {
  postMessage(message: unknown): void;
  setState(state: unknown): void;
  getState(): unknown;
};

type Field = {
  id: string; kind: string; propertyName: string; foreignKeyPropertyName?: string;
  storageName?: string; required?: boolean; primary?: boolean; apiAware?: boolean; searchRanking?: number; maxLength?: number;
  preservedFlags?: string[]; modifiersBeforeFlags?: string[]; modifiersAfterFlags?: string[];
  associationFlags?: string[]; associationModifiersBeforeFlags?: string[]; associationModifiersAfterFlags?: string[];
  min?: number; max?: number; elementTypeClass?: string;
  targetDefinitionClass?: string; targetEntityClass?: string; targetCollectionClass?: string;
  targetEntityName?: string; referenceField?: string; referenceStorageName?: string;
  mappingDefinitionClass?: string; mappingLocalColumn?: string; mappingReferenceColumn?: string;
  sourceColumn?: string; usesExistingColumn?: boolean; deleteBehavior?: string; associationApiAware?: boolean;
  associationSearchRanking?: number; migrationDefault?: string; editable: boolean; raw?: string;
};
type Spec = {
  mode: string; pluginRootUri: string; directoryUri: string; namespace: string;
  className: string; entityName: string; definitionClass?: string; entityClass?: string;
  collectionClass?: string; fields: Field[]; indexes?: {name: string; kind: string; columns: string[]}[];
  serviceUri?: string; createMigration: boolean; migrationName?: string; migrationTimestamp?: number;
};
type Bootstrap = {
  plugin: {composerName: string; shopwareVersion?: string; serviceUris?: string[]};
  spec: Spec; fieldTypes: {kind: string; label: string; stored: boolean}[];
  graph: {snapshotCount: number; leaves?: string[]; needsReconciliation: boolean};
  existing?: Target[];
};
type Target = {entityName: string; definitionClass: string; entityClass?: string; collectionClass?: string; fileUri?: string; versionAware?: boolean; fields?: {storageName: string; propertyName: string; primary?: boolean}[]};
type Decision = {kind: string; entity: string; from?: string; to?: string};
type Preview = {
  revision: string; files?: {uri: string; action: string; after: string}[];
  issues?: Issue[];
  diff?: SchemaDiff;
  destructive: boolean; drift: boolean; driftMessage?: string; snapshotId?: string;
  migrationTimestamp?: number;
};
type Issue = {code: string; message: string; fieldId?: string; severity?: string};
type Column = {name: string; sqlType: string; notNull: boolean};
type SchemaDiff = {
  createdEntities?: {name: string}[]; removedEntities?: {name: string}[];
  addedColumns?: {entity: string; after?: Column}[]; removedColumns?: {entity: string; before?: Column}[];
  changedColumns?: {entity: string; before?: Column; after?: Column}[];
  renameQuestions?: {entity: string; added: string; candidates: {from: string; score: number}[]}[];
  addedIndexes?: {entity: string; index: {name: string; unique: boolean; columns: string[]}}[];
  removedIndexes?: {entity: string; index: {name: string; unique: boolean; columns: string[]}}[];
  addedForeignKeys?: {entity: string; foreignKey: {name: string}}[];
  removedForeignKeys?: {entity: string; foreignKey: {name: string}}[];
  changedPrimaryKeys?: {entity: string; before?: string[]; after?: string[]}[];
};
type PersistedState = {spec?: Spec; decisions?: Decision[]; driftDecision?: string; selectedFieldId?: string; applied?: boolean};

const vscode = acquireVsCodeApi();
const app = document.getElementById('app')!;
let restoredState = vscode.getState() as PersistedState | undefined;
let bootstrap: Bootstrap | undefined;
let spec: Spec | undefined;
let preview: Preview | undefined;
let decisions: Decision[] = [];
let driftDecision = '';
let previewBusy = false;
let actionBusy = false;
let relationBusy = false;
let errorMessage = '';
let successMessage = '';
let selectedFile = 0;
let selectedFieldId = '';
let destructiveConfirmed = false;
let appliedRevision = '';
let previewTimer: number | undefined;
let previewRequestId = 0;
let previewQueued = false;
let relationRequestId = 0;
let relationField = '';
let relationRole: 'target' | 'mapping' = 'target';
let relationResults: Target[] = [];
let knownTargets: Target[] = [];

window.addEventListener('message', event => {
  const message = event.data;
  switch (message.type) {
    case 'bootstrap':
      bootstrap = message.value as Bootstrap;
      spec = structuredClone(bootstrap.spec);
      previewRequestId++; preview = undefined; decisions = []; driftDecision = ''; previewBusy = false; actionBusy = false; errorMessage = ''; successMessage = ''; appliedRevision = '';
      selectedFieldId = spec.fields[0]?.id ?? '';
      if (!restoredState?.applied && restoredState?.spec?.directoryUri === spec.directoryUri && restoredState.spec.pluginRootUri === spec.pluginRootUri) {
        spec = structuredClone(restoredState.spec);
        decisions = structuredClone(restoredState.decisions ?? []);
        driftDecision = restoredState.driftDecision ?? '';
        selectedFieldId = restoredState.selectedFieldId && spec.fields.some(field => field.id === restoredState!.selectedFieldId) ? restoredState.selectedFieldId : selectedFieldId;
      }
      restoredState = undefined;
      knownTargets = bootstrap.existing ?? []; relationResults = knownTargets;
      render(); schedulePreview();
      break;
    case 'loaded':
      spec = structuredClone(message.value as Spec);
      previewRequestId++; preview = undefined; decisions = []; driftDecision = ''; previewBusy = false; actionBusy = false; errorMessage = ''; successMessage = ''; appliedRevision = '';
      selectedFieldId = spec.fields[0]?.id ?? '';
      persistState();
      render(); schedulePreview();
      break;
    case 'preview':
      if (message.requestId !== previewRequestId) break;
      if (previewQueued) {
        previewBusy = false; previewQueued = false; schedulePreview(); break;
      }
      preview = message.value as Preview; previewBusy = false; errorMessage = '';
      if (preview.migrationTimestamp && spec) spec.migrationTimestamp = preview.migrationTimestamp;
      selectedFile = Math.min(selectedFile, Math.max(0, (preview.files?.length ?? 1) - 1));
      render();
      break;
    case 'search':
      if (message.requestId !== relationRequestId) break;
      relationResults = message.value as Target[]; knownTargets = mergeTargets(knownTargets, relationResults); relationBusy = false; renderRelationDialog();
      break;
    case 'applied':
      actionBusy = false; appliedRevision = preview?.revision ?? ''; if (spec) spec.migrationTimestamp = undefined; successMessage = `Applied snapshot ${String(message.snapshotId).slice(0, 12)}. Close and reopen the designer before starting another migration.`; errorMessage = ''; persistState(true); render();
      break;
    case 'error':
      if (message.operation === 'preview' && message.requestId !== previewRequestId) break;
      if (message.operation === 'search' && message.requestId !== relationRequestId) break;
      if (message.operation === 'preview') {
        previewBusy = false;
        if (previewQueued) { previewQueued = false; schedulePreview(); }
      } else if (message.operation === 'search') {
        relationBusy = false;
      } else {
        actionBusy = false;
      }
      errorMessage = String(message.message); successMessage = ''; render();
      break;
  }
});

function render(): void {
  if (!bootstrap || !spec) {
    app.textContent = 'Loading entity designer…';
    return;
  }
  const issues = preview?.issues ?? [];
  const hasErrors = issues.some(issue => !issue.severity || issue.severity === 'error');
  const files = preview?.files ?? [];
  const currentFile = files[selectedFile];
  const existing = (bootstrap.existing ?? []).slice(0, 100);
  const selectedField = spec.fields.find(field => field.id === selectedFieldId);
  const working = previewBusy || actionBusy;
  const applyDisabled = working || hasErrors || !preview?.revision || appliedRevision === preview.revision || Boolean(preview?.destructive && !destructiveConfirmed);
  app.innerHTML = `
    <div id="status" class="toolbar"><h1 style="margin:0">Entity Designer</h1><span class="badge">${esc(bootstrap.plugin.composerName)}</span>${bootstrap.plugin.shopwareVersion ? `<span class="muted">Shopware ${esc(bootstrap.plugin.shopwareVersion)}</span>` : ''}<span style="flex:1"></span><span class="muted">${working ? 'Working…' : preview?.snapshotId ? `snapshot ${esc(preview.snapshotId.slice(0, 12))}` : ''}</span></div>
    ${errorMessage ? `<div class="card error" role="alert">${esc(errorMessage)}</div>` : ''}
    ${successMessage ? `<div class="card success" role="status">${esc(successMessage)}</div>` : ''}
    ${bootstrap.graph.needsReconciliation ? `<div class="card danger"><b>Snapshot branches need reconciliation.</b><p>Select the authoritative leaf when branches differ.</p><div class="row"><select id="leaf">${(bootstrap.graph.leaves ?? []).map(id => `<option value="${attr(id)}">${esc(id.slice(0, 16))}</option>`).join('')}</select><button id="reconcile">Create merge snapshot</button></div></div>` : ''}
    <section class="card"><div class="row"><h2 style="margin-right:auto">Identity</h2>${existing.length ? `<select id="loadExisting"><option value="">Edit an indexed entity…</option>${existing.map(item => `<option value="${attr(item.definitionClass)}">${esc(item.entityName)} — ${esc(item.definitionClass)}</option>`).join('')}</select>` : ''}</div>
      <div class="grid"><label>PHP base class<input data-spec="className" value="${attr(spec.className)}" ${spec.mode === 'edit' ? 'disabled' : ''}></label><label>Technical entity name<input data-spec="entityName" value="${attr(spec.entityName)}" ${spec.mode === 'edit' ? 'disabled' : ''}></label><label>Namespace<input data-spec="namespace" value="${attr(spec.namespace)}" ${spec.mode === 'edit' ? 'disabled' : ''}></label></div>
      <p class="muted">${spec.mode === 'edit' ? 'Existing entity identity is fixed. Custom definition and entity members are preserved.' : 'Creates a definition, entity, collection, service registration, migration, and committed schema snapshot.'}</p>
    </section>
    <section class="card"><div class="toolbar"><h2 style="margin-right:auto">Fields</h2><select id="newKind">${bootstrap.fieldTypes.filter(type => type.kind !== 'id').map(type => `<option value="${attr(type.kind)}">${esc(type.label)}</option>`).join('')}</select><button id="addField">Add field</button></div>
      <div class="field-list">
        <div class="field field-header muted"><span></span><span>Type</span><span>Property</span><span>Storage / relation</span><span>Required</span><span>Primary</span><span>API</span><span>Actions</span></div>
        ${spec.fields.map((field, index) => fieldRow(field, index, issues.filter(issue => issue.fieldId === field.id))).join('')}
      </div>
      ${selectedField ? fieldInspector(selectedField, issues.filter(issue => issue.fieldId === selectedField.id)) : '<p class="muted">Select a field to edit its advanced settings.</p>'}
    </section>
    <section class="card"><div class="toolbar"><h2 style="margin-right:auto">Indexes</h2><button id="addIndex">Add index</button></div>${(spec.indexes ?? []).map(indexRow).join('') || '<p class="muted">No custom indexes.</p>'}</section>
    <section class="card"><h2>Migration</h2><div class="grid"><label>Name suffix<input data-spec="migrationName" value="${attr(spec.migrationName ?? '')}" placeholder="UpdateExample"></label><label>Service configuration<select data-spec="serviceUri"><option value="">Create services.yaml</option>${(bootstrap.plugin.serviceUris ?? []).map(uri => `<option value="${attr(uri)}" ${uri === spec!.serviceUri ? 'selected' : ''}>${esc(uri.split('/').pop() ?? uri)}</option>`).join('')}</select></label><label><input type="checkbox" data-bool-spec="createMigration" ${spec.createMigration ? 'checked' : ''}> Generate migration when DB changes</label></div><p class="muted">Drops and other destructive SQL are intentionally generated in <code>update()</code>. No <code>updateDestructive()</code> method is used.</p></section>
    ${preview?.drift ? `<section class="card danger"><h2>Manual schema drift</h2><p>${esc(preview.driftMessage ?? '')}</p><div class="row"><button data-drift="adopt">Adopt current code as baseline</button><button data-drift="migrate">Generate migration from last snapshot</button></div></section>` : ''}
    ${(preview?.diff?.renameQuestions ?? []).map(question => `<section class="card danger"><b>Is ${esc(question.entity)}.${esc(question.added)} a rename?</b><select data-rename data-rename-entity="${attr(question.entity)}" data-rename-added="${attr(question.added)}"><option value="">Choose…</option><option value="create" ${renameDecisionValue(question.entity, question.added) === 'create' ? 'selected' : ''}>No, create a new column</option>${question.candidates.map(candidate => `<option value="${attr(candidate.from)}" ${renameDecisionValue(question.entity, question.added) === candidate.from ? 'selected' : ''}>Rename ${esc(candidate.from)} (${candidate.score}% match)</option>`).join('')}</select></section>`).join('')}
    <section class="card"><div class="toolbar"><h2 style="margin-right:auto">Review changes</h2><button id="refresh" class="secondary">Refresh</button>${preview?.destructive ? '<span class="error"><b>Destructive database change</b></span>' : ''}<label class="confirm-destructive">${preview?.destructive ? `<input id="confirmDestructive" type="checkbox" ${destructiveConfirmed ? 'checked' : ''}> I understand data may be removed` : ''}</label><button id="apply" ${applyDisabled ? 'disabled' : ''}>Apply atomically</button></div>
      ${issues.map(issue => `<div class="issue ${issue.severity === 'warning' ? 'warning' : 'error'}">${esc(issue.message)} <span class="muted">[${esc(issue.code)}]</span>${issue.fieldId ? ` <button class="link" data-show-field="${attr(issue.fieldId)}">Show field</button>` : ''}</div>`).join('')}
      ${preview?.diff ? renderChangeSummary(preview.diff) : ''}
      ${files.length ? `<div class="row" style="margin-top:10px"><select id="fileSelect">${files.map((file, index) => `<option value="${index}" ${index === selectedFile ? 'selected' : ''}>${esc(file.action)} ${esc(shortUri(file.uri))}</option>`).join('')}</select></div><pre>${esc(currentFile?.after ?? '')}</pre>` : !issues.length ? '<p class="muted">Waiting for preview…</p>' : ''}
    </section>
    <dialog id="relationDialog"><div id="relationContent"></div></dialog>`;
  bindEvents();
  if (actionBusy || appliedRevision) document.querySelectorAll<HTMLInputElement | HTMLSelectElement | HTMLButtonElement>('input,select,button').forEach(control => { control.disabled = true; });
}

function fieldRow(field: Field, index: number, issues: Issue[]): string {
  const locked = !field.editable;
  const relation = isRelation(field.kind);
  const toOne = field.kind === 'many-to-one' || field.kind === 'one-to-one';
  const nonStored = isNonStored(field);
  const referenceVersion = field.kind === 'reference-version';
  const fixed = field.kind === 'id' || field.kind === 'auto-increment' || field.kind === 'version' || field.kind === 'created-at' || field.kind === 'updated-at';
  const target = field.targetEntityName || field.targetDefinitionClass || 'Choose target…';
  const storage = relation
    ? `<button class="secondary" data-relation="${attr(field.id)}">${esc(target)}</button>${toOne || referenceVersion ? `<input data-field-storage="${attr(field.id)}" value="${attr(field.storageName ?? '')}" placeholder="${referenceVersion ? 'target_version_id' : 'foreign_key_id'}">` : field.kind === 'one-to-many' ? `<input data-reference-storage="${attr(field.id)}" value="${attr(field.referenceStorageName ?? '')}" placeholder="target FK column">` : ''}`
    : `<input data-field-storage="${attr(field.id)}" value="${attr(field.storageName ?? '')}" ${locked || fixed ? 'disabled' : ''} placeholder="storage_name">`;
  return `<div class="field ${locked ? 'locked' : ''} ${field.id === selectedFieldId ? 'selected' : ''} ${issues.length ? 'invalid' : ''}" data-field-row="${attr(field.id)}"><span class="muted">${index + 1}</span><select data-field-kind="${attr(field.id)}" ${locked || field.kind === 'id' ? 'disabled' : ''}>${bootstrap!.fieldTypes.map(type => `<option value="${attr(type.kind)}" ${type.kind === field.kind ? 'selected' : ''}>${esc(type.label)}</option>`).join('')}</select><input data-field-property="${attr(field.id)}" value="${attr(field.propertyName ?? '')}" ${locked || fixed ? 'disabled' : ''} placeholder="propertyName"><div class="field-storage">${storage}</div><input type="checkbox" title="Required" aria-label="Required" data-field-required="${attr(field.id)}" ${field.required ? 'checked' : ''} ${locked || fixed || nonStored || field.usesExistingColumn ? 'disabled' : ''}><input type="checkbox" title="Primary" aria-label="Primary" data-field-primary="${attr(field.id)}" ${field.primary ? 'checked' : ''} ${locked || fixed || nonStored || field.usesExistingColumn ? 'disabled' : ''}><input type="checkbox" title="API-aware" aria-label="API-aware" data-field-api="${attr(field.id)}" ${(nonStored || field.usesExistingColumn ? field.associationApiAware : field.apiAware) ? 'checked' : ''} ${locked ? 'disabled' : ''}><div class="field-actions"><button class="secondary" title="Field details" aria-label="Field details" data-select-field="${attr(field.id)}">${issues.length ? `!${issues.length}` : '…'}</button><button class="secondary" title="Move up" aria-label="Move up" data-up="${attr(field.id)}" ${index === 0 ? 'disabled' : ''}>↑</button><button class="secondary" title="Move down" aria-label="Move down" data-down="${attr(field.id)}" ${index === spec!.fields.length - 1 ? 'disabled' : ''}>↓</button>${field.kind !== 'id' && field.editable ? `<button class="secondary" title="Remove field" aria-label="Remove field" data-remove="${attr(field.id)}">×</button>` : ''}</div>${locked ? `<div class="field-note muted">Custom field expression is locked and will be preserved.</div>` : ''}</div>`;
}

function fieldInspector(field: Field, issues: Issue[]): string {
  if (!field.editable) {
    return `<div class="inspector"><div class="toolbar"><h3>Custom field</h3><span class="muted">Preserved exactly as written</span></div><pre>${esc(field.raw ?? '')}</pre></div>`;
  }
  const controls: string[] = [];
  const target = targetByClass(field.targetDefinitionClass);
  const targetFields = target?.fields ?? [];
  const mapping = targetByClass(field.mappingDefinitionClass);
  const mappingFields = mapping?.fields ?? [];
  const relation = isRelation(field.kind);
  const toOne = field.kind === 'many-to-one' || field.kind === 'one-to-one';
  if (relation) {
    controls.push(`<div class="detail-control"><span>Relation target</span><button class="secondary" data-relation="${attr(field.id)}">${esc(field.targetEntityName || field.targetDefinitionClass || 'Choose target…')}</button></div>`);
  }
  if (field.kind === 'string') controls.push(numberControl(field, 'maxLength', 'Maximum length', 1, '1', 16383));
  if (field.kind === 'int') {
    controls.push(numberControl(field, 'min', 'Minimum value'));
    controls.push(numberControl(field, 'max', 'Maximum value'));
  }
  if (field.kind === 'string' || field.kind === 'long-text') controls.push(numberControl(field, 'searchRanking', 'Search ranking', 0, 'any'));
  if (field.kind === 'list') controls.push(`<label><span>Element field class (optional)</span><input data-element-class="${attr(field.id)}" value="${attr(field.elementTypeClass ?? '')}" placeholder="Shopware\\…\\StringField"></label>`);
  if (toOne) {
    if (field.kind === 'one-to-one') controls.push(checkboxControl(field.id, 'existing-column', 'Reuse an existing local column', Boolean(field.usesExistingColumn)));
    if (!field.usesExistingColumn) controls.push(`<label><span>Foreign-key property</span><input data-fk-property="${attr(field.id)}" value="${attr(field.foreignKeyPropertyName ?? '')}" placeholder="productId"></label>`);
    controls.push(choiceControl(field, 'referenceField', 'Referenced target field', field.referenceField ?? 'id', targetFields.map(item => item.propertyName)));
    controls.push(choiceControl(field, 'referenceStorageName', 'Referenced target column', field.referenceStorageName ?? 'id', targetFields.map(item => item.storageName)));
    controls.push(deleteControl(field));
  }
  if (field.kind === 'one-to-many') {
    controls.push(choiceControl(field, 'referenceStorageName', 'Target foreign-key column', field.referenceStorageName ?? '', targetFields.map(item => item.storageName)));
    controls.push(choiceControl(field, 'sourceColumn', 'Local source column', field.sourceColumn ?? 'id', storedColumns()));
    controls.push(deleteControl(field));
  }
  if (field.kind === 'many-to-many') {
    controls.push(`<div class="detail-control"><span>Mapping definition</span><button class="secondary" data-mapping="${attr(field.id)}">${esc(field.mappingDefinitionClass || 'Choose mapping definition…')}</button></div>`);
    controls.push(choiceControl(field, 'mappingLocalColumn', 'Mapping local column', field.mappingLocalColumn ?? '', mappingFields.map(item => item.storageName)));
    controls.push(choiceControl(field, 'mappingReferenceColumn', 'Mapping target column', field.mappingReferenceColumn ?? '', mappingFields.map(item => item.storageName)));
    controls.push(choiceControl(field, 'sourceColumn', 'Local source column', field.sourceColumn ?? 'id', storedColumns()));
    controls.push(choiceControl(field, 'referenceField', 'Target reference field', field.referenceField ?? 'id', targetFields.map(item => item.propertyName)));
  }
  if (toOne || field.kind === 'one-to-many' || field.kind === 'many-to-many') {
    controls.push(checkboxControl(field.id, 'association-api', 'Association API-aware', Boolean(field.associationApiAware)));
    controls.push(numberControl(field, 'associationSearchRanking', 'Association search ranking', 0, 'any'));
  }
  if (field.required && !isNonStored(field) && !['id', 'version', 'auto-increment', 'created-at'].includes(field.kind)) {
    controls.push(`<label class="span-two"><span>Existing-row backfill SQL</span><input data-migration-default="${attr(field.id)}" value="${attr(field.migrationDefault ?? '')}" placeholder="for example 0, 'unknown', JSON_OBJECT()"></label>`);
  }
  const preserved = [...(field.preservedFlags ?? []), ...(field.modifiersBeforeFlags ?? []), ...(field.modifiersAfterFlags ?? []), ...(field.associationFlags ?? []), ...(field.associationModifiersBeforeFlags ?? []), ...(field.associationModifiersAfterFlags ?? [])];
  return `<div class="inspector"><div class="toolbar"><h3 style="margin-right:auto">${esc(field.propertyName || field.kind)} details</h3><span class="muted">${esc(field.kind)}</span></div>${issues.map(issue => `<div class="issue error">${esc(issue.message)} <span class="muted">[${esc(issue.code)}]</span></div>`).join('')}<div class="inspector-grid">${controls.join('')}</div>${preserved.length ? `<details><summary>Preserved custom flags and modifiers (${preserved.length})</summary><pre>${esc(preserved.join('\n'))}</pre></details>` : ''}</div>`;
}

function indexRow(index: {name: string; kind: string; columns: string[]}, row: number): string {
  const columns = Array.from(new Set([...storedColumns(), ...index.columns]));
  return `<div class="index-row"><input data-index-name="${row}" value="${attr(index.name)}" placeholder="idx.entity.column"><select data-index-kind="${row}"><option value="index" ${index.kind === 'index' ? 'selected' : ''}>Index</option><option value="unique" ${index.kind === 'unique' ? 'selected' : ''}>Unique</option></select><div class="column-picker">${columns.map(column => `<label><input type="checkbox" data-index-column="${row}" value="${attr(column)}" ${index.columns.includes(column) ? 'checked' : ''}> ${esc(column)}</label>`).join('') || '<span class="muted">Add a stored field first.</span>'}</div><button class="secondary" data-remove-index="${row}">Remove</button></div>`;
}

function renderChangeSummary(diff: SchemaDiff): string {
  const changes: string[] = [];
  for (const entity of diff.createdEntities ?? []) changes.push(`<li class="added">Create table <code>${esc(entity.name)}</code></li>`);
  for (const entity of diff.removedEntities ?? []) changes.push(`<li class="removed">Drop table <code>${esc(entity.name)}</code></li>`);
  for (const change of diff.addedColumns ?? []) changes.push(`<li class="added">Add <code>${esc(change.entity)}.${esc(change.after?.name)}</code> ${esc(change.after?.sqlType ?? '')}</li>`);
  for (const change of diff.removedColumns ?? []) changes.push(`<li class="removed">Drop <code>${esc(change.entity)}.${esc(change.before?.name)}</code></li>`);
  for (const change of diff.changedColumns ?? []) changes.push(`<li class="changed">Change <code>${esc(change.entity)}.${esc(change.after?.name)}</code> from ${esc(change.before?.sqlType ?? '')} to ${esc(change.after?.sqlType ?? '')}</li>`);
  for (const change of diff.addedIndexes ?? []) changes.push(`<li class="added">Add ${change.index.unique ? 'unique ' : ''}index <code>${esc(change.index.name)}</code></li>`);
  for (const change of diff.removedIndexes ?? []) changes.push(`<li class="removed">Drop index <code>${esc(change.index.name)}</code></li>`);
  for (const change of diff.addedForeignKeys ?? []) changes.push(`<li class="added">Add foreign key <code>${esc(change.foreignKey.name)}</code></li>`);
  for (const change of diff.removedForeignKeys ?? []) changes.push(`<li class="removed">Drop foreign key <code>${esc(change.foreignKey.name)}</code></li>`);
  for (const change of diff.changedPrimaryKeys ?? []) changes.push(`<li class="changed">Change primary key on <code>${esc(change.entity)}</code>: ${esc((change.before ?? []).join(', ') || 'none')} → ${esc((change.after ?? []).join(', ') || 'none')}</li>`);
  return changes.length ? `<div class="change-summary"><h3>Database changes</h3><ul>${changes.join('')}</ul></div>` : '<p class="muted">No database schema changes.</p>';
}

function numberControl(field: Field, key: keyof Field, label: string, min?: number, step = '1', max?: number): string {
  const value = field[key];
  return `<label><span>${esc(label)}</span><input type="number" data-field-number="${attr(field.id)}" data-number-key="${attr(String(key))}" value="${typeof value === 'number' ? attr(value) : ''}" ${min === undefined ? '' : `min="${min}"`} ${max === undefined ? '' : `max="${max}"`} step="${attr(step)}"></label>`;
}

function choiceControl(field: Field, key: keyof Field, label: string, value: string, choices: string[]): string {
  const values = Array.from(new Set([value, ...choices].filter(Boolean)));
  if (!choices.length) return `<label><span>${esc(label)}</span><input data-field-choice="${attr(field.id)}" data-choice-key="${attr(String(key))}" value="${attr(value)}"></label>`;
  return `<label><span>${esc(label)}</span><select data-field-choice="${attr(field.id)}" data-choice-key="${attr(String(key))}"><option value="">Choose…</option>${values.map(choice => `<option value="${attr(choice)}" ${choice === value ? 'selected' : ''}>${esc(choice)}</option>`).join('')}</select></label>`;
}

function checkboxControl(fieldId: string, attribute: string, label: string, checked: boolean): string {
  return `<label class="compact"><input type="checkbox" data-${attribute}="${attr(fieldId)}" ${checked ? 'checked' : ''}><span>${esc(label)}</span></label>`;
}

function deleteControl(field: Field): string {
  return `<label><span>Delete behavior</span><select data-delete="${attr(field.id)}"><option value="" ${!field.deleteBehavior ? 'selected' : ''}>framework default</option><option value="restrict" ${field.deleteBehavior === 'restrict' ? 'selected' : ''}>restrict</option><option value="cascade" ${field.deleteBehavior === 'cascade' ? 'selected' : ''}>cascade</option><option value="set-null" ${field.deleteBehavior === 'set-null' ? 'selected' : ''}>set null</option></select></label>`;
}

function bindEvents(): void {
  document.querySelectorAll<HTMLInputElement | HTMLSelectElement>('[data-spec]').forEach(input => input.addEventListener('change', () => {
    const key = input.dataset.spec as keyof Spec;
    (spec as unknown as Record<string, unknown>)[key] = input.value;
    if (key === 'className' || key === 'namespace') {
      spec!.definitionClass = undefined; spec!.entityClass = undefined; spec!.collectionClass = undefined;
    }
    changed();
  }));
  document.querySelectorAll<HTMLInputElement>('[data-bool-spec]').forEach(input => input.addEventListener('change', () => { (spec as unknown as Record<string, unknown>)[input.dataset.boolSpec!] = input.checked; changed(); }));
  bindFieldInputs();
  document.getElementById('addField')?.addEventListener('click', () => addField((document.getElementById('newKind') as HTMLSelectElement).value));
  document.getElementById('addIndex')?.addEventListener('click', () => { (spec!.indexes ??= []).push({name: `idx.${spec!.entityName}.`, kind: 'index', columns: []}); changed(true); });
  document.querySelectorAll<HTMLElement>('[data-remove-index]').forEach(button => button.addEventListener('click', () => { spec!.indexes!.splice(Number(button.dataset.removeIndex), 1); changed(true); }));
  document.querySelectorAll<HTMLInputElement>('[data-index-name]').forEach(input => input.addEventListener('change', () => { spec!.indexes![Number(input.dataset.indexName)].name = input.value; changed(); }));
  document.querySelectorAll<HTMLSelectElement>('[data-index-kind]').forEach(input => input.addEventListener('change', () => { spec!.indexes![Number(input.dataset.indexKind)].kind = input.value; changed(); }));
  document.querySelectorAll<HTMLInputElement>('[data-index-column]').forEach(input => input.addEventListener('change', () => {
    const index = spec!.indexes![Number(input.dataset.indexColumn)];
    index.columns = Array.from(document.querySelectorAll<HTMLInputElement>(`[data-index-column="${input.dataset.indexColumn}"]:checked`)).map(item => item.value);
    if (index.name.endsWith('.') && index.columns.length === 1) index.name += index.columns[0];
    changed(true);
  }));
  document.getElementById('refresh')?.addEventListener('click', requestPreview);
  document.getElementById('confirmDestructive')?.addEventListener('change', event => {
    destructiveConfirmed = (event.target as HTMLInputElement).checked;
    const apply = document.getElementById('apply') as HTMLButtonElement | null;
    if (apply) apply.disabled = !destructiveConfirmed || previewBusy || actionBusy || !preview?.revision;
  });
  document.getElementById('apply')?.addEventListener('click', () => { actionBusy = true; render(); vscode.postMessage({type: 'apply', allowDestructive: destructiveConfirmed}); });
  document.getElementById('fileSelect')?.addEventListener('change', event => { selectedFile = Number((event.target as HTMLSelectElement).value); render(); });
  document.getElementById('loadExisting')?.addEventListener('change', event => { const value = (event.target as HTMLSelectElement).value; if (value) { const target = bootstrap?.existing?.find(item => item.definitionClass === value); previewRequestId++; previewBusy = false; actionBusy = true; render(); vscode.postMessage({type: 'load', definitionClass: value, fileUri: target?.fileUri}); } });
  document.querySelectorAll<HTMLElement>('[data-drift]').forEach(button => button.addEventListener('click', () => { driftDecision = button.dataset.drift!; destructiveConfirmed = false; persistState(); requestPreview(); }));
  document.querySelectorAll<HTMLSelectElement>('[data-rename]').forEach(select => select.addEventListener('change', () => { if (!select.value) return; const entity = select.dataset.renameEntity!; const added = select.dataset.renameAdded!; decisions = decisions.filter(item => !(item.entity === entity && item.to === added)); decisions.push(select.value === 'create' ? {kind: 'columnCreate', entity, to: added} : {kind: 'columnRename', entity, from: select.value, to: added}); destructiveConfirmed = false; persistState(); requestPreview(); }));
  document.querySelectorAll<HTMLElement>('[data-show-field]').forEach(button => button.addEventListener('click', () => { selectedFieldId = button.dataset.showField!; render(); document.querySelector(`[data-field-row="${cssEscape(selectedFieldId)}"]`)?.scrollIntoView({block: 'center'}); }));
  document.getElementById('reconcile')?.addEventListener('click', () => { actionBusy = true; render(); vscode.postMessage({type: 'reconcile', selectedLeaf: (document.getElementById('leaf') as HTMLSelectElement).value}); });
}

function bindFieldInputs(): void {
  const field = (id: string): Field => spec!.fields.find(item => item.id === id)!;
  const textBindings: [string, keyof Field][] = [['field-property','propertyName'],['field-storage','storageName'],['fk-property','foreignKeyPropertyName'],['reference-storage','referenceStorageName'],['migration-default','migrationDefault'],['element-class','elementTypeClass'],['mapping-local','mappingLocalColumn'],['mapping-reference','mappingReferenceColumn'],['source-column','sourceColumn']];
  for (const [attribute, key] of textBindings) document.querySelectorAll<HTMLInputElement>(`[data-${attribute}]`).forEach(input => input.addEventListener('change', () => { (field(input.dataset[toDataset(attribute)]!) as unknown as Record<string, unknown>)[key] = input.value; changed(); }));
  document.querySelectorAll<HTMLInputElement | HTMLSelectElement>('[data-field-choice]').forEach(input => input.addEventListener('change', () => { (field(input.dataset.fieldChoice!) as unknown as Record<string, unknown>)[input.dataset.choiceKey!] = input.value; changed(); }));
  document.querySelectorAll<HTMLInputElement>('[data-field-number]').forEach(input => input.addEventListener('change', () => { if (!input.checkValidity()) { input.reportValidity(); return; } const item = field(input.dataset.fieldNumber!) as unknown as Record<string, unknown>; item[input.dataset.numberKey!] = input.value === '' ? undefined : input.valueAsNumber; changed(); }));
  document.querySelectorAll<HTMLSelectElement>('[data-field-kind]').forEach(input => input.addEventListener('change', () => { changeFieldKind(field(input.dataset.fieldKind!), input.value); changed(true); }));
  document.querySelectorAll<HTMLInputElement>('[data-field-required]').forEach(input => input.addEventListener('change', () => { const item = field(input.dataset.fieldRequired!); item.required = input.checked; if (item.kind === 'many-to-one' || item.kind === 'one-to-one') { item.deleteBehavior = input.checked ? 'restrict' : 'set-null'; if (item.targetDefinitionClass) ensureReferenceVersionRequired(item.targetDefinitionClass, input.checked); } changed(true); }));
  document.querySelectorAll<HTMLInputElement>('[data-field-primary]').forEach(input => input.addEventListener('change', () => { const item = field(input.dataset.fieldPrimary!); item.primary = input.checked; if (input.checked) item.required = true; changed(true); }));
  document.querySelectorAll<HTMLInputElement>('[data-field-api]').forEach(input => input.addEventListener('change', () => { const item = field(input.dataset.fieldApi!); if (item.kind === 'one-to-many' || item.kind === 'many-to-many' || item.usesExistingColumn) item.associationApiAware = input.checked; else item.apiAware = input.checked; changed(); }));
  document.querySelectorAll<HTMLInputElement>('[data-association-api]').forEach(input => input.addEventListener('change', () => { field(input.dataset.associationApi!).associationApiAware = input.checked; changed(); }));
  document.querySelectorAll<HTMLInputElement>('[data-existing-column]').forEach(input => input.addEventListener('change', () => { const item = field(input.dataset.existingColumn!); item.usesExistingColumn = input.checked; if (input.checked) item.required = false; changed(true); }));
  document.querySelectorAll<HTMLSelectElement>('[data-delete]').forEach(input => input.addEventListener('change', () => { field(input.dataset.delete!).deleteBehavior = input.value; changed(); }));
  document.querySelectorAll<HTMLElement>('[data-select-field]').forEach(button => button.addEventListener('click', () => { selectedFieldId = button.dataset.selectField!; persistState(); render(); }));
  document.querySelectorAll<HTMLElement>('[data-remove]').forEach(button => button.addEventListener('click', () => { const removed = button.dataset.remove!; spec!.fields = spec!.fields.filter(item => item.id !== removed); if (selectedFieldId === removed) selectedFieldId = spec!.fields[0]?.id ?? ''; changed(true); }));
  document.querySelectorAll<HTMLElement>('[data-up],[data-down]').forEach(button => button.addEventListener('click', () => { const id = button.dataset.up ?? button.dataset.down!; const index = spec!.fields.findIndex(item => item.id === id); const target = button.dataset.up ? index - 1 : index + 1; [spec!.fields[index], spec!.fields[target]] = [spec!.fields[target], spec!.fields[index]]; changed(true); }));
  document.querySelectorAll<HTMLElement>('[data-relation]').forEach(button => button.addEventListener('click', () => openRelationDialog(button.dataset.relation!)));
  document.querySelectorAll<HTMLElement>('[data-mapping]').forEach(button => button.addEventListener('click', () => openRelationDialog(button.dataset.mapping!, 'mapping')));
}

function addField(kind: string): void {
  const id = `${kind}-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
  const item: Field = {id, kind, propertyName: '', storageName: '', editable: true};
  applyFieldDefaults(item); spec!.fields.push(item); selectedFieldId = item.id; changed(true);
}

function changeFieldKind(item: Field, kind: string): void {
  const preserved = {id: item.id, kind, propertyName: item.propertyName, storageName: item.storageName, required: item.required, primary: item.primary, apiAware: item.apiAware, editable: item.editable};
  for (const key of Object.keys(item) as (keyof Field)[]) delete item[key];
  Object.assign(item, preserved);
  applyFieldDefaults(item);
}

function applyFieldDefaults(item: Field): void {
  if (item.kind === 'created-at') Object.assign(item, {propertyName: 'createdAt', storageName: 'created_at', required: true, migrationDefault: 'CURRENT_TIMESTAMP(3)'});
  if (item.kind === 'updated-at') Object.assign(item, {propertyName: 'updatedAt', storageName: 'updated_at', required: false});
  if (item.kind === 'auto-increment') Object.assign(item, {propertyName: 'autoIncrement', storageName: 'auto_increment', required: true});
  if (item.kind === 'version') Object.assign(item, {propertyName: 'versionId', storageName: 'version_id', required: true, primary: true});
  if (item.kind === 'string' && !item.maxLength) item.maxLength = 255;
  if (item.kind === 'many-to-one' || item.kind === 'one-to-one') Object.assign(item, {referenceField: 'id', referenceStorageName: 'id', deleteBehavior: item.required ? 'restrict' : 'set-null'});
  if (item.kind === 'one-to-many') Object.assign(item, {storageName: undefined, sourceColumn: 'id', deleteBehavior: 'restrict'});
  if (item.kind === 'many-to-many') Object.assign(item, {storageName: undefined, referenceField: 'id', sourceColumn: 'id'});
}

function openRelationDialog(fieldId: string, role: 'target' | 'mapping' = 'target'): void {
  relationField = fieldId; relationRole = role; relationResults = bootstrap?.existing ?? []; renderRelationDialog();
}

function renderRelationDialog(): void {
  const dialog = document.getElementById('relationDialog') as HTMLDialogElement | null;
  const content = document.getElementById('relationContent');
  if (!dialog || !content) return;
  content.innerHTML = `<div class="toolbar"><h2 style="margin-right:auto">${relationRole === 'mapping' ? 'Mapping definition' : 'Relation target'}</h2><input id="relationQuery" placeholder="Search technical name or PHP class"><button id="relationSearch" ${relationBusy ? 'disabled' : ''}>${relationBusy ? 'Searching…' : 'Search'}</button><button class="secondary" id="relationClose">Close</button></div><div>${relationResults.map((target, index) => `<button class="secondary relation-result" data-target="${index}"><b>${esc(target.entityName)}</b><br><span class="muted">${esc(target.definitionClass)}</span></button>`).join('') || '<p class="muted">No indexed entities found.</p>'}</div>`;
  const search = (): void => { const query = (content.querySelector('#relationQuery') as HTMLInputElement | null)?.value ?? ''; relationBusy = true; relationRequestId++; renderRelationDialog(); vscode.postMessage({type: 'search', requestId: relationRequestId, query}); };
  content.querySelector('#relationSearch')?.addEventListener('click', search);
  content.querySelector('#relationQuery')?.addEventListener('keydown', event => { if ((event as KeyboardEvent).key === 'Enter') search(); });
  content.querySelector('#relationClose')?.addEventListener('click', () => dialog.close());
  content.querySelectorAll<HTMLElement>('[data-target]').forEach(button => button.addEventListener('click', () => {
    const target = relationResults[Number(button.dataset.target)]; const item = spec!.fields.find(field => field.id === relationField)!;
    if (relationRole === 'mapping') {
      item.mappingDefinitionClass = target.definitionClass;
      dialog.close(); changed(true); return;
    }
    const primary = target.fields?.find(field => field.primary);
    Object.assign(item, {targetDefinitionClass: target.definitionClass, targetEntityClass: target.entityClass, targetCollectionClass: target.collectionClass, targetEntityName: target.entityName, referenceField: primary?.propertyName || 'id', referenceStorageName: primary?.storageName || 'id'});
    if (item.kind === 'reference-version') Object.assign(item, {propertyName: `${camel(target.entityName)}VersionId`, storageName: `${target.entityName}_version_id`});
    if ((item.kind === 'many-to-one' || item.kind === 'one-to-one') && !item.propertyName) item.propertyName = camel(target.entityName);
    if (item.kind === 'many-to-one' || item.kind === 'one-to-one') { item.foreignKeyPropertyName ||= `${item.propertyName}Id`; item.storageName ||= `${snake(item.propertyName)}_id`; }
    if ((item.kind === 'many-to-one' || item.kind === 'one-to-one') && !item.usesExistingColumn && target.versionAware) ensureReferenceVersion(target, item.required ?? false);
    dialog.close(); changed(true);
  }));
  if (!dialog.open) dialog.showModal();
}

function isRelation(kind: string): boolean { return ['reference-version', 'many-to-one', 'one-to-one', 'one-to-many', 'many-to-many'].includes(kind); }

function ensureReferenceVersion(target: Target, required: boolean): void {
  const existing = spec!.fields.find(item => item.kind === 'reference-version' && item.targetDefinitionClass === target.definitionClass);
  if (existing) { if (required) existing.required = true; return; }
  spec!.fields.push({id: `reference-version-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`, kind: 'reference-version', propertyName: `${camel(target.entityName)}VersionId`, storageName: `${target.entityName}_version_id`, targetDefinitionClass: target.definitionClass, targetEntityClass: target.entityClass, targetCollectionClass: target.collectionClass, targetEntityName: target.entityName, required, editable: true});
}

function ensureReferenceVersionRequired(targetDefinitionClass: string, required: boolean): void {
  if (!required) return;
  const version = spec!.fields.find(item => item.kind === 'reference-version' && item.targetDefinitionClass === targetDefinitionClass);
  if (version) version.required = true;
}

function storedColumns(): string[] {
  if (!spec) return [];
  return Array.from(new Set(spec.fields.filter(field => !isNonStored(field) && field.kind !== 'locked' && Boolean(field.storageName)).map(field => field.storageName!)));
}

function targetByClass(className?: string): Target | undefined {
  if (!className) return undefined;
  return knownTargets.find(target => target.definitionClass === className);
}

function mergeTargets(left: Target[], right: Target[]): Target[] {
  const merged = new Map<string, Target>();
  for (const target of [...left, ...right]) merged.set(target.definitionClass, target);
  return Array.from(merged.values()).sort((a, b) => a.entityName.localeCompare(b.entityName));
}

function renameDecisionValue(entity: string, added: string): string {
  const decision = decisions.find(item => item.entity === entity && item.to === added);
  return decision?.kind === 'columnCreate' ? 'create' : decision?.from ?? '';
}

function isNonStored(field: Field): boolean { return field.kind === 'one-to-many' || field.kind === 'many-to-many' || (field.kind === 'one-to-one' && Boolean(field.usesExistingColumn)); }

function persistState(applied = false): void { vscode.setState({spec, decisions, driftDecision, selectedFieldId, applied} satisfies PersistedState); }
function changed(rerender = false): void { preview = undefined; appliedRevision = ''; destructiveConfirmed = false; errorMessage = ''; successMessage = ''; if (rerender) render(); schedulePreview(); persistState(); }
function schedulePreview(): void { if (previewTimer) window.clearTimeout(previewTimer); previewTimer = window.setTimeout(requestPreview, 350); }
function requestPreview(): void { if (!spec) return; if (previewTimer) { window.clearTimeout(previewTimer); previewTimer = undefined; } if (previewBusy) { previewQueued = true; return; } previewBusy = true; previewRequestId++; vscode.postMessage({type: 'preview', requestId: previewRequestId, spec, decisions, driftDecision: driftDecision || undefined}); }
function shortUri(uri: string): string { try { return decodeURIComponent(new URL(uri).pathname).split('/').slice(-4).join('/'); } catch { return uri; } }
function esc(value: unknown): string { return String(value ?? '').replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]!)); }
function attr(value: unknown): string { return esc(value); }
function snake(value: string): string { return value.replace(/([a-z0-9])([A-Z])/g, '$1_$2').replace(/[- ]+/g, '_').toLowerCase(); }
function camel(value: string): string { return value.replace(/_([a-z])/g, (_, letter: string) => letter.toUpperCase()); }
function toDataset(attribute: string): string { return attribute.replace(/-([a-z])/g, (_, letter: string) => letter.toUpperCase()); }
function cssEscape(value: string): string { return CSS.escape(value); }

vscode.postMessage({type: 'ready'});
