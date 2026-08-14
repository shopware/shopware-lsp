declare function acquireVsCodeApi(): {
  postMessage(message: unknown): void;
  setState(state: unknown): void;
  getState(): unknown;
};

type Behavior = {runtime?: boolean; runtimeDependencies?: string[]; runtimeDependenciesExpression?: string; computed?: boolean; noConstraint?: boolean};
type Implementation = {class: string; constructorMode: 'storage-property' | 'fixed'; additionalArguments?: string[]; fixedArguments?: string[]; fixedStorageName?: string; fixedPropertyName?: string; entityType?: string; entityBooleanGetter?: boolean; entityTrait?: string; manageEntity?: boolean; implicitComputed?: boolean; maxLengthArgument?: boolean; minimumAdditionalArguments?: number};
type Metadata = {allowHtmlSanitized?: boolean; allowEmptyString?: boolean; asArray?: boolean; immutable?: boolean; since?: string; deprecated?: {deprecatedSince: string; willBeRemovedIn: string; replacedBy?: string}; ignoreInOpenapiSchema?: boolean; ignoreInUnusedMediaSearch?: boolean; apiCriteriaAware?: boolean; ruleAreas?: string[]; choice?: {values: string[]; strict?: boolean}; doNotUseContext?: boolean; extension?: boolean};
type FlagKind = 'required' | 'primary-key' | 'api-aware' | 'search-ranking' | 'cascade-delete' | 'set-null-on-delete' | 'restrict-delete' | 'runtime' | 'computed' | 'no-constraint' | 'inherited' | 'reverse-inherited' | 'write-protected' | 'allow-html' | 'allow-empty-string' | 'as-array' | 'immutable' | 'since' | 'deprecated' | 'ignore-in-openapi-schema' | 'ignore-in-unused-media-search' | 'api-criteria-aware' | 'rule-areas' | 'choice' | 'do-not-use-context' | 'extension';
type FlagSpec = {kind: FlagKind; apiSources?: string[]; searchRanking?: number; searchTokenize?: boolean; runtimeDependencies?: string[]; runtimeDependenciesExpression?: string; inheritedForeignKey?: string; reverseProperty?: string; writeScopes?: string[]; allowHtmlSanitized?: boolean; cloneRelevant?: boolean; enforcedByConstraint?: boolean; since?: string; deprecated?: {deprecatedSince: string; willBeRemovedIn: string; replacedBy?: string}; ruleAreas?: string[]; choice?: {values: string[]; strict?: boolean}};
type FieldModification = {id: string; propertyName: string; addFlags?: FlagSpec[]; removeFlags?: FlagKind[]};
type DefinitionKind = 'entity' | 'mapping' | 'extension' | 'bulk-extension';
type DefinitionDefault = {propertyName: string; valueExpression: string};
type DefinitionMetadata = {since?: string; defaults?: DefinitionDefault[]; childDefaults?: DefinitionDefault[]; hydratorClass?: string; sinceMethodRaw?: string; defaultsMethodRaw?: string; childDefaultsMethodRaw?: string; hydratorMethodRaw?: string};

type Field = {
  id: string; kind: string; propertyName: string; foreignKeyPropertyName?: string;
  storageName?: string; required?: boolean; primary?: boolean; apiAware?: boolean; apiAwareSources?: string[]; searchRanking?: number; searchRankingTokenize?: boolean; behavior?: Behavior; implementation?: Implementation; metadata?: Metadata; maxLength?: number;
  writeProtected?: boolean; writeProtectedScopes?: string[];
  inherited?: boolean; inheritedForeignKey?: string;
  preservedFlags?: string[]; modifiersBeforeFlags?: string[]; modifiersAfterFlags?: string[];
  associationFlags?: string[]; associationModifiersBeforeFlags?: string[]; associationModifiersAfterFlags?: string[];
  min?: number; max?: number; elementTypeClass?: string; jsonPropertyMappingExpression?: string; jsonDefaultExpression?: string;
  enumClass?: string; enumCase?: string; enumBackingType?: 'string' | 'int';
  targetDefinitionClass?: string; targetEntityClass?: string; targetCollectionClass?: string;
  targetEntityName?: string; referenceField?: string; referenceStorageName?: string;
  mappingDefinitionClass?: string; mappingLocalColumn?: string; mappingReferenceColumn?: string;
  sourceColumn?: string; usesExistingColumn?: boolean; deleteBehavior?: string; deleteCloneRelevant?: boolean; deleteEnforcedByConstraint?: boolean; associationApiAware?: boolean; associationApiAwareSources?: string[]; associationAutoload?: boolean; conditionalAssociation?: {conditionExpression: string; alternativeKind: 'many-to-one' | 'one-to-one'; alternativeAutoload?: boolean};
  associationSearchRanking?: number; associationSearchRankingTokenize?: boolean; associationBehavior?: Behavior; associationMetadata?: Metadata; associationWriteProtected?: boolean; associationWriteProtectedScopes?: string[];
  associationInherited?: boolean; associationInheritedForeignKey?: string; reverseInheritedProperty?: string;
  migrationDefault?: string; translated?: boolean; translationDefinitionOnly?: boolean; translationUseForSorting?: boolean;
  hierarchyParentProperty?: string; hierarchyChildrenFlags?: string[]; hierarchyChildrenModifiersBeforeFlags?: string[]; hierarchyChildrenModifiersAfterFlags?: string[];
  hierarchyChildrenApiAware?: boolean; hierarchyChildrenApiAwareSources?: string[]; hierarchyChildrenSearchRanking?: number; hierarchyChildrenSearchRankingTokenize?: boolean; hierarchyChildrenBehavior?: Behavior; hierarchyChildrenMetadata?: Metadata; hierarchyChildrenWriteProtected?: boolean; hierarchyChildrenWriteProtectedScopes?: string[]; hierarchyChildrenInherited?: boolean; hierarchyChildrenInheritedForeignKey?: string; hierarchyChildrenReverseInheritedProperty?: string;
  hierarchyVersionAware?: boolean; hierarchyVersionApiAware?: boolean; hierarchyVersionApiAwareSources?: string[]; hierarchyVersionWriteProtected?: boolean; hierarchyVersionWriteProtectedScopes?: string[]; hierarchyVersionInherited?: boolean; hierarchyVersionInheritedForeignKey?: string;
  hierarchyVersionFlags?: string[]; hierarchyVersionBehavior?: Behavior; hierarchyVersionMetadata?: Metadata; hierarchyVersionModifiersBeforeFlags?: string[]; hierarchyVersionModifiersAfterFlags?: string[];
  translationApiAware?: boolean; translationApiAwareSources?: string[]; translationSearchRanking?: number; translationSearchRankingTokenize?: boolean; translationBehavior?: Behavior; translationMetadata?: Metadata; translationWriteProtected?: boolean; translationWriteProtectedScopes?: string[]; translationInherited?: boolean; translationInheritedForeignKey?: string; translationFlags?: string[];
  translationModifiersBeforeFlags?: string[]; translationModifiersAfterFlags?: string[]; editable: boolean; raw?: string;
};
type DefinitionBehavior = {
	parentDefinitionClass?: string; versionAware?: boolean; overrideDefaultFields?: boolean; defaultFields?: Field[]; overrideBaseFields?: boolean; baseFields?: Field[]; restrictDeleteMetaProperties?: string[];
	parentDefinitionMethodRaw?: string; versionAwareMethodRaw?: string; inheritanceAwareMethodRaw?: string; defaultFieldsMethodRaw?: string; baseFieldsMethodRaw?: string; restrictDeleteMetaMethodRaw?: string;
};
type Translation = {
  enabled: boolean; entityName?: string; definitionClass?: string; entityClass?: string; collectionClass?: string;
  definitionUri?: string; entityUri?: string; collectionUri?: string; parentDefinitionClass?: string;
  parentStorageName?: string; parentPropertyName?: string; associationProperty?: string; associationLocalField?: string;
  associationRequired?: boolean; associationApiAware?: boolean; associationApiAwareSources?: string[]; associationBehavior?: Behavior; associationMetadata?: Metadata; associationWriteProtected?: boolean; associationWriteProtectedScopes?: string[]; associationFlags?: string[];
  associationInherited?: boolean; associationInheritedForeignKey?: string; reverseInheritedProperty?: string;
  associationModifiersBeforeFlags?: string[]; associationModifiersAfterFlags?: string[];
  definitionBehavior?: DefinitionBehavior; definitionMetadata?: DefinitionMetadata;
};
type IndexSpec = {name: string; kind: string; columns: string[]; translation?: boolean};
type BulkExtensionTarget = {
  id: string; entityName: string; extendedDefinitionClass?: string;
  extendedFields?: {storageName: string; propertyName: string; primary?: boolean}[];
  fields: Field[]; indexes?: IndexSpec[];
};
type Spec = {
  mode: string; definitionKind?: DefinitionKind; pluginRootUri: string; directoryUri: string; namespace: string;
  className: string; entityName: string; definitionClass?: string; entityClass?: string;
  extendedDefinitionClass?: string; extendedFields?: {storageName: string; propertyName: string; primary?: boolean}[]; inheritanceAware?: boolean; definitionBehavior?: DefinitionBehavior; definitionMetadata?: DefinitionMetadata; readProtected?: boolean; readProtectionScopes?: string[]; writeProtected?: boolean; writeProtectionScopes?: string[]; preservedProtections?: string[]; protectionMethodRaw?: string; fieldModifications?: FieldModification[]; modifyFieldsMethodRaw?: string; collectMethodRaw?: string; shopwareVersion?: string; definitionUri?: string;
  collectionClass?: string; entityUri?: string; collectionUri?: string; fields: Field[]; indexes?: IndexSpec[]; translation?: Translation; bulkExtensions?: BulkExtensionTarget[];
  serviceUri?: string; createMigration: boolean; migrationName?: string; migrationTimestamp?: number;
};
type Bootstrap = {
  plugin: {composerName: string; shopwareVersion?: string; serviceUris?: string[]};
  spec: Spec; definitionKinds?: DefinitionKind[]; fieldTypes: {id?: string; kind: string; label: string; stored: boolean; definitionKinds?: DefinitionKind[]; requiresDefaultFieldsOverride?: boolean; template?: Field}[];
  graph: {snapshotCount: number; leaves?: string[]; needsReconciliation: boolean};
  existing?: Target[];
  editable?: {entityName: string; definitionClass: string; definitionKind: DefinitionKind; fileUri: string}[];
};
type Target = {entityName: string; definitionClass: string; definitionKind?: DefinitionKind; entityClass?: string; collectionClass?: string; fileUri?: string; versionAware?: boolean; inheritanceAware?: boolean; fields?: {storageName: string; propertyName: string; primary?: boolean}[]};
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
  entityRenameQuestions?: {added: string; candidates: {from: string; score: number}[]}[];
  addedIndexes?: {entity: string; index: {name: string; unique: boolean; columns: string[]}}[];
  removedIndexes?: {entity: string; index: {name: string; unique: boolean; columns: string[]}}[];
  addedForeignKeys?: {entity: string; foreignKey: {name: string}}[];
  removedForeignKeys?: {entity: string; foreignKey: {name: string}}[];
  changedPrimaryKeys?: {entity: string; before?: string[]; after?: string[]}[];
};
type PersistedState = {spec?: Spec; decisions?: Decision[]; driftDecision?: string; selectedFieldId?: string; selectedBulkTargetId?: string; applied?: boolean};

const fieldFlagKinds = ([
  ['required', 'Required'], ['primary-key', 'Primary key'], ['api-aware', 'API aware'], ['search-ranking', 'Search ranking'],
  ['cascade-delete', 'Cascade delete'], ['set-null-on-delete', 'Set null on delete'], ['restrict-delete', 'Restrict delete'],
  ['runtime', 'Runtime'], ['computed', 'Computed'], ['no-constraint', 'No constraint'], ['inherited', 'Inherited'],
  ['reverse-inherited', 'Reverse inherited'], ['write-protected', 'Write protected'], ['allow-html', 'Allow HTML'],
  ['allow-empty-string', 'Allow empty string'], ['as-array', 'As array'], ['immutable', 'Immutable'], ['since', 'Since'],
  ['deprecated', 'Deprecated'], ['ignore-in-openapi-schema', 'Ignore in OpenAPI'],
  ['ignore-in-unused-media-search', 'Ignore in unused-media search'], ['api-criteria-aware', 'API criteria aware'],
  ['rule-areas', 'Rule areas'], ['choice', 'Choice'], ['do-not-use-context', 'Do not use context'], ['extension', 'Extension'],
] as [FlagKind, string][]).map(([kind, label]) => ({kind, label}));

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
let selectedBulkTargetId = '';
let destructiveConfirmed = false;
let appliedRevision = '';
let previewTimer: number | undefined;
let previewRequestId = 0;
let previewQueued = false;
let relationRequestId = 0;
let relationField = '';
let relationRole: 'target' | 'mapping' | 'extension' | 'bulk-extension-add' | 'bulk-extension-change' = 'target';
let relationResults: Target[] = [];
let knownTargets: Target[] = [];

function selectedBulkTarget(): BulkExtensionTarget | undefined {
	if (spec?.definitionKind !== 'bulk-extension') return undefined;
	const targets = spec.bulkExtensions ?? [];
	return targets.find(target => target.id === selectedBulkTargetId) ?? targets[0];
}

function activeFields(): Field[] {
	return spec?.definitionKind === 'bulk-extension' ? selectedBulkTarget()?.fields ?? [] : spec?.fields ?? [];
}

function replaceActiveFields(fields: Field[]): void {
	const target = selectedBulkTarget();
	if (spec?.definitionKind === 'bulk-extension' && target) target.fields = fields;
	else if (spec) spec.fields = fields;
}

function activeIndexes(create = false): IndexSpec[] {
	if (!spec) return [];
	if (spec.definitionKind === 'bulk-extension') {
		const target = selectedBulkTarget();
		if (!target) return [];
		if (create) target.indexes ??= [];
		return target.indexes ?? [];
	}
	if (create) spec.indexes ??= [];
	return spec.indexes ?? [];
}

function activeExtendedFields(): {storageName: string; propertyName: string; primary?: boolean}[] {
	return spec?.definitionKind === 'bulk-extension' ? selectedBulkTarget()?.extendedFields ?? [] : spec?.extendedFields ?? [];
}

function activeEntityName(): string {
	return spec?.definitionKind === 'bulk-extension' ? selectedBulkTarget()?.entityName ?? '' : spec?.entityName ?? '';
}

function selectInitialBulkTarget(): void {
	const targets = spec?.bulkExtensions ?? [];
	if (!targets.some(target => target.id === selectedBulkTargetId)) selectedBulkTargetId = targets[0]?.id ?? '';
	const fields = activeFields();
	if (!fields.some(field => field.id === selectedFieldId)) selectedFieldId = fields[0]?.id ?? '';
}

window.addEventListener('message', event => {
  const message = event.data;
  switch (message.type) {
    case 'bootstrap':
      bootstrap = message.value as Bootstrap;
      spec = structuredClone(bootstrap.spec);
      previewRequestId++; preview = undefined; decisions = []; driftDecision = ''; previewBusy = false; actionBusy = false; errorMessage = ''; successMessage = ''; appliedRevision = '';
		selectedBulkTargetId = spec.bulkExtensions?.[0]?.id ?? '';
		selectedFieldId = activeFields()[0]?.id ?? '';
      if (!restoredState?.applied && restoredState?.spec?.directoryUri === spec.directoryUri && restoredState.spec.pluginRootUri === spec.pluginRootUri) {
        spec = structuredClone(restoredState.spec);
        decisions = structuredClone(restoredState.decisions ?? []);
        driftDecision = restoredState.driftDecision ?? '';
		selectedBulkTargetId = restoredState.selectedBulkTargetId ?? selectedBulkTargetId;
		selectInitialBulkTarget();
		selectedFieldId = restoredState.selectedFieldId && activeFields().some(field => field.id === restoredState!.selectedFieldId) ? restoredState.selectedFieldId : selectedFieldId;
      }
      restoredState = undefined;
      knownTargets = bootstrap.existing ?? []; relationResults = knownTargets;
      render(); schedulePreview();
      break;
    case 'loaded':
      spec = structuredClone(message.value as Spec);
      previewRequestId++; preview = undefined; decisions = []; driftDecision = ''; previewBusy = false; actionBusy = false; errorMessage = ''; successMessage = ''; appliedRevision = '';
		selectedBulkTargetId = spec.bulkExtensions?.[0]?.id ?? '';
		selectedFieldId = activeFields()[0]?.id ?? '';
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
  const editable = (bootstrap.editable ?? []).slice(0, 100);
  const fields = activeFields();
  const indexes = activeIndexes();
  const selectedField = fields.find(field => field.id === selectedFieldId);
  const definitionKind = spec.definitionKind ?? 'entity';
  const fieldTypes = availableFieldTypes();
	const definitionKinds = availableDefinitionKinds();
	const customBulkCollect = definitionKind === 'bulk-extension' && Boolean(spec.collectMethodRaw);
  const customDefinitionBehavior = definitionHasLockedMethods(spec.definitionBehavior, spec.definitionMetadata) || definitionHasLockedMethods(spec.translation?.definitionBehavior, spec.translation?.definitionMetadata);
  const customInheritance = Boolean(spec.definitionBehavior?.inheritanceAwareMethodRaw);
  const working = previewBusy || actionBusy;
  const applyDisabled = working || hasErrors || !preview?.revision || appliedRevision === preview.revision || Boolean(preview?.destructive && !destructiveConfirmed);
  app.innerHTML = `
    <div id="status" class="toolbar"><h1 style="margin:0">Entity Designer</h1><span class="badge">${esc(bootstrap.plugin.composerName)}</span>${bootstrap.plugin.shopwareVersion ? `<span class="muted">Shopware ${esc(bootstrap.plugin.shopwareVersion)}</span>` : ''}<span style="flex:1"></span><span class="muted">${working ? 'Working…' : preview?.snapshotId ? `snapshot ${esc(preview.snapshotId.slice(0, 12))}` : ''}</span></div>
    ${errorMessage ? `<div class="card error" role="alert">${esc(errorMessage)}</div>` : ''}
    ${successMessage ? `<div class="card success" role="status">${esc(successMessage)}</div>` : ''}
    ${bootstrap.graph.needsReconciliation ? `<div class="card danger"><b>Snapshot branches need reconciliation.</b><p>Select the authoritative leaf when branches differ.</p><div class="row"><select id="leaf">${(bootstrap.graph.leaves ?? []).map(id => `<option value="${attr(id)}">${esc(id.slice(0, 16))}</option>`).join('')}</select><button id="reconcile">Create merge snapshot</button></div></div>` : ''}
	<section class="card"><div class="row"><h2 style="margin-right:auto">Identity</h2>${editable.length ? `<select id="loadExisting"><option value="">Edit a plugin DAL class…</option>${editable.map(item => `<option value="${attr(item.definitionClass)}">${esc(item.entityName || item.definitionClass.split('\\').pop() || item.definitionClass)} — ${esc(item.definitionKind)}</option>`).join('')}</select>` : ''}</div>
	  <div class="grid"><label>Definition type<select id="definitionKind" ${customBulkCollect || customDefinitionBehavior ? 'disabled' : ''}>${definitionKinds.map(kind => `<option value="${kind}" ${definitionKind === kind ? 'selected' : ''}>${definitionKindLabel(kind)}</option>`).join('')}</select></label><label>PHP base class<input data-spec="className" value="${attr(spec.className)}" ${spec.mode === 'edit' ? 'disabled' : ''}></label>${definitionKind === 'extension' ? `<label>Extended entity<button type="button" class="secondary" id="extensionTarget">${esc(spec.entityName && spec.extendedDefinitionClass ? `${spec.entityName} — ${spec.extendedDefinitionClass}` : 'Choose indexed entity…')}</button></label>` : definitionKind === 'bulk-extension' ? `<label>Extended entities<span>${spec.bulkExtensions?.length ?? 0} selected targets</span></label>` : `<label>Technical entity name<input data-spec="entityName" value="${attr(spec.entityName)}"></label>`}<label>Namespace<input data-spec="namespace" value="${attr(spec.namespace)}" ${spec.mode === 'edit' ? 'disabled' : ''}></label>${definitionKind === 'entity' ? `<label class="compact"><input type="checkbox" data-inheritance-aware ${spec.inheritanceAware ? 'checked' : ''} ${customInheritance ? 'disabled' : ''}><span>Inheritance-aware definition</span></label>` : ''}</div>
	  <p class="muted">${spec.mode === 'edit' ? `Existing PHP class and file identity remain fixed. Definition type, technical table identity, and extension target changes require explicit preview decisions when their database meaning is ambiguous. Custom members are preserved.` : definitionKind === 'mapping' ? 'Creates a MappingEntityDefinition, service registration, join table migration, and committed schema snapshot. Mapping definitions have no entity, collection, or implicit timestamps.' : definitionKind === 'extension' ? 'Creates an EntityExtension for an indexed entity. To-one relations add their paired foreign-key columns; scalar additions are Runtime because Shopware rejects arbitrary persisted scalar extension fields.' : definitionKind === 'bulk-extension' ? 'Creates one BulkEntityExtension service that yields fields for multiple indexed entities. Each target owns independent fields, indexes, and migration changes.' : 'Creates a definition, entity, collection, optional translation bundle, service registrations, migration, and committed schema snapshot.'}</p>
	  ${definitionKind === 'entity' || definitionKind === 'extension' ? entityProtectionControls() : ''}
    </section>
	${definitionKind === 'entity' || definitionKind === 'mapping' ? definitionClassControls(false) : ''}
	${definitionKind === 'entity' && spec.translation?.enabled ? definitionClassControls(true) : ''}
	${definitionKind === 'bulk-extension' ? bulkExtensionControls() : ''}
	<section class="card"><div class="toolbar"><h2 style="margin-right:auto">${definitionKind === 'bulk-extension' ? `Fields for ${esc(activeEntityName() || 'selected target')}` : 'Fields'}</h2><select id="newKind">${fieldTypes.filter(type => type.kind !== 'id').map(type => `<option value="${attr(fieldTypeValue(type))}">${esc(type.label)}</option>`).join('')}</select><button id="addField" ${definitionKind === 'bulk-extension' && !selectedBulkTarget() ? 'disabled' : ''}>Add field</button></div>
      <div class="field-list">
        <div class="field field-header muted"><span></span><span>Type</span><span>Property</span><span>Storage / relation</span><span>Required</span><span>Primary</span><span>API</span><span>Actions</span></div>
		${fields.map((field, index) => fieldRow(field, index, issues.filter(issue => issue.fieldId === field.id))).join('')}
      </div>
      ${selectedField ? fieldInspector(selectedField, issues.filter(issue => issue.fieldId === selectedField.id)) : '<p class="muted">Select a field to edit its advanced settings.</p>'}
    </section>
    ${definitionKind === 'extension' ? fieldModificationControls() : ''}
	<section class="card"><div class="toolbar"><h2 style="margin-right:auto">Indexes</h2><button id="addIndex" ${definitionKind === 'bulk-extension' && !selectedBulkTarget() ? 'disabled' : ''}>Add index</button></div>${indexes.map(indexRow).join('') || '<p class="muted">No custom indexes.</p>'}</section>
    <section class="card"><h2>Migration</h2><div class="grid"><label>Name suffix<input data-spec="migrationName" value="${attr(spec.migrationName ?? '')}" placeholder="UpdateExample"></label><label>Service configuration<select data-spec="serviceUri"><option value="">Create services.yaml</option>${(bootstrap.plugin.serviceUris ?? []).map(uri => `<option value="${attr(uri)}" ${uri === spec!.serviceUri ? 'selected' : ''}>${esc(uri.split('/').pop() ?? uri)}</option>`).join('')}</select></label><label><input type="checkbox" data-bool-spec="createMigration" ${spec.createMigration ? 'checked' : ''}> Generate migration when DB changes</label></div><p class="muted">Drops and other destructive SQL are intentionally generated in <code>update()</code>. No <code>updateDestructive()</code> method is used.</p></section>
    ${preview?.drift ? `<section class="card danger"><h2>Manual schema drift</h2><p>${esc(preview.driftMessage ?? '')}</p><div class="row"><button data-drift="adopt">Adopt current code as baseline</button><button data-drift="migrate">Generate migration from last snapshot</button></div></section>` : ''}
    ${(preview?.diff?.entityRenameQuestions ?? []).map(question => `<section class="card danger"><b>Is table ${esc(question.added)} a rename?</b><select data-entity-rename data-entity-rename-added="${attr(question.added)}"><option value="">Choose…</option><option value="create" ${entityRenameDecisionValue(question.added) === 'create' ? 'selected' : ''}>No, create a new table</option>${question.candidates.map(candidate => `<option value="${attr(candidate.from)}" ${entityRenameDecisionValue(question.added) === candidate.from ? 'selected' : ''}>Rename ${esc(candidate.from)} (${candidate.score}% match)</option>`).join('')}</select></section>`).join('')}
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

function definitionHasLockedMethods(behavior?: DefinitionBehavior, metadata?: DefinitionMetadata): boolean {
	return Boolean(behavior?.parentDefinitionMethodRaw || behavior?.versionAwareMethodRaw || behavior?.inheritanceAwareMethodRaw || behavior?.defaultFieldsMethodRaw || behavior?.baseFieldsMethodRaw || behavior?.restrictDeleteMetaMethodRaw ||
		metadata?.sinceMethodRaw || metadata?.defaultsMethodRaw || metadata?.childDefaultsMethodRaw || metadata?.hydratorMethodRaw);
}

function definitionClassControls(translation: boolean): string {
	if (!spec) return '';
	const owner = translation ? spec.translation : spec;
	if (!owner) return '';
	const behavior = owner.definitionBehavior;
	const metadata = owner.definitionMetadata;
	const scope = translation ? 'translation' : 'parent';
	const kind = spec.definitionKind ?? 'entity';
	const parentTargets = bootstrap?.existing ?? [];
	const currentParent = behavior?.parentDefinitionClass ?? '';
	const parentOptions = Array.from(new Map([
		...(currentParent ? [[currentParent, {definitionClass: currentParent, entityName: currentParent.split('\\').pop() ?? currentParent}]] as const : []),
		...parentTargets.map(target => [target.definitionClass, target] as const),
	]).values());
	const versionValue = behavior?.versionAware === undefined ? '' : String(behavior.versionAware);
	const customDefaults = Boolean(behavior?.defaultFields?.length || behavior?.defaultFieldsMethodRaw);
	const customBaseFields = Boolean(behavior?.baseFields?.length || behavior?.baseFieldsMethodRaw);
	const rawMethods = [
		behavior?.parentDefinitionMethodRaw, behavior?.versionAwareMethodRaw, behavior?.inheritanceAwareMethodRaw,
		behavior?.defaultFieldsMethodRaw, behavior?.baseFieldsMethodRaw, behavior?.restrictDeleteMetaMethodRaw,
		metadata?.sinceMethodRaw, metadata?.defaultsMethodRaw, metadata?.childDefaultsMethodRaw, metadata?.hydratorMethodRaw,
	].filter((value): value is string => Boolean(value));
	const defaultFieldSummary = behavior?.defaultFields?.length
		? `<div class="detail-control span-two"><span>Custom default fields</span><code>${behavior.defaultFields.map(field => `${field.kind}:${field.propertyName}`).join(', ')}</code><span class="muted">Imported default fields are preserved and edited as locked class behavior.</span></div>`
		: '';
	const baseFieldSummary = behavior?.baseFields?.length
		? `<div class="detail-control span-two"><span>Custom base fields</span><code>${behavior.baseFields.map(field => `${field.kind}:${field.propertyName}`).join(', ')}</code><span class="muted">These fields participate in generated entities, relation metadata, snapshots, and migrations.</span></div>`
		: '';
	return `<section class="card"><div class="toolbar"><h2 style="margin-right:auto">${translation ? 'Translation definition behavior' : 'Definition behavior'}</h2>${rawMethods.length ? '<span class="badge">custom methods locked</span>' : ''}</div>
		<div class="inspector-grid">
		${!translation ? `<label class="span-two"><span>Aggregate parent definition (optional)</span><select data-definition-parent="${scope}" ${behavior?.parentDefinitionMethodRaw ? 'disabled' : ''}><option value="">No aggregate parent</option>${parentOptions.map(target => `<option value="${attr(target.definitionClass)}" ${target.definitionClass === currentParent ? 'selected' : ''}>${esc(target.entityName)} — ${esc(target.definitionClass)}</option>`).join('')}</select></label>` : ''}
		<label><span>Version-awareness</span><select data-definition-version-aware="${scope}" ${behavior?.versionAwareMethodRaw ? 'disabled' : ''}><option value="" ${versionValue === '' ? 'selected' : ''}>Infer from fields/framework</option><option value="true" ${versionValue === 'true' ? 'selected' : ''}>Explicitly version-aware</option><option value="false" ${versionValue === 'false' ? 'selected' : ''}>Explicitly not version-aware</option></select></label>
		<label><span>Available since</span><input data-definition-metadata-text="${scope}:since" value="${attr(metadata?.since ?? '')}" placeholder="6.7.1.0" ${metadata?.sinceMethodRaw ? 'disabled' : ''}></label>
		${!translation && kind === 'entity' ? `<label><span>Custom hydrator class</span><input data-definition-metadata-text="${scope}:hydratorClass" value="${attr(metadata?.hydratorClass ?? '')}" placeholder="Acme\\…\\EntityHydrator" ${metadata?.hydratorMethodRaw ? 'disabled' : ''}></label>` : ''}
		${kind !== 'mapping' ? `<label class="compact span-two"><input type="checkbox" data-definition-default-fields="${scope}" ${behavior?.overrideDefaultFields ? 'checked' : ''} ${customDefaults ? 'disabled' : ''}><span>Override framework default fields${translation ? '' : ' (empty disables implicit timestamps)'}</span></label>` : ''}
		${defaultFieldSummary}
		${customBaseFields ? baseFieldSummary : ''}
		${!translation ? `<label class="span-two"><span>Restrict-delete metadata properties</span><input data-definition-restrict-properties="${scope}" value="${attr((behavior?.restrictDeleteMetaProperties ?? []).join(', '))}" placeholder="id, fileName" ${behavior?.restrictDeleteMetaMethodRaw ? 'disabled' : ''}></label>` : ''}
		</div>
		${definitionDefaultsControls(scope, 'defaults', 'Write defaults', metadata?.defaults, metadata?.defaultsMethodRaw)}
		${!translation && kind === 'entity' ? definitionDefaultsControls(scope, 'childDefaults', 'Inheritance child defaults', metadata?.childDefaults, metadata?.childDefaultsMethodRaw) : ''}
		${rawMethods.length ? `<details><summary>Preserved custom definition methods (${rawMethods.length})</summary>${rawMethods.map(method => `<pre>${esc(method)}</pre>`).join('')}</details>` : ''}
	</section>`;
}

function definitionDefaultsControls(scope: 'parent' | 'translation', kind: 'defaults' | 'childDefaults', label: string, values: DefinitionDefault[] = [], raw?: string): string {
	if (raw) return `<div class="definition-defaults"><div class="toolbar"><h3>${esc(label)}</h3><span class="muted">Custom method preserved</span></div><pre>${esc(raw)}</pre></div>`;
	return `<div class="definition-defaults"><div class="toolbar"><h3 style="margin-right:auto">${esc(label)}</h3><button class="secondary" data-add-definition-default="${scope}:${kind}">Add default</button></div>
		${values.map((value, index) => `<div class="detail-grid"><label><span>Property</span><input data-definition-default-property="${scope}:${kind}:${index}" value="${attr(value.propertyName)}" placeholder="active"></label><label><span>PHP value expression</span><input data-definition-default-expression="${scope}:${kind}:${index}" value="${attr(value.valueExpression)}" placeholder="true"></label><button class="secondary" data-remove-definition-default="${scope}:${kind}:${index}">Remove</button></div>`).join('') || '<p class="muted">No defaults.</p>'}
	</div>`;
}

function fieldModificationControls(): string {
	if (!spec) return '';
	if (spec.modifyFieldsMethodRaw) {
		return `<section class="card"><h2>Existing field modifications</h2><p class="muted">This custom <code>modifyFields()</code> method is preserved exactly and cannot be mixed with structured edits.</p><pre>${esc(spec.modifyFieldsMethodRaw)}</pre></section>`;
	}
	const modifications = spec.fieldModifications ?? [];
	const targets = (spec.extendedFields ?? []).map(field => field.propertyName);
	const options = (selected: FlagKind[] = []) => fieldFlagKinds.filter(flag => !selected.includes(flag.kind)).map(flag => `<option value="${attr(flag.kind)}">${esc(flag.label)}</option>`).join('');
	return `<section class="card"><div class="toolbar"><h2 style="margin-right:auto">Modify existing target fields</h2><button id="addFieldModification">Add modification</button></div><p class="muted">Adds or removes typed DAL flags through <code>EntityExtension::modifyFields()</code>. Property names may refer to stored fields or associations on the selected target.</p><datalist id="modificationTargets">${targets.map(target => `<option value="${attr(target)}"></option>`).join('')}</datalist>${modifications.map((modification, modificationIndex) => {
		const used = [...(modification.addFlags ?? []).map(flag => flag.kind), ...(modification.removeFlags ?? [])];
		return `<div class="inspector"><div class="toolbar"><label style="flex:1"><span>Target property</span><input list="modificationTargets" data-modification-property="${modificationIndex}" value="${attr(modification.propertyName)}" placeholder="name"></label><button class="secondary" data-remove-modification="${modificationIndex}">Remove</button></div>
			<div class="detail-grid">${(modification.addFlags ?? []).map((flag, flagIndex) => modificationFlagEditor(modificationIndex, flagIndex, flag)).join('')}</div>
			<div class="toolbar"><select data-add-modification-flag-select="${modificationIndex}">${options(used)}</select><button class="secondary" data-add-modification-flag="${modificationIndex}">Add flag</button></div>
			${(modification.removeFlags ?? []).length ? `<div class="toolbar"><span>Remove:</span>${modification.removeFlags!.map((kind, removeIndex) => `<span class="badge">${esc(fieldFlagLabel(kind))} <button class="link" data-remove-removed-flag="${modificationIndex}:${removeIndex}">×</button></span>`).join('')}</div>` : ''}
			<div class="toolbar"><select data-remove-modification-flag-select="${modificationIndex}">${options(used)}</select><button class="secondary" data-add-removed-flag="${modificationIndex}">Remove flag</button></div></div>`;
	}).join('') || '<p class="muted">No target fields are modified.</p>'}</section>`;
}

function bulkExtensionControls(): string {
	if (!spec) return '';
	if (spec.collectMethodRaw) {
		return `<section class="card"><h2>Custom bulk collection</h2><p class="muted">This non-literal <code>collect()</code> implementation is preserved exactly. Its computed targets and schema effects cannot be edited safely in the designer.</p><pre>${esc(spec.collectMethodRaw)}</pre></section>`;
	}
	const targets = spec.bulkExtensions ?? [];
	const selected = selectedBulkTarget();
	return `<section class="card"><div class="toolbar"><h2 style="margin-right:auto">Bulk targets</h2><button id="addBulkTarget">Add indexed target</button></div>
		${targets.length ? `<div class="row"><label style="min-width:360px">Active target<select id="bulkTarget">${targets.map(target => `<option value="${attr(target.id)}" ${target.id === selected?.id ? 'selected' : ''}>${esc(target.entityName)} — ${esc(target.extendedDefinitionClass || 'literal entity name')}</option>`).join('')}</select></label><button class="secondary" id="changeBulkTarget">Change indexed target</button><button class="secondary" id="removeBulkTarget">Remove target</button></div>` : '<p class="muted">Add at least one indexed entity. Fields and indexes are edited independently for the selected target.</p>'}
		<p class="muted">The generated <code>collect()</code> method yields one field list per target. Scalar fields are Runtime; associations and their paired foreign keys follow normal EntityExtension rules.</p></section>`;
}

function modificationFlagEditor(modificationIndex: number, flagIndex: number, flag: FlagSpec): string {
	const key = `${modificationIndex}:${flagIndex}`;
	const input = (field: string, label: string, value: unknown, placeholder = '') => `<label><span>${label}</span><input data-modification-flag="${key}" data-flag-key="${field}" value="${attr(value ?? '')}" placeholder="${attr(placeholder)}"></label>`;
	const check = (field: string, label: string, checked: boolean) => `<label class="compact"><input type="checkbox" data-modification-flag="${key}" data-flag-key="${field}" ${checked ? 'checked' : ''}><span>${label}</span></label>`;
	const controls: string[] = [];
	switch (flag.kind) {
		case 'api-aware': controls.push(input('apiSources', 'API source classes (comma-separated)', (flag.apiSources ?? []).join(', '))); break;
		case 'search-ranking': controls.push(input('searchRanking', 'Ranking', flag.searchRanking ?? 0)); controls.push(check('searchTokenize', 'Tokenize', flag.searchTokenize ?? true)); break;
		case 'runtime': controls.push(input('runtimeDependencies', 'Dependencies (comma-separated)', (flag.runtimeDependencies ?? []).join(', '))); break;
		case 'inherited': controls.push(input('inheritedForeignKey', 'Foreign-key override', flag.inheritedForeignKey)); break;
		case 'reverse-inherited': controls.push(input('reverseProperty', 'Reverse property', flag.reverseProperty)); break;
		case 'write-protected': controls.push(input('writeScopes', 'Allowed scopes (comma-separated)', (flag.writeScopes ?? []).join(', '))); break;
		case 'allow-html': controls.push(check('allowHtmlSanitized', 'Sanitize HTML', flag.allowHtmlSanitized ?? true)); break;
		case 'cascade-delete': controls.push(check('cloneRelevant', 'Clone relevant', flag.cloneRelevant ?? true)); break;
		case 'set-null-on-delete': controls.push(check('enforcedByConstraint', 'Enforced by constraint', flag.enforcedByConstraint ?? false)); break;
		case 'since': controls.push(input('since', 'Available since', flag.since)); break;
		case 'deprecated': controls.push(input('deprecatedSince', 'Deprecated since', flag.deprecated?.deprecatedSince)); controls.push(input('willBeRemovedIn', 'Will be removed in', flag.deprecated?.willBeRemovedIn)); controls.push(input('replacedBy', 'Replacement', flag.deprecated?.replacedBy)); break;
		case 'rule-areas': controls.push(input('ruleAreas', 'Rule areas (comma-separated)', (flag.ruleAreas ?? []).join(', '))); break;
		case 'choice': controls.push(`<label><span>Choices (one PHP expression per line)</span><textarea data-modification-flag="${key}" data-flag-key="choiceValues" rows="3">${esc((flag.choice?.values ?? []).join('\n'))}</textarea></label>`); controls.push(check('choiceStrict', 'Strict choices', flag.choice?.strict ?? false)); break;
	}
	return `<div class="detail-control span-two"><div class="toolbar"><b>${esc(fieldFlagLabel(flag.kind))}</b><button class="link" data-remove-added-flag="${key}">Remove</button></div>${controls.length ? `<div class="detail-grid">${controls.join('')}</div>` : '<span class="muted">No arguments</span>'}</div>`;
}

function fieldFlagLabel(kind: FlagKind): string { return fieldFlagKinds.find(flag => flag.kind === kind)?.label ?? kind; }

function defaultFieldFlag(kind: FlagKind): FlagSpec {
	const flag: FlagSpec = {kind};
	if (kind === 'search-ranking') Object.assign(flag, {searchRanking: 500, searchTokenize: true});
	if (kind === 'reverse-inherited') flag.reverseProperty = 'children';
	if (kind === 'allow-html') flag.allowHtmlSanitized = true;
	if (kind === 'cascade-delete') flag.cloneRelevant = true;
	if (kind === 'set-null-on-delete') flag.enforcedByConstraint = false;
	if (kind === 'since') flag.since = spec?.shopwareVersion?.replace(/^[~^]/, '') || '6.7.0.0';
	if (kind === 'deprecated') flag.deprecated = {deprecatedSince: '6.7.0.0', willBeRemovedIn: '6.8.0.0'};
	if (kind === 'rule-areas') flag.ruleAreas = ['product'];
	if (kind === 'choice') flag.choice = {values: ["'value'"], strict: false};
	return flag;
}

function entityProtectionControls(): string {
	if (!spec) return '';
	if (spec.protectionMethodRaw) return `<details><summary>Custom entity protections (preserved)</summary><pre>${esc(spec.protectionMethodRaw)}</pre></details>`;
	return `<div class="detail-grid"><label class="compact"><input type="checkbox" data-entity-read-protected ${spec.readProtected ? 'checked' : ''}><span>Read-protected entity</span></label>${spec.readProtected ? `<label><span>Allowed read scopes (comma-separated)</span><input data-entity-read-scopes value="${attr((spec.readProtectionScopes ?? []).join(', '))}" placeholder="system"></label>` : ''}<label class="compact"><input type="checkbox" data-entity-write-protected ${spec.writeProtected ? 'checked' : ''}><span>Write-protected entity</span></label>${spec.writeProtected ? `<label><span>Allowed write scopes (comma-separated)</span><input data-entity-write-scopes value="${attr((spec.writeProtectionScopes ?? []).join(', '))}" placeholder="system"></label>` : ''}</div>${spec.preservedProtections?.length ? `<details><summary>Preserved custom protections (${spec.preservedProtections.length})</summary><pre>${esc(spec.preservedProtections.join('\n'))}</pre></details>` : ''}`;
}

function fieldRow(field: Field, index: number, issues: Issue[]): string {
  const locked = !field.editable;
  const relation = isRelation(field.kind);
  const toOne = field.kind === 'foreign-key' || field.kind === 'many-to-one' || field.kind === 'one-to-one';
  const nonStored = isNonStored(field);
  const referenceVersion = field.kind === 'reference-version';
  const fixed = field.kind === 'id' || field.kind === 'auto-increment' || field.kind === 'version' || field.kind === 'hierarchy' || field.implementation?.constructorMode === 'fixed';
  const fixedProperty = fixed && field.kind !== 'hierarchy';
  const target = field.targetEntityName || field.targetDefinitionClass || 'Choose target…';
  const storage = relation
    ? `<button class="secondary" data-relation="${attr(field.id)}">${esc(target)}</button>${toOne || referenceVersion ? `<input data-field-storage="${attr(field.id)}" value="${attr(field.storageName ?? '')}" placeholder="${referenceVersion ? 'target_version_id' : 'foreign_key_id'}">` : field.kind === 'one-to-many' ? `<input data-reference-storage="${attr(field.id)}" value="${attr(field.referenceStorageName ?? '')}" placeholder="target FK column">` : ''}`
    : `<input data-field-storage="${attr(field.id)}" value="${attr(field.storageName ?? '')}" ${locked || fixed ? 'disabled' : ''} placeholder="storage_name">`;
  const fieldTypes = availableFieldTypes(field.kind);
  const removable = field.editable && (field.kind !== 'id' || spec?.definitionKind === 'mapping');
  const selectedType = fieldTypeID(field);
  return `<div class="field ${locked ? 'locked' : ''} ${field.id === selectedFieldId ? 'selected' : ''} ${issues.length ? 'invalid' : ''}" data-field-row="${attr(field.id)}"><span class="muted">${index + 1}</span><select data-field-kind="${attr(field.id)}" ${locked || (field.kind === 'id' && spec?.definitionKind !== 'mapping') ? 'disabled' : ''}>${fieldTypes.map(type => `<option value="${attr(fieldTypeValue(type))}" ${fieldTypeValue(type) === selectedType ? 'selected' : ''}>${esc(type.label)}</option>`).join('')}</select><input data-field-property="${attr(field.id)}" value="${attr(field.propertyName ?? '')}" ${locked || fixedProperty ? 'disabled' : ''} placeholder="${field.kind === 'hierarchy' ? 'children property' : 'propertyName'}"><div class="field-storage">${storage}</div><input type="checkbox" title="Required" aria-label="Required" data-field-required="${attr(field.id)}" ${field.required ? 'checked' : ''} ${locked || fixed || nonStored || field.usesExistingColumn ? 'disabled' : ''}><input type="checkbox" title="Primary" aria-label="Primary" data-field-primary="${attr(field.id)}" ${field.primary ? 'checked' : ''} ${locked || fixed || nonStored || field.usesExistingColumn || isExtensionKind(spec?.definitionKind) ? 'disabled' : ''}><input type="checkbox" title="API-aware" aria-label="API-aware" data-field-api="${attr(field.id)}" ${(nonStored || field.usesExistingColumn ? field.associationApiAware : field.apiAware) ? 'checked' : ''} ${locked ? 'disabled' : ''}><div class="field-actions"><button class="secondary" title="Field details" aria-label="Field details" data-select-field="${attr(field.id)}">${issues.length ? `!${issues.length}` : '…'}</button><button class="secondary" title="Move up" aria-label="Move up" data-up="${attr(field.id)}" ${index === 0 ? 'disabled' : ''}>↑</button><button class="secondary" title="Move down" aria-label="Move down" data-down="${attr(field.id)}" ${index === activeFields().length - 1 ? 'disabled' : ''}>↓</button>${removable ? `<button class="secondary" title="Remove field" aria-label="Remove field" data-remove="${attr(field.id)}">×</button>` : ''}</div>${locked ? `<div class="field-note muted">Custom field expression is locked and will be preserved.</div>` : ''}</div>`;
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
  const foreignKey = field.kind === 'foreign-key';
  const toOne = field.kind === 'many-to-one' || field.kind === 'one-to-one';
  const hasStoredField = !isNonStored(field) && !field.usesExistingColumn;
	if (field.implementation) {
		controls.push(`<div class="detail-control span-two"><span>Shopware field implementation</span><code>${esc(field.implementation.class)}</code></div>`);
		const constructorArguments = field.implementation.constructorMode === 'fixed' ? field.implementation.fixedArguments : field.implementation.additionalArguments;
		const minimum = field.implementation.constructorMode === 'storage-property' ? field.implementation.minimumAdditionalArguments ?? 0 : 0;
		controls.push(`<label class="span-two"><span>Additional PHP constructor arguments${minimum ? ` (at least ${minimum})` : ' (optional)'}</span><textarea data-implementation-arguments="${attr(field.id)}" rows="4" placeholder="One PHP expression per line">${esc((constructorArguments ?? []).join('\n'))}</textarea></label>`);
	}
  if (relation) {
    controls.push(`<div class="detail-control"><span>Relation target</span><button class="secondary" data-relation="${attr(field.id)}">${esc(field.targetEntityName || field.targetDefinitionClass || 'Choose target…')}</button></div>`);
  }
  if (field.kind === 'string') controls.push(numberControl(field, 'maxLength', 'Maximum length', 1, '1', 16383));
	if (field.kind === 'enum') {
		controls.push(`<label class="span-two"><span>Backed enum class</span><input data-enum-class="${attr(field.id)}" value="${attr(field.enumClass ?? '')}" placeholder="Acme\\Plugin\\Core\\Status"></label>`);
		controls.push(`<label><span>Constructor enum case</span><input data-enum-case="${attr(field.id)}" value="${attr(field.enumCase ?? '')}" placeholder="Active"></label>`);
		controls.push(`<label><span>Backing type</span><select data-field-choice="${attr(field.id)}" data-choice-key="enumBackingType"><option value="string" ${field.enumBackingType !== 'int' ? 'selected' : ''}>string</option><option value="int" ${field.enumBackingType === 'int' ? 'selected' : ''}>int</option></select></label>`);
	}
  if (field.kind === 'int') {
    controls.push(numberControl(field, 'min', 'Minimum value'));
    controls.push(numberControl(field, 'max', 'Maximum value'));
  }
  if (field.kind === 'string' || field.kind === 'long-text') {
	controls.push(numberControl(field, 'searchRanking', 'Search ranking', 0, 'any'));
	if ((field.searchRanking ?? 0) > 0) controls.push(checkboxControl(field.id, 'search-tokenize', 'Tokenize search value', field.searchRankingTokenize ?? true));
  }
  if ((spec?.definitionKind ?? 'entity') === 'entity' && isTranslatable(field.kind)) {
    controls.push(checkboxControl(field.id, 'translated', 'Store this field in a translation entity', Boolean(field.translated)));
    if (field.translated) {
      controls.push(checkboxControl(field.id, 'translation-sorting', 'Allow translated sorting', Boolean(field.translationUseForSorting)));
      controls.push(checkboxControl(field.id, 'translation-api', 'Translated facade API-aware', field.translationApiAware ?? Boolean(field.apiAware)));
	  if (field.translationApiAware ?? Boolean(field.apiAware)) controls.push(apiAwareSourcesControl(field.id, 'translation', field.translationApiAwareSources ?? field.apiAwareSources));
      controls.push(numberControl(field, 'translationSearchRanking', 'Translated facade search ranking', 0, 'any'));
	  if ((field.translationSearchRanking ?? 0) > 0) controls.push(checkboxControl(field.id, 'translation-search-tokenize', 'Tokenize translated search value', field.translationSearchRankingTokenize ?? true));
	  controls.push(...behaviorControls(field.id, 'translation', 'Translated facade', field.translationBehavior));
	  controls.push(...metadataControls(field.id, 'translation', 'Translated facade', field.translationMetadata));
      controls.push(...writeProtectionControls(field.id, 'translation', 'Translated facade write-protected', Boolean(field.translationWriteProtected), field.translationWriteProtectedScopes));
      controls.push(checkboxControl(field.id, 'translation-association-write-protected', 'Translations association write-protected', Boolean(spec?.translation?.associationWriteProtected)));
	  controls.push(checkboxControl(field.id, 'translation-association-api', 'Translations association API-aware', Boolean(spec?.translation?.associationApiAware)));
	  if (spec?.translation?.associationApiAware) controls.push(apiAwareSourcesControl(field.id, 'translation-association', spec.translation.associationApiAwareSources));
      if (spec?.translation?.associationWriteProtected) controls.push(writeScopeControl(field.id, 'translation-association', spec.translation.associationWriteProtectedScopes));
	  controls.push(...behaviorControls(field.id, 'translation-association', 'Translations association', spec?.translation?.associationBehavior));
	  controls.push(...metadataControls(field.id, 'translation-association', 'Translations association', spec?.translation?.associationMetadata));
    }
  }
  if (field.kind === 'list') controls.push(`<label><span>Element field class (optional)</span><input data-element-class="${attr(field.id)}" value="${attr(field.elementTypeClass ?? '')}" placeholder="Shopware\\…\\StringField"></label>`);
  if (field.kind === 'json') {
	controls.push(`<label class="span-two"><span>Property mapping PHP array (optional)</span><textarea data-json-property-mapping="${attr(field.id)}" rows="5" placeholder="[new StringField('name', 'name')]">${esc(field.jsonPropertyMappingExpression ?? '')}</textarea></label>`);
	controls.push(`<label class="span-two"><span>Default PHP array or null (optional)</span><textarea data-json-default="${attr(field.id)}" rows="3" placeholder="[]">${esc(field.jsonDefaultExpression ?? '')}</textarea></label>`);
  }
  if (toOne) {
	if (field.conditionalAssociation) controls.push(`<div class="detail-control span-two"><span>Version-gated association shape</span><code>${esc(field.conditionalAssociation.conditionExpression)}</code><span class="muted">Uses ${esc(field.kind)} when true and ${esc(field.conditionalAssociation.alternativeKind)} otherwise.</span></div>`);
    controls.push(checkboxControl(field.id, 'existing-column', 'Association-only: reuse an existing local column', Boolean(field.usesExistingColumn)));
    if (!field.usesExistingColumn) controls.push(`<label><span>Foreign-key property</span><input data-fk-property="${attr(field.id)}" value="${attr(field.foreignKeyPropertyName ?? '')}" placeholder="productId"></label>`);
    controls.push(choiceControl(field, 'referenceField', 'Referenced target field', field.referenceField ?? 'id', targetFields.map(item => item.propertyName)));
    controls.push(choiceControl(field, 'referenceStorageName', 'Referenced target column', field.referenceStorageName ?? 'id', targetFields.map(item => item.storageName)));
    controls.push(deleteControl(field));
    controls.push(checkboxControl(field.id, 'association-autoload', 'Autoload association', Boolean(field.associationAutoload)));
  }
  if (foreignKey) {
    controls.push(choiceControl(field, 'referenceField', 'Referenced target field', field.referenceField ?? 'id', targetFields.map(item => item.propertyName)));
    controls.push(choiceControl(field, 'referenceStorageName', 'Referenced target column', field.referenceStorageName ?? 'id', targetFields.map(item => item.storageName)));
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
  if (field.kind === 'hierarchy') {
    controls.push(`<div class="detail-control span-two"><span>Native hierarchy bundle</span><span class="muted">ParentFkField + ParentAssociationField + ChildrenAssociationField${field.hierarchyVersionAware ? ' + parent reference version' : ''}</span></div>`);
    controls.push(checkboxControl(field.id, 'association-api', 'Parent association API-aware', Boolean(field.associationApiAware)));
	if (field.associationApiAware) controls.push(apiAwareSourcesControl(field.id, 'association', field.associationApiAwareSources));
    controls.push(checkboxControl(field.id, 'hierarchy-children-api', 'Children association API-aware', Boolean(field.hierarchyChildrenApiAware)));
	if (field.hierarchyChildrenApiAware) controls.push(apiAwareSourcesControl(field.id, 'hierarchy-children', field.hierarchyChildrenApiAwareSources));
    if (field.hierarchyVersionAware) controls.push(checkboxControl(field.id, 'hierarchy-version-api', 'Parent reference-version API-aware', Boolean(field.hierarchyVersionApiAware)));
	if (field.hierarchyVersionAware && field.hierarchyVersionApiAware) controls.push(apiAwareSourcesControl(field.id, 'hierarchy-version', field.hierarchyVersionApiAwareSources));
    controls.push(numberControl(field, 'associationSearchRanking', 'Parent association search ranking', 0, 'any'));
	if ((field.associationSearchRanking ?? 0) > 0) controls.push(checkboxControl(field.id, 'association-search-tokenize', 'Tokenize parent association search value', field.associationSearchRankingTokenize ?? true));
	controls.push(...behaviorControls(field.id, 'association', 'Parent association', field.associationBehavior));
	controls.push(...metadataControls(field.id, 'association', 'Parent association', field.associationMetadata));
    controls.push(numberControl(field, 'hierarchyChildrenSearchRanking', 'Children association search ranking', 0, 'any'));
	if ((field.hierarchyChildrenSearchRanking ?? 0) > 0) controls.push(checkboxControl(field.id, 'hierarchy-children-search-tokenize', 'Tokenize children search value', field.hierarchyChildrenSearchRankingTokenize ?? true));
	controls.push(...behaviorControls(field.id, 'hierarchy-children', 'Children association', field.hierarchyChildrenBehavior));
	controls.push(...metadataControls(field.id, 'hierarchy-children', 'Children association', field.hierarchyChildrenMetadata));
	if (field.hierarchyVersionAware) controls.push(...behaviorControls(field.id, 'hierarchy-version', 'Parent reference-version field', field.hierarchyVersionBehavior));
	if (field.hierarchyVersionAware) controls.push(...metadataControls(field.id, 'hierarchy-version', 'Parent reference-version field', field.hierarchyVersionMetadata));
	controls.push(...writeProtectionControls(field.id, 'hierarchy-children', 'Children association write-protected', Boolean(field.hierarchyChildrenWriteProtected), field.hierarchyChildrenWriteProtectedScopes));
	if (field.hierarchyVersionAware) controls.push(...writeProtectionControls(field.id, 'hierarchy-version', 'Parent reference-version write-protected', Boolean(field.hierarchyVersionWriteProtected), field.hierarchyVersionWriteProtectedScopes));
  }
	if (hasStoredField) controls.push(...behaviorControls(field.id, 'field', field.translated ? 'Translation storage field' : 'Stored field', field.behavior, foreignKey || toOne || field.kind === 'hierarchy'));
	if (hasStoredField && field.apiAware) controls.push(apiAwareSourcesControl(field.id, 'field', field.apiAwareSources));
	if (hasStoredField) controls.push(...metadataControls(field.id, 'field', field.translated ? 'Translation storage field' : 'Stored field', field.metadata, field.kind === 'string' || field.kind === 'long-text'));
	if (hasStoredField) controls.push(...writeProtectionControls(field.id, 'field', field.translated ? 'Translation storage field write-protected' : 'Stored field write-protected', Boolean(field.writeProtected), field.writeProtectedScopes));
	if (isAssociation(field.kind)) controls.push(...writeProtectionControls(field.id, 'association', 'Association write-protected', Boolean(field.associationWriteProtected), field.associationWriteProtectedScopes));
	const inheritanceFieldsEnabled = Boolean(spec?.inheritanceAware || isExtensionKind(spec?.definitionKind));
	const storedInheritance = inheritanceFieldsEnabled && !field.translated && !isNonStored(field) && !field.usesExistingColumn && !['id', 'version', 'auto-increment', 'created-at', 'updated-at', 'hierarchy'].includes(field.kind);
	if (storedInheritance) {
		controls.push(checkboxControl(field.id, 'inherited', 'Inherited stored field', Boolean(field.inherited)));
		if (field.inherited) controls.push(`<label><span>Inherited foreign-key override (optional)</span><input data-inherited-fk="${attr(field.id)}" value="${attr(field.inheritedForeignKey ?? '')}" placeholder="product_id"></label>`);
	}
	if (field.translated && inheritanceFieldsEnabled) {
		controls.push(checkboxControl(field.id, 'translation-inherited', 'Inherited translated facade', Boolean(field.translationInherited)));
		if (field.translationInherited) controls.push(`<label><span>Translated inherited FK override (optional)</span><input data-translation-inherited-fk="${attr(field.id)}" value="${attr(field.translationInheritedForeignKey ?? '')}" placeholder="product_id"></label>`);
	}
	if (isAssociation(field.kind) && field.kind !== 'hierarchy') {
		if (inheritanceFieldsEnabled) {
			controls.push(checkboxControl(field.id, 'association-inherited', 'Inherited association', Boolean(field.associationInherited)));
			if (field.associationInherited) controls.push(`<label><span>Association inherited FK override (optional)</span><input data-association-inherited-fk="${attr(field.id)}" value="${attr(field.associationInheritedForeignKey ?? '')}" placeholder="product_id"></label>`);
		}
		controls.push(`<label><span>Reverse-inherited target property (optional)</span><input data-reverse-inherited="${attr(field.id)}" value="${attr(field.reverseInheritedProperty ?? '')}" placeholder="manufacturer"></label>`);
	}
  if (toOne || field.kind === 'one-to-many' || field.kind === 'many-to-many') {
    controls.push(checkboxControl(field.id, 'association-api', 'Association API-aware', Boolean(field.associationApiAware)));
	if (field.associationApiAware) controls.push(apiAwareSourcesControl(field.id, 'association', field.associationApiAwareSources));
    controls.push(numberControl(field, 'associationSearchRanking', 'Association search ranking', 0, 'any'));
	if ((field.associationSearchRanking ?? 0) > 0) controls.push(checkboxControl(field.id, 'association-search-tokenize', 'Tokenize association search value', field.associationSearchRankingTokenize ?? true));
	controls.push(...behaviorControls(field.id, 'association', 'Association', field.associationBehavior));
	controls.push(...metadataControls(field.id, 'association', 'Association', field.associationMetadata));
  }
  if (field.required && !isNonStored(field) && !['id', 'version', 'auto-increment'].includes(field.kind)) {
    controls.push(`<label class="span-two"><span>Existing-row backfill SQL</span><input data-migration-default="${attr(field.id)}" value="${attr(field.migrationDefault ?? '')}" placeholder="for example 0, 'unknown', JSON_OBJECT()"></label>`);
  }
  const preserved = [...(field.preservedFlags ?? []), ...(field.modifiersBeforeFlags ?? []), ...(field.modifiersAfterFlags ?? []), ...(field.associationFlags ?? []), ...(field.associationModifiersBeforeFlags ?? []), ...(field.associationModifiersAfterFlags ?? []), ...(field.hierarchyChildrenFlags ?? []), ...(field.hierarchyChildrenModifiersBeforeFlags ?? []), ...(field.hierarchyChildrenModifiersAfterFlags ?? []), ...(field.hierarchyVersionFlags ?? []), ...(field.hierarchyVersionModifiersBeforeFlags ?? []), ...(field.hierarchyVersionModifiersAfterFlags ?? []), ...(field.translationFlags ?? []), ...(field.translationModifiersBeforeFlags ?? []), ...(field.translationModifiersAfterFlags ?? [])];
  return `<div class="inspector"><div class="toolbar"><h3 style="margin-right:auto">${esc(field.propertyName || field.kind)} details</h3><span class="muted">${esc(field.kind)}</span></div>${issues.map(issue => `<div class="issue error">${esc(issue.message)} <span class="muted">[${esc(issue.code)}]</span></div>`).join('')}<div class="inspector-grid">${controls.join('')}</div>${preserved.length ? `<details><summary>Preserved custom flags and modifiers (${preserved.length})</summary><pre>${esc(preserved.join('\n'))}</pre></details>` : ''}</div>`;
}

function indexRow(index: {name: string; kind: string; columns: string[]; translation?: boolean}, row: number): string {
  const columns = Array.from(new Set([...(index.translation ? translationColumns() : storedColumns()), ...index.columns]));
  const target = spec?.translation?.enabled ? `<select data-index-target="${row}"><option value="parent" ${index.translation ? '' : 'selected'}>Parent table</option><option value="translation" ${index.translation ? 'selected' : ''}>Translation table</option></select>` : '';
  return `<div class="index-row">${target}<input data-index-name="${row}" value="${attr(index.name)}" placeholder="idx.entity.column"><select data-index-kind="${row}"><option value="index" ${index.kind === 'index' ? 'selected' : ''}>Index</option><option value="unique" ${index.kind === 'unique' ? 'selected' : ''}>Unique</option></select><div class="column-picker">${columns.map(column => `<label><input type="checkbox" data-index-column="${row}" value="${attr(column)}" ${index.columns.includes(column) ? 'checked' : ''}> ${esc(column)}</label>`).join('') || '<span class="muted">Add a stored field first.</span>'}</div><button class="secondary" data-remove-index="${row}">Remove</button></div>`;
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

function writeProtectionControls(fieldId: string, prefix: string, label: string, enabled: boolean, scopes?: string[]): string[] {
  const controls = [checkboxControl(fieldId, `${prefix}-write-protected`, label, enabled)];
  if (enabled) controls.push(writeScopeControl(fieldId, prefix, scopes));
  return controls;
}

function writeScopeControl(fieldId: string, prefix: string, scopes?: string[]): string {
  return `<label><span>Allowed write scopes (optional)</span><input data-${prefix}-write-scopes="${attr(fieldId)}" value="${attr((scopes ?? []).join(', '))}" placeholder="system, crud"></label>`;
}

function apiAwareSourcesControl(fieldId: string, prefix: string, sources?: string[]): string {
	return `<label class="span-two"><span>Allowed API source classes (optional)</span><input data-${prefix}-api-sources="${attr(fieldId)}" value="${attr((sources ?? []).join(', '))}" placeholder="Shopware\\Core\\Framework\\Api\\Context\\AdminApiSource"></label>`;
}

function parseScopeList(value: string): string[] | undefined {
  const scopes = Array.from(new Set(value.split(',').map(scope => scope.trim()).filter(Boolean)));
  return scopes.length ? scopes : undefined;
}

function behaviorControls(fieldId: string, prefix: string, label: string, behavior?: Behavior, allowNoConstraint = false): string[] {
	const controls = [
		checkboxControl(fieldId, `${prefix}-runtime`, `${label} is populated at runtime`, Boolean(behavior?.runtime)),
		checkboxControl(fieldId, `${prefix}-computed`, `${label} is computed and read-only`, Boolean(behavior?.computed)),
	];
	if (behavior?.runtime) {
		if (behavior.runtimeDependenciesExpression) {
			controls.push(`<div class="detail-control span-two"><span>Imported runtime dependencies</span><code>${esc(behavior.runtimeDependenciesExpression)}</code><span class="muted">The non-literal PHP expression is preserved verbatim.</span></div>`);
		} else {
			controls.push(`<label><span>Runtime dependencies (comma-separated)</span><input data-${prefix}-runtime-dependencies="${attr(fieldId)}" value="${attr((behavior.runtimeDependencies ?? []).join(', '))}" placeholder="path, updatedAt"></label>`);
		}
	}
	if (allowNoConstraint) controls.push(checkboxControl(fieldId, `${prefix}-no-constraint`, 'No physical database foreign-key constraint', Boolean(behavior?.noConstraint)));
	return controls;
}

function metadataControls(fieldId: string, prefix: string, label: string, metadata?: Metadata, allowHTML = false): string[] {
	const controls: string[] = [];
	if (allowHTML) {
		controls.push(checkboxControl(fieldId, `${prefix}-allow-html`, `${label} allows HTML`, metadata?.allowHtmlSanitized !== undefined));
		if (metadata?.allowHtmlSanitized !== undefined) controls.push(checkboxControl(fieldId, `${prefix}-sanitize-html`, 'Sanitize allowed HTML', metadata.allowHtmlSanitized));
		controls.push(checkboxControl(fieldId, `${prefix}-allow-empty-string`, 'Allow an empty string', Boolean(metadata?.allowEmptyString)));
	}
	controls.push(checkboxControl(fieldId, `${prefix}-as-array`, `Expose ${label.toLowerCase()} as an array`, Boolean(metadata?.asArray)));
	controls.push(checkboxControl(fieldId, `${prefix}-immutable`, `${label} is immutable after creation`, Boolean(metadata?.immutable)));
	controls.push(checkboxControl(fieldId, `${prefix}-api-criteria-aware`, `${label} may be used in API criteria`, Boolean(metadata?.apiCriteriaAware)));
	controls.push(checkboxControl(fieldId, `${prefix}-ignore-openapi`, `Hide ${label.toLowerCase()} from OpenAPI schema`, Boolean(metadata?.ignoreInOpenapiSchema)));
	controls.push(checkboxControl(fieldId, `${prefix}-ignore-unused-media`, `Ignore ${label.toLowerCase()} in unused-media search`, Boolean(metadata?.ignoreInUnusedMediaSearch)));
	controls.push(checkboxControl(fieldId, `${prefix}-do-not-use-context`, `${label} must not use Context`, Boolean(metadata?.doNotUseContext)));
	controls.push(`<label><span>Available since (optional)</span><input data-${prefix}-since="${attr(fieldId)}" value="${attr(metadata?.since ?? '')}" placeholder="6.7.0.0"></label>`);
	controls.push(checkboxControl(fieldId, `${prefix}-deprecated`, `${label} is deprecated`, Boolean(metadata?.deprecated)));
	if (metadata?.deprecated) {
		controls.push(`<label><span>Deprecated since</span><input data-${prefix}-deprecated-since="${attr(fieldId)}" value="${attr(metadata.deprecated.deprecatedSince)}"></label>`);
		controls.push(`<label><span>Removed in</span><input data-${prefix}-removed-in="${attr(fieldId)}" value="${attr(metadata.deprecated.willBeRemovedIn)}"></label>`);
		controls.push(`<label><span>Replacement (optional)</span><input data-${prefix}-replaced-by="${attr(fieldId)}" value="${attr(metadata.deprecated.replacedBy ?? '')}"></label>`);
	}
	if (metadata?.ruleAreas?.length) controls.push(`<label><span>Rule areas</span><input data-${prefix}-rule-areas="${attr(fieldId)}" value="${attr(metadata.ruleAreas.join(', '))}"></label>`);
	if (metadata?.choice) {
		controls.push(`<label class="span-two"><span>Choice value expressions (one per line)</span><textarea data-${prefix}-choice-values="${attr(fieldId)}">${esc(metadata.choice.values.join('\n'))}</textarea></label>`);
		controls.push(checkboxControl(fieldId, `${prefix}-choice-strict`, 'Strictly validate choice values', metadata.choice.strict ?? false));
	}
	return controls;
}

function parseList(value: string): string[] | undefined {
	const values = Array.from(new Set(value.split(',').map(item => item.trim()).filter(Boolean)));
	return values.length ? values : undefined;
}

function metadataIsEmpty(metadata: Metadata): boolean {
	return metadata.allowHtmlSanitized === undefined && !metadata.allowEmptyString && !metadata.asArray && !metadata.immutable && !metadata.since && !metadata.deprecated &&
		!metadata.ignoreInOpenapiSchema && !metadata.ignoreInUnusedMediaSearch && !metadata.apiCriteriaAware && !metadata.ruleAreas?.length && !metadata.choice &&
		!metadata.doNotUseContext && !metadata.extension;
}

function deleteControl(field: Field): string {
	let option = '';
	if (field.deleteBehavior === 'cascade') option = checkboxControl(field.id, 'delete-clone-relevant', 'Include relation when cloning versions', field.deleteCloneRelevant ?? true);
	if (field.deleteBehavior === 'set-null') option = checkboxControl(field.id, 'delete-enforced-by-constraint', 'Enforce set-null through the database constraint', field.deleteEnforcedByConstraint ?? true);
  return `<label><span>Delete behavior</span><select data-delete="${attr(field.id)}"><option value="" ${!field.deleteBehavior ? 'selected' : ''}>framework default</option><option value="restrict" ${field.deleteBehavior === 'restrict' ? 'selected' : ''}>restrict</option><option value="cascade" ${field.deleteBehavior === 'cascade' ? 'selected' : ''}>cascade</option><option value="set-null" ${field.deleteBehavior === 'set-null' ? 'selected' : ''}>set null</option></select></label>${option}`;
}

function bindEvents(): void {
  document.getElementById('definitionKind')?.addEventListener('change', event => changeDefinitionKind((event.target as HTMLSelectElement).value as DefinitionKind));
	bindDefinitionClassInputs();
	document.querySelector<HTMLInputElement>('[data-inheritance-aware]')?.addEventListener('change', event => {
		const enabled = (event.target as HTMLInputElement).checked;
		spec!.inheritanceAware = enabled;
		if (enabled && !spec!.fields.some(item => item.kind === 'hierarchy')) {
			const hierarchy: Field = {id: `hierarchy-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`, kind: 'hierarchy', propertyName: 'children', editable: true};
			applyFieldDefaults(hierarchy); spec!.fields.push(hierarchy); selectedFieldId = hierarchy.id;
		}
		if (!enabled) clearInheritedFlags();
		changed(true);
	});
	document.querySelector<HTMLInputElement>('[data-entity-read-protected]')?.addEventListener('change', event => {
		spec!.readProtected = (event.target as HTMLInputElement).checked;
		if (!spec!.readProtected) spec!.readProtectionScopes = undefined;
		changed(true);
	});
	document.querySelector<HTMLInputElement>('[data-entity-write-protected]')?.addEventListener('change', event => {
		spec!.writeProtected = (event.target as HTMLInputElement).checked;
		if (!spec!.writeProtected) spec!.writeProtectionScopes = undefined;
		changed(true);
	});
	document.querySelector<HTMLInputElement>('[data-entity-read-scopes]')?.addEventListener('change', event => { spec!.readProtectionScopes = parseList((event.target as HTMLInputElement).value); changed(); });
	document.querySelector<HTMLInputElement>('[data-entity-write-scopes]')?.addEventListener('change', event => { spec!.writeProtectionScopes = parseList((event.target as HTMLInputElement).value); changed(); });
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
	document.getElementById('addIndex')?.addEventListener('click', () => { activeIndexes(true).push({name: `idx.${activeEntityName()}.`, kind: 'index', columns: []}); changed(true); });
	document.querySelectorAll<HTMLElement>('[data-remove-index]').forEach(button => button.addEventListener('click', () => { activeIndexes().splice(Number(button.dataset.removeIndex), 1); changed(true); }));
	document.querySelectorAll<HTMLInputElement>('[data-index-name]').forEach(input => input.addEventListener('change', () => { activeIndexes()[Number(input.dataset.indexName)].name = input.value; changed(); }));
	document.querySelectorAll<HTMLSelectElement>('[data-index-kind]').forEach(input => input.addEventListener('change', () => { activeIndexes()[Number(input.dataset.indexKind)].kind = input.value; changed(); }));
	document.querySelectorAll<HTMLSelectElement>('[data-index-target]').forEach(input => input.addEventListener('change', () => {
		const index = activeIndexes()[Number(input.dataset.indexTarget)];
		index.translation = input.value === 'translation';
		index.columns = [];
		changed(true);
	}));
  document.querySelectorAll<HTMLInputElement>('[data-index-column]').forEach(input => input.addEventListener('change', () => {
	const index = activeIndexes()[Number(input.dataset.indexColumn)];
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
  document.getElementById('loadExisting')?.addEventListener('change', event => { const value = (event.target as HTMLSelectElement).value; if (value) { const target = bootstrap?.editable?.find(item => item.definitionClass === value); previewRequestId++; previewBusy = false; actionBusy = true; render(); vscode.postMessage({type: 'load', definitionClass: value, definitionKind: target?.definitionKind, fileUri: target?.fileUri}); } });
  document.getElementById('extensionTarget')?.addEventListener('click', () => openRelationDialog('', 'extension'));
	document.getElementById('bulkTarget')?.addEventListener('change', event => {
		selectedBulkTargetId = (event.target as HTMLSelectElement).value;
		selectedFieldId = activeFields()[0]?.id ?? '';
		persistState(); render();
	});
	document.getElementById('addBulkTarget')?.addEventListener('click', () => openRelationDialog('', 'bulk-extension-add'));
	document.getElementById('changeBulkTarget')?.addEventListener('click', () => openRelationDialog('', 'bulk-extension-change'));
	document.getElementById('removeBulkTarget')?.addEventListener('click', () => {
		if (!spec) return;
		spec.bulkExtensions = (spec.bulkExtensions ?? []).filter(target => target.id !== selectedBulkTargetId);
		selectedBulkTargetId = spec.bulkExtensions[0]?.id ?? '';
		selectedFieldId = activeFields()[0]?.id ?? '';
		changed(true);
	});
	document.getElementById('addFieldModification')?.addEventListener('click', () => {
		(spec!.fieldModifications ??= []).push({id: `modify-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`, propertyName: ''});
		changed(true);
	});
	document.querySelectorAll<HTMLElement>('[data-remove-modification]').forEach(button => button.addEventListener('click', () => {
		spec!.fieldModifications!.splice(Number(button.dataset.removeModification), 1);
		changed(true);
	}));
	document.querySelectorAll<HTMLInputElement>('[data-modification-property]').forEach(input => input.addEventListener('change', () => {
		spec!.fieldModifications![Number(input.dataset.modificationProperty)].propertyName = input.value;
		changed();
	}));
	document.querySelectorAll<HTMLElement>('[data-add-modification-flag]').forEach(button => button.addEventListener('click', () => {
		const index = Number(button.dataset.addModificationFlag);
		const select = document.querySelector<HTMLSelectElement>(`[data-add-modification-flag-select="${index}"]`);
		if (!select?.value) return;
		(spec!.fieldModifications![index].addFlags ??= []).push(defaultFieldFlag(select.value as FlagKind));
		changed(true);
	}));
	document.querySelectorAll<HTMLElement>('[data-remove-added-flag]').forEach(button => button.addEventListener('click', () => {
		const [modificationIndex, flagIndex] = button.dataset.removeAddedFlag!.split(':').map(Number);
		spec!.fieldModifications![modificationIndex].addFlags!.splice(flagIndex, 1);
		changed(true);
	}));
	document.querySelectorAll<HTMLElement>('[data-add-removed-flag]').forEach(button => button.addEventListener('click', () => {
		const index = Number(button.dataset.addRemovedFlag);
		const select = document.querySelector<HTMLSelectElement>(`[data-remove-modification-flag-select="${index}"]`);
		if (!select?.value) return;
		(spec!.fieldModifications![index].removeFlags ??= []).push(select.value as FlagKind);
		changed(true);
	}));
	document.querySelectorAll<HTMLElement>('[data-remove-removed-flag]').forEach(button => button.addEventListener('click', () => {
		const [modificationIndex, flagIndex] = button.dataset.removeRemovedFlag!.split(':').map(Number);
		spec!.fieldModifications![modificationIndex].removeFlags!.splice(flagIndex, 1);
		changed(true);
	}));
	document.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>('[data-modification-flag]').forEach(input => input.addEventListener('change', () => {
		const [modificationIndex, flagIndex] = input.dataset.modificationFlag!.split(':').map(Number);
		const flag = spec!.fieldModifications![modificationIndex].addFlags![flagIndex];
		const key = input.dataset.flagKey!;
		const value = input instanceof HTMLInputElement && input.type === 'checkbox' ? input.checked : input.value;
		switch (key) {
			case 'apiSources': flag.apiSources = parseScopeList(String(value)); break;
			case 'runtimeDependencies': flag.runtimeDependencies = parseList(String(value)); break;
			case 'writeScopes': flag.writeScopes = parseScopeList(String(value)); break;
			case 'ruleAreas': flag.ruleAreas = parseScopeList(String(value)); break;
			case 'searchRanking': flag.searchRanking = Number(value); break;
			case 'choiceValues': (flag.choice ??= {values: []}).values = String(value).split('\n').map(item => item.trim()).filter(Boolean); break;
			case 'choiceStrict': (flag.choice ??= {values: []}).strict = Boolean(value); break;
			case 'deprecatedSince': (flag.deprecated ??= {deprecatedSince: '', willBeRemovedIn: ''}).deprecatedSince = String(value); break;
			case 'willBeRemovedIn': (flag.deprecated ??= {deprecatedSince: '', willBeRemovedIn: ''}).willBeRemovedIn = String(value); break;
			case 'replacedBy': (flag.deprecated ??= {deprecatedSince: '', willBeRemovedIn: ''}).replacedBy = String(value) || undefined; break;
			default: (flag as unknown as Record<string, unknown>)[key] = value;
		}
		changed();
	}));
  document.querySelectorAll<HTMLElement>('[data-drift]').forEach(button => button.addEventListener('click', () => { driftDecision = button.dataset.drift!; destructiveConfirmed = false; persistState(); requestPreview(); }));
  document.querySelectorAll<HTMLSelectElement>('[data-rename]').forEach(select => select.addEventListener('change', () => { if (!select.value) return; const entity = select.dataset.renameEntity!; const added = select.dataset.renameAdded!; decisions = decisions.filter(item => !(item.entity === entity && item.to === added)); decisions.push(select.value === 'create' ? {kind: 'columnCreate', entity, to: added} : {kind: 'columnRename', entity, from: select.value, to: added}); destructiveConfirmed = false; persistState(); requestPreview(); }));
	document.querySelectorAll<HTMLSelectElement>('[data-entity-rename]').forEach(select => select.addEventListener('change', () => {
		if (!select.value) return;
		const added = select.dataset.entityRenameAdded!;
		decisions = decisions.filter(item => !((item.kind === 'entityCreate' || item.kind === 'entityRename') && item.to === added));
		decisions.push(select.value === 'create' ? {kind: 'entityCreate', entity: added, to: added} : {kind: 'entityRename', entity: added, from: select.value, to: added});
		destructiveConfirmed = false;
		persistState();
		requestPreview();
	}));
	document.querySelectorAll<HTMLElement>('[data-show-field]').forEach(button => button.addEventListener('click', () => {
		const id = button.dataset.showField!;
		const target = spec?.bulkExtensions?.find(candidate => candidate.id === id || candidate.fields.some(field => field.id === id));
		if (target) selectedBulkTargetId = target.id;
		selectedFieldId = id;
		if (!activeFields().some(field => field.id === id)) selectedFieldId = activeFields()[0]?.id ?? '';
		render(); document.querySelector(`[data-field-row="${cssEscape(selectedFieldId)}"]`)?.scrollIntoView({block: 'center'});
	}));
  document.getElementById('reconcile')?.addEventListener('click', () => { actionBusy = true; render(); vscode.postMessage({type: 'reconcile', selectedLeaf: (document.getElementById('leaf') as HTMLSelectElement).value}); });
}

function definitionOwner(scope: string): Spec | Translation | undefined {
	if (!spec) return undefined;
	return scope === 'translation' ? spec.translation : spec;
}

function ensureDefinitionBehavior(scope: string): DefinitionBehavior | undefined {
	const owner = definitionOwner(scope);
	if (!owner) return undefined;
	return owner.definitionBehavior ??= {};
}

function ensureDefinitionMetadata(scope: string): DefinitionMetadata | undefined {
	const owner = definitionOwner(scope);
	if (!owner) return undefined;
	return owner.definitionMetadata ??= {};
}

function definitionDefaults(scope: string, kind: string, create = false): DefinitionDefault[] | undefined {
	const metadata = create ? ensureDefinitionMetadata(scope) : definitionOwner(scope)?.definitionMetadata;
	if (!metadata || (kind !== 'defaults' && kind !== 'childDefaults')) return undefined;
	if (create) metadata[kind] ??= [];
	return metadata[kind];
}

function bindDefinitionClassInputs(): void {
	document.querySelectorAll<HTMLSelectElement>('[data-definition-parent]').forEach(input => input.addEventListener('change', () => {
		const behavior = ensureDefinitionBehavior(input.dataset.definitionParent!);
		if (!behavior) return;
		behavior.parentDefinitionClass = input.value || undefined;
		changed();
	}));
	document.querySelectorAll<HTMLSelectElement>('[data-definition-version-aware]').forEach(input => input.addEventListener('change', () => {
		const behavior = ensureDefinitionBehavior(input.dataset.definitionVersionAware!);
		if (!behavior) return;
		behavior.versionAware = input.value === '' ? undefined : input.value === 'true';
		changed();
	}));
	document.querySelectorAll<HTMLInputElement>('[data-definition-default-fields]').forEach(input => input.addEventListener('change', () => {
		const behavior = ensureDefinitionBehavior(input.dataset.definitionDefaultFields!);
		if (!behavior) return;
		behavior.overrideDefaultFields = input.checked || undefined;
		if (!input.checked) behavior.defaultFields = undefined;
		changed(true);
	}));
	document.querySelectorAll<HTMLInputElement>('[data-definition-restrict-properties]').forEach(input => input.addEventListener('change', () => {
		const behavior = ensureDefinitionBehavior(input.dataset.definitionRestrictProperties!);
		if (!behavior) return;
		behavior.restrictDeleteMetaProperties = parseScopeList(input.value);
		changed();
	}));
	document.querySelectorAll<HTMLInputElement>('[data-definition-metadata-text]').forEach(input => input.addEventListener('change', () => {
		const [scope, key] = input.dataset.definitionMetadataText!.split(':') as [string, 'since' | 'hydratorClass'];
		const metadata = ensureDefinitionMetadata(scope);
		if (!metadata) return;
		metadata[key] = input.value || undefined;
		changed();
	}));
	document.querySelectorAll<HTMLElement>('[data-add-definition-default]').forEach(button => button.addEventListener('click', () => {
		const [scope, kind] = button.dataset.addDefinitionDefault!.split(':');
		definitionDefaults(scope, kind, true)?.push({propertyName: '', valueExpression: ''});
		changed(true);
	}));
	document.querySelectorAll<HTMLElement>('[data-remove-definition-default]').forEach(button => button.addEventListener('click', () => {
		const [scope, kind, rawIndex] = button.dataset.removeDefinitionDefault!.split(':');
		definitionDefaults(scope, kind)?.splice(Number(rawIndex), 1);
		changed(true);
	}));
	document.querySelectorAll<HTMLInputElement>('[data-definition-default-property]').forEach(input => input.addEventListener('change', () => {
		const [scope, kind, rawIndex] = input.dataset.definitionDefaultProperty!.split(':');
		const value = definitionDefaults(scope, kind)?.[Number(rawIndex)];
		if (!value) return;
		value.propertyName = input.value;
		changed();
	}));
	document.querySelectorAll<HTMLInputElement>('[data-definition-default-expression]').forEach(input => input.addEventListener('change', () => {
		const [scope, kind, rawIndex] = input.dataset.definitionDefaultExpression!.split(':');
		const value = definitionDefaults(scope, kind)?.[Number(rawIndex)];
		if (!value) return;
		value.valueExpression = input.value;
		changed();
	}));
}

function bindFieldInputs(): void {
  const field = (id: string): Field => activeFields().find(item => item.id === id)!;
  const textBindings: [string, keyof Field][] = [['field-property','propertyName'],['field-storage','storageName'],['fk-property','foreignKeyPropertyName'],['reference-storage','referenceStorageName'],['migration-default','migrationDefault'],['element-class','elementTypeClass'],['enum-class','enumClass'],['enum-case','enumCase'],['mapping-local','mappingLocalColumn'],['mapping-reference','mappingReferenceColumn'],['source-column','sourceColumn'],['inherited-fk','inheritedForeignKey'],['association-inherited-fk','associationInheritedForeignKey'],['translation-inherited-fk','translationInheritedForeignKey'],['reverse-inherited','reverseInheritedProperty']];
  for (const [attribute, key] of textBindings) document.querySelectorAll<HTMLInputElement>(`[data-${attribute}]`).forEach(input => input.addEventListener('change', () => { (field(input.dataset[toDataset(attribute)]!) as unknown as Record<string, unknown>)[key] = input.value; changed(); }));
  document.querySelectorAll<HTMLInputElement | HTMLSelectElement>('[data-field-choice]').forEach(input => input.addEventListener('change', () => { (field(input.dataset.fieldChoice!) as unknown as Record<string, unknown>)[input.dataset.choiceKey!] = input.value; changed(); }));
  document.querySelectorAll<HTMLInputElement>('[data-field-number]').forEach(input => input.addEventListener('change', () => { if (!input.checkValidity()) { input.reportValidity(); return; } const item = field(input.dataset.fieldNumber!) as unknown as Record<string, unknown>; const key = input.dataset.numberKey!; item[key] = input.value === '' ? undefined : input.valueAsNumber; if (!item[key]) { if (key === 'searchRanking') item.searchRankingTokenize = undefined; if (key === 'associationSearchRanking') item.associationSearchRankingTokenize = undefined; if (key === 'translationSearchRanking') item.translationSearchRankingTokenize = undefined; if (key === 'hierarchyChildrenSearchRanking') item.hierarchyChildrenSearchRankingTokenize = undefined; } changed(true); }));
  document.querySelectorAll<HTMLSelectElement>('[data-field-kind]').forEach(input => input.addEventListener('change', () => { changeFieldKind(field(input.dataset.fieldKind!), input.value); changed(true); }));
  document.querySelectorAll<HTMLInputElement>('[data-field-required]').forEach(input => input.addEventListener('change', () => { const item = field(input.dataset.fieldRequired!); item.required = input.checked; if (item.kind === 'many-to-one' || item.kind === 'one-to-one') item.deleteBehavior = input.checked ? 'restrict' : 'set-null'; if ((item.kind === 'foreign-key' || item.kind === 'many-to-one' || item.kind === 'one-to-one') && item.targetDefinitionClass) ensureReferenceVersionRequired(item.targetDefinitionClass, input.checked); changed(true); }));
  document.querySelectorAll<HTMLInputElement>('[data-field-primary]').forEach(input => input.addEventListener('change', () => { const item = field(input.dataset.fieldPrimary!); item.primary = input.checked; if (input.checked) item.required = true; changed(true); }));
  document.querySelectorAll<HTMLInputElement>('[data-field-api]').forEach(input => input.addEventListener('change', () => { const item = field(input.dataset.fieldApi!); if (item.kind === 'one-to-many' || item.kind === 'many-to-many' || item.usesExistingColumn) { item.associationApiAware = input.checked; if (!input.checked) item.associationApiAwareSources = undefined; } else { item.apiAware = input.checked; if (!input.checked) item.apiAwareSources = undefined; } changed(true); }));
  document.querySelectorAll<HTMLInputElement>('[data-association-api]').forEach(input => input.addEventListener('change', () => { const item = field(input.dataset.associationApi!); item.associationApiAware = input.checked; if (!input.checked) item.associationApiAwareSources = undefined; changed(true); }));
	document.querySelectorAll<HTMLTextAreaElement>('[data-json-property-mapping]').forEach(input => input.addEventListener('change', () => { field(input.dataset.jsonPropertyMapping!).jsonPropertyMappingExpression = input.value || undefined; changed(); }));
	document.querySelectorAll<HTMLTextAreaElement>('[data-json-default]').forEach(input => input.addEventListener('change', () => { field(input.dataset.jsonDefault!).jsonDefaultExpression = input.value || undefined; changed(); }));
	document.querySelectorAll<HTMLTextAreaElement>('[data-implementation-arguments]').forEach(input => input.addEventListener('change', () => {
		const implementation = field(input.dataset.implementationArguments!).implementation;
		if (!implementation) return;
		const values = input.value.split('\n').map(value => value.trim()).filter(Boolean);
		if (implementation.constructorMode === 'fixed') implementation.fixedArguments = values.length ? values : undefined;
		else implementation.additionalArguments = values.length ? values : undefined;
		changed();
	}));
	const apiSourceBindings: [string, keyof Field][] = [
		['field', 'apiAwareSources'], ['association', 'associationApiAwareSources'], ['translation', 'translationApiAwareSources'],
		['hierarchy-children', 'hierarchyChildrenApiAwareSources'], ['hierarchy-version', 'hierarchyVersionApiAwareSources'],
	];
	for (const [prefix, key] of apiSourceBindings) document.querySelectorAll<HTMLInputElement>(`[data-${prefix}-api-sources]`).forEach(input => input.addEventListener('change', () => {
		(field(input.dataset[toDataset(`${prefix}-api-sources`)]!) as unknown as Record<string, unknown>)[key] = parseScopeList(input.value);
		changed();
	}));
  document.querySelectorAll<HTMLInputElement>('[data-association-autoload]').forEach(input => input.addEventListener('change', () => { field(input.dataset.associationAutoload!).associationAutoload = input.checked; changed(); }));
	const rankingTokenizeBindings: [string, keyof Field][] = [
		['search-tokenize', 'searchRankingTokenize'],
		['association-search-tokenize', 'associationSearchRankingTokenize'],
		['translation-search-tokenize', 'translationSearchRankingTokenize'],
		['hierarchy-children-search-tokenize', 'hierarchyChildrenSearchRankingTokenize'],
	];
	for (const [attribute, key] of rankingTokenizeBindings) {
		document.querySelectorAll<HTMLInputElement>(`[data-${attribute}]`).forEach(input => input.addEventListener('change', () => {
			(field(input.dataset[toDataset(attribute)]!) as unknown as Record<string, unknown>)[key] = input.checked;
			changed();
		}));
	}
	const behaviorBindings: [string, keyof Field][] = [
		['field', 'behavior'], ['association', 'associationBehavior'], ['translation', 'translationBehavior'],
		['hierarchy-children', 'hierarchyChildrenBehavior'], ['hierarchy-version', 'hierarchyVersionBehavior'],
	];
	for (const [prefix, key] of behaviorBindings) {
		for (const flag of ['runtime', 'computed', 'no-constraint'] as const) {
			document.querySelectorAll<HTMLInputElement>(`[data-${prefix}-${flag}]`).forEach(input => input.addEventListener('change', () => {
				const item = field(input.dataset[toDataset(`${prefix}-${flag}`)]!);
				const record = item as unknown as Record<string, unknown>;
				const behavior = (record[key] ??= {}) as Behavior;
				if (flag === 'runtime') {
					behavior.runtime = input.checked;
					if (!input.checked) { behavior.runtimeDependencies = undefined; behavior.runtimeDependenciesExpression = undefined; }
				} else if (flag === 'computed') behavior.computed = input.checked;
				else behavior.noConstraint = input.checked;
				if (!behavior.runtime && !behavior.computed && !behavior.noConstraint) record[key] = undefined;
				changed(true);
			}));
		}
		document.querySelectorAll<HTMLInputElement>(`[data-${prefix}-runtime-dependencies]`).forEach(input => input.addEventListener('change', () => {
			const item = field(input.dataset[toDataset(`${prefix}-runtime-dependencies`)]!);
			const record = item as unknown as Record<string, unknown>;
			const behavior = (record[key] ??= {runtime: true}) as Behavior;
			behavior.runtimeDependencies = parseList(input.value);
			changed();
		}));
	}
	const metadataBindings: [string, keyof Field][] = [
		['field', 'metadata'], ['association', 'associationMetadata'], ['translation', 'translationMetadata'],
		['hierarchy-children', 'hierarchyChildrenMetadata'], ['hierarchy-version', 'hierarchyVersionMetadata'],
	];
	for (const [prefix, key] of metadataBindings) {
		const edit = (input: HTMLInputElement | HTMLTextAreaElement, mutate: (metadata: Metadata) => void, rerender = false): void => {
			const id = input.dataset[toDataset(input.dataset.metadataAttribute!)]!;
			const item = field(id);
			const record = item as unknown as Record<string, unknown>;
			const metadata = (record[key] ??= {}) as Metadata;
			mutate(metadata);
			if (metadataIsEmpty(metadata)) record[key] = undefined;
			changed(rerender);
		};
		const bindCheck = (suffix: string, mutate: (metadata: Metadata, checked: boolean) => void, rerender = false): void => {
			const attribute = `${prefix}-${suffix}`;
			document.querySelectorAll<HTMLInputElement>(`[data-${attribute}]`).forEach(input => {
				input.dataset.metadataAttribute = attribute;
				input.addEventListener('change', () => edit(input, metadata => mutate(metadata, input.checked), rerender));
			});
		};
		bindCheck('allow-html', (metadata, checked) => { metadata.allowHtmlSanitized = checked ? true : undefined; }, true);
		bindCheck('sanitize-html', (metadata, checked) => { metadata.allowHtmlSanitized = checked; });
		bindCheck('allow-empty-string', (metadata, checked) => { metadata.allowEmptyString = checked; });
		bindCheck('as-array', (metadata, checked) => { metadata.asArray = checked; });
		bindCheck('immutable', (metadata, checked) => { metadata.immutable = checked; });
		bindCheck('api-criteria-aware', (metadata, checked) => { metadata.apiCriteriaAware = checked; });
		bindCheck('ignore-openapi', (metadata, checked) => { metadata.ignoreInOpenapiSchema = checked; });
		bindCheck('ignore-unused-media', (metadata, checked) => { metadata.ignoreInUnusedMediaSearch = checked; });
		bindCheck('do-not-use-context', (metadata, checked) => { metadata.doNotUseContext = checked; });
		bindCheck('deprecated', (metadata, checked) => { metadata.deprecated = checked ? {deprecatedSince: '', willBeRemovedIn: ''} : undefined; }, true);
		bindCheck('choice-strict', (metadata, checked) => { if (metadata.choice) metadata.choice.strict = checked; });
		const textBindings: [string, (metadata: Metadata, value: string) => void][] = [
			['since', (metadata, value) => { metadata.since = value || undefined; }],
			['deprecated-since', (metadata, value) => { if (metadata.deprecated) metadata.deprecated.deprecatedSince = value; }],
			['removed-in', (metadata, value) => { if (metadata.deprecated) metadata.deprecated.willBeRemovedIn = value; }],
			['replaced-by', (metadata, value) => { if (metadata.deprecated) metadata.deprecated.replacedBy = value || undefined; }],
			['rule-areas', (metadata, value) => { metadata.ruleAreas = parseList(value); }],
			['choice-values', (metadata, value) => { if (metadata.choice) metadata.choice.values = value.split('\n').map(item => item.trim()).filter(Boolean); }],
		];
		for (const [suffix, mutate] of textBindings) {
			const attribute = `${prefix}-${suffix}`;
			document.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>(`[data-${attribute}]`).forEach(input => {
				input.dataset.metadataAttribute = attribute;
				input.addEventListener('change', () => edit(input, metadata => mutate(metadata, input.value)));
			});
		}
	}
	const writeProtectionBindings: [string, keyof Field, keyof Field][] = [
		['field', 'writeProtected', 'writeProtectedScopes'],
		['association', 'associationWriteProtected', 'associationWriteProtectedScopes'],
		['translation', 'translationWriteProtected', 'translationWriteProtectedScopes'],
		['hierarchy-children', 'hierarchyChildrenWriteProtected', 'hierarchyChildrenWriteProtectedScopes'],
		['hierarchy-version', 'hierarchyVersionWriteProtected', 'hierarchyVersionWriteProtectedScopes'],
	];
	for (const [prefix, enabledKey, scopesKey] of writeProtectionBindings) {
		document.querySelectorAll<HTMLInputElement>(`[data-${prefix}-write-protected]`).forEach(input => input.addEventListener('change', () => {
			const item = field(input.dataset[toDataset(`${prefix}-write-protected`)]!);
			(item as unknown as Record<string, unknown>)[enabledKey] = input.checked;
			if (!input.checked) (item as unknown as Record<string, unknown>)[scopesKey] = undefined;
			changed(true);
		}));
		document.querySelectorAll<HTMLInputElement>(`[data-${prefix}-write-scopes]`).forEach(input => input.addEventListener('change', () => {
			const item = field(input.dataset[toDataset(`${prefix}-write-scopes`)]!);
			(item as unknown as Record<string, unknown>)[scopesKey] = parseScopeList(input.value);
			changed();
		}));
	}
	document.querySelectorAll<HTMLInputElement>('[data-translation-association-api]').forEach(input => input.addEventListener('change', () => {
		if (!spec?.translation) return;
		spec.translation.associationApiAware = input.checked;
		if (!input.checked) spec.translation.associationApiAwareSources = undefined;
		changed(true);
	}));
	document.querySelectorAll<HTMLInputElement>('[data-translation-association-api-sources]').forEach(input => input.addEventListener('change', () => {
		if (!spec?.translation) return;
		spec.translation.associationApiAwareSources = parseScopeList(input.value);
		changed();
	}));
	document.querySelectorAll<HTMLInputElement>('[data-translation-association-write-protected]').forEach(input => input.addEventListener('change', () => {
		if (!spec?.translation) return;
		spec.translation.associationWriteProtected = input.checked;
		if (!input.checked) spec.translation.associationWriteProtectedScopes = undefined;
		changed(true);
	}));
	document.querySelectorAll<HTMLInputElement>('[data-translation-association-write-scopes]').forEach(input => input.addEventListener('change', () => {
		if (!spec?.translation) return;
		spec.translation.associationWriteProtectedScopes = parseScopeList(input.value);
		changed();
	}));
	for (const flag of ['runtime', 'computed'] as const) {
		document.querySelectorAll<HTMLInputElement>(`[data-translation-association-${flag}]`).forEach(input => input.addEventListener('change', () => {
			if (!spec?.translation) return;
			const behavior = (spec.translation.associationBehavior ??= {});
			behavior[flag] = input.checked;
			if (flag === 'runtime' && !input.checked) { behavior.runtimeDependencies = undefined; behavior.runtimeDependenciesExpression = undefined; }
			if (!behavior.runtime && !behavior.computed && !behavior.noConstraint) spec.translation.associationBehavior = undefined;
			changed(true);
		}));
	}
	document.querySelectorAll<HTMLInputElement>('[data-translation-association-runtime-dependencies]').forEach(input => input.addEventListener('change', () => {
		if (!spec?.translation) return;
		const behavior = (spec.translation.associationBehavior ??= {runtime: true});
		behavior.runtimeDependencies = parseList(input.value);
		changed();
	}));
	const editTranslationAssociationMetadata = (mutate: (metadata: Metadata) => void, rerender = false): void => {
		if (!spec?.translation) return;
		const metadata = (spec.translation.associationMetadata ??= {});
		mutate(metadata);
		if (metadataIsEmpty(metadata)) spec.translation.associationMetadata = undefined;
		changed(rerender);
	};
	const translationMetadataChecks: [string, (metadata: Metadata, checked: boolean) => void, boolean?][] = [
		['as-array', (metadata, checked) => { metadata.asArray = checked; }],
		['immutable', (metadata, checked) => { metadata.immutable = checked; }],
		['api-criteria-aware', (metadata, checked) => { metadata.apiCriteriaAware = checked; }],
		['ignore-openapi', (metadata, checked) => { metadata.ignoreInOpenapiSchema = checked; }],
		['ignore-unused-media', (metadata, checked) => { metadata.ignoreInUnusedMediaSearch = checked; }],
		['do-not-use-context', (metadata, checked) => { metadata.doNotUseContext = checked; }],
		['deprecated', (metadata, checked) => { metadata.deprecated = checked ? {deprecatedSince: '', willBeRemovedIn: ''} : undefined; }, true],
		['choice-strict', (metadata, checked) => { if (metadata.choice) metadata.choice.strict = checked; }],
	];
	for (const [suffix, mutate, rerender] of translationMetadataChecks) document.querySelectorAll<HTMLInputElement>(`[data-translation-association-${suffix}]`).forEach(input => input.addEventListener('change', () => editTranslationAssociationMetadata(metadata => mutate(metadata, input.checked), rerender)));
	const translationMetadataTexts: [string, (metadata: Metadata, value: string) => void][] = [
		['since', (metadata, value) => { metadata.since = value || undefined; }],
		['deprecated-since', (metadata, value) => { if (metadata.deprecated) metadata.deprecated.deprecatedSince = value; }],
		['removed-in', (metadata, value) => { if (metadata.deprecated) metadata.deprecated.willBeRemovedIn = value; }],
		['replaced-by', (metadata, value) => { if (metadata.deprecated) metadata.deprecated.replacedBy = value || undefined; }],
		['rule-areas', (metadata, value) => { metadata.ruleAreas = parseList(value); }],
		['choice-values', (metadata, value) => { if (metadata.choice) metadata.choice.values = value.split('\n').map(item => item.trim()).filter(Boolean); }],
	];
	for (const [suffix, mutate] of translationMetadataTexts) document.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>(`[data-translation-association-${suffix}]`).forEach(input => input.addEventListener('change', () => editTranslationAssociationMetadata(metadata => mutate(metadata, input.value))));
  document.querySelectorAll<HTMLInputElement>('[data-hierarchy-children-api]').forEach(input => input.addEventListener('change', () => { const item = field(input.dataset.hierarchyChildrenApi!); item.hierarchyChildrenApiAware = input.checked; if (!input.checked) item.hierarchyChildrenApiAwareSources = undefined; changed(true); }));
  document.querySelectorAll<HTMLInputElement>('[data-hierarchy-version-api]').forEach(input => input.addEventListener('change', () => { const item = field(input.dataset.hierarchyVersionApi!); item.hierarchyVersionApiAware = input.checked; if (!input.checked) item.hierarchyVersionApiAwareSources = undefined; changed(true); }));
  document.querySelectorAll<HTMLInputElement>('[data-inherited]').forEach(input => input.addEventListener('change', () => { const item = field(input.dataset.inherited!); item.inherited = input.checked; if (!input.checked) item.inheritedForeignKey = undefined; changed(true); }));
  document.querySelectorAll<HTMLInputElement>('[data-association-inherited]').forEach(input => input.addEventListener('change', () => { const item = field(input.dataset.associationInherited!); item.associationInherited = input.checked; if (!input.checked) item.associationInheritedForeignKey = undefined; changed(true); }));
  document.querySelectorAll<HTMLInputElement>('[data-translation-inherited]').forEach(input => input.addEventListener('change', () => { const item = field(input.dataset.translationInherited!); item.translationInherited = input.checked; if (!input.checked) item.translationInheritedForeignKey = undefined; changed(true); }));
  document.querySelectorAll<HTMLInputElement>('[data-translated]').forEach(input => input.addEventListener('change', () => {
    const item = field(input.dataset.translated!);
    item.translated = input.checked;
	if (input.checked) {
		spec!.translation ??= {enabled: true, associationRequired: true};
		spec!.translation.enabled = true;
	}
	if (!input.checked) {
		item.translationWriteProtected = false;
		item.translationWriteProtectedScopes = undefined;
		item.translationInherited = false;
		item.translationInheritedForeignKey = undefined;
		if (!activeFields().some(candidate => candidate.translated) && spec!.translation) spec!.translation.enabled = false;
	}
    changed(true);
  }));
  document.querySelectorAll<HTMLInputElement>('[data-translation-sorting]').forEach(input => input.addEventListener('change', () => { field(input.dataset.translationSorting!).translationUseForSorting = input.checked; changed(); }));
  document.querySelectorAll<HTMLInputElement>('[data-translation-api]').forEach(input => input.addEventListener('change', () => { const item = field(input.dataset.translationApi!); item.translationApiAware = input.checked; if (!input.checked) item.translationApiAwareSources = undefined; changed(true); }));
  document.querySelectorAll<HTMLInputElement>('[data-existing-column]').forEach(input => input.addEventListener('change', () => { const item = field(input.dataset.existingColumn!); item.usesExistingColumn = input.checked; if (input.checked) item.required = false; changed(true); }));
  document.querySelectorAll<HTMLSelectElement>('[data-delete]').forEach(input => input.addEventListener('change', () => { const item = field(input.dataset.delete!); item.deleteBehavior = input.value; item.deleteCloneRelevant = undefined; item.deleteEnforcedByConstraint = undefined; changed(true); }));
	document.querySelectorAll<HTMLInputElement>('[data-delete-clone-relevant]').forEach(input => input.addEventListener('change', () => { field(input.dataset.deleteCloneRelevant!).deleteCloneRelevant = input.checked; changed(); }));
	document.querySelectorAll<HTMLInputElement>('[data-delete-enforced-by-constraint]').forEach(input => input.addEventListener('change', () => { field(input.dataset.deleteEnforcedByConstraint!).deleteEnforcedByConstraint = input.checked; changed(); }));
  document.querySelectorAll<HTMLElement>('[data-select-field]').forEach(button => button.addEventListener('click', () => { selectedFieldId = button.dataset.selectField!; persistState(); render(); }));
  document.querySelectorAll<HTMLElement>('[data-remove]').forEach(button => button.addEventListener('click', () => { const removed = button.dataset.remove!; replaceActiveFields(activeFields().filter(item => item.id !== removed)); if (selectedFieldId === removed) selectedFieldId = activeFields()[0]?.id ?? ''; changed(true); }));
  document.querySelectorAll<HTMLElement>('[data-up],[data-down]').forEach(button => button.addEventListener('click', () => { const id = button.dataset.up ?? button.dataset.down!; const fields = activeFields(); const index = fields.findIndex(item => item.id === id); const target = button.dataset.up ? index - 1 : index + 1; [fields[index], fields[target]] = [fields[target], fields[index]]; changed(true); }));
  document.querySelectorAll<HTMLElement>('[data-relation]').forEach(button => button.addEventListener('click', () => openRelationDialog(button.dataset.relation!)));
  document.querySelectorAll<HTMLElement>('[data-mapping]').forEach(button => button.addEventListener('click', () => openRelationDialog(button.dataset.mapping!, 'mapping')));
}

function addField(typeID: string): void {
	const type = availableFieldTypes().find(candidate => fieldTypeValue(candidate) === typeID);
	const kind = type?.kind ?? typeID;
	const id = `${kind}-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
	const item: Field = type?.template ? {...structuredClone(type.template), id, editable: true} : {id, kind, propertyName: '', storageName: '', editable: true};
	applyFieldDefaults(item); activeFields().push(item); selectedFieldId = item.id; changed(true);
}

function changeFieldKind(item: Field, typeID: string): void {
	const type = availableFieldTypes(item.kind).find(candidate => fieldTypeValue(candidate) === typeID);
	const kind = type?.kind ?? typeID;
	const translated = isTranslatable(kind) ? item.translated : false;
	const preserved = {id: item.id, kind, propertyName: item.propertyName, storageName: item.storageName, required: item.required, primary: item.primary, apiAware: item.apiAware, translated, editable: item.editable};
	for (const key of Object.keys(item) as (keyof Field)[]) delete item[key];
	Object.assign(item, type?.template ? structuredClone(type.template) : {}, preserved);
	applyFieldDefaults(item);
}

function fieldTypeValue(type: Bootstrap['fieldTypes'][number]): string { return type.id ?? type.kind; }

function fieldTypeID(field: Field): string {
	if (field.implementation?.class) {
		const match = bootstrap?.fieldTypes.find(type => type.template?.implementation?.class === field.implementation?.class);
		if (match) return fieldTypeValue(match);
	}
	return field.kind;
}

function applyFieldDefaults(item: Field): void {
  const mapping = spec?.definitionKind === 'mapping';
  if (item.kind === 'foreign-key') Object.assign(item, {referenceField: 'id', referenceStorageName: 'id', required: mapping, primary: mapping});
  if (item.kind === 'created-at') Object.assign(item, {propertyName: 'createdAt', storageName: 'created_at', required: true, migrationDefault: 'CURRENT_TIMESTAMP(3)'});
  if (item.kind === 'updated-at') Object.assign(item, {propertyName: 'updatedAt', storageName: 'updated_at', required: false});
  if (item.kind === 'auto-increment') Object.assign(item, {propertyName: 'autoIncrement', storageName: 'auto_increment', required: true});
  if (item.kind === 'version') Object.assign(item, {propertyName: 'versionId', storageName: 'version_id', required: true, primary: true});
  if (item.kind === 'string' && !item.maxLength) item.maxLength = 255;
  if (item.kind === 'enum' && !item.enumBackingType) item.enumBackingType = 'string';
  if (item.kind === 'many-to-one' || item.kind === 'one-to-one') Object.assign(item, {
    referenceField: 'id', referenceStorageName: 'id',
    required: mapping && item.kind === 'many-to-one' ? true : item.required,
    primary: mapping && item.kind === 'many-to-one' ? true : item.primary,
    deleteBehavior: (mapping && item.kind === 'many-to-one') || item.required ? 'restrict' : 'set-null',
  });
  if (item.kind === 'one-to-many') Object.assign(item, {storageName: undefined, sourceColumn: 'id', deleteBehavior: 'restrict'});
  if (item.kind === 'many-to-many') Object.assign(item, {storageName: undefined, referenceField: 'id', sourceColumn: 'id'});
  if (item.kind === 'hierarchy') Object.assign(item, {propertyName: 'children', hierarchyParentProperty: 'parent', foreignKeyPropertyName: 'parentId', storageName: 'parent_id', referenceField: 'id', referenceStorageName: 'id', deleteBehavior: 'cascade', required: false, primary: false});
	if (isExtensionKind(spec?.definitionKind) && extensionFieldRequiresRuntime(item)) {
		item.behavior = {...item.behavior, runtime: true, noConstraint: undefined};
		item.required = false;
		item.primary = false;
		item.migrationDefault = undefined;
	}
}

function extensionFieldRequiresRuntime(field: Field): boolean {
	return !['reference-version', 'many-to-one', 'one-to-one', 'one-to-many', 'many-to-many', 'locked'].includes(field.kind);
}

function openRelationDialog(fieldId: string, role: 'target' | 'mapping' | 'extension' | 'bulk-extension-add' | 'bulk-extension-change' = 'target'): void {
  relationField = fieldId; relationRole = role; relationResults = bootstrap?.existing ?? []; renderRelationDialog();
  if (role === 'extension' || role.startsWith('bulk-extension')) {
    relationBusy = true;
    relationRequestId++;
    renderRelationDialog();
    vscode.postMessage({type: 'search', requestId: relationRequestId, query: ''});
  }
}

function renderRelationDialog(): void {
  const dialog = document.getElementById('relationDialog') as HTMLDialogElement | null;
  const content = document.getElementById('relationContent');
  if (!dialog || !content) return;
  const title = relationRole === 'mapping' ? 'Mapping definition' : relationRole === 'extension' ? 'Extended entity' : relationRole.startsWith('bulk-extension') ? 'Bulk extension target' : 'Relation target';
  content.innerHTML = `<div class="toolbar"><h2 style="margin-right:auto">${title}</h2><input id="relationQuery" placeholder="Search technical name or PHP class"><button id="relationSearch" ${relationBusy ? 'disabled' : ''}>${relationBusy ? 'Searching…' : 'Search'}</button><button class="secondary" id="relationClose">Close</button></div><div>${relationResults.map((target, index) => `<button class="secondary relation-result" data-target="${index}"><b>${esc(target.entityName)}</b><br><span class="muted">${esc(target.definitionClass)}</span></button>`).join('') || '<p class="muted">No indexed entities found.</p>'}</div>`;
  const search = (): void => { const query = (content.querySelector('#relationQuery') as HTMLInputElement | null)?.value ?? ''; relationBusy = true; relationRequestId++; renderRelationDialog(); vscode.postMessage({type: 'search', requestId: relationRequestId, query}); };
  content.querySelector('#relationSearch')?.addEventListener('click', search);
  content.querySelector('#relationQuery')?.addEventListener('keydown', event => { if ((event as KeyboardEvent).key === 'Enter') search(); });
  content.querySelector('#relationClose')?.addEventListener('click', () => dialog.close());
  content.querySelectorAll<HTMLElement>('[data-target]').forEach(button => button.addEventListener('click', () => {
    const target = relationResults[Number(button.dataset.target)];
    if (relationRole === 'extension') {
      spec!.extendedDefinitionClass = target.definitionClass;
      spec!.entityName = target.entityName;
		spec!.extendedFields = target.fields?.map(field => ({...field}));
      dialog.close(); changed(true); return;
    }
	if (relationRole === 'bulk-extension-add') {
		if ((spec!.bulkExtensions ?? []).some(item => item.extendedDefinitionClass === target.definitionClass || item.entityName === target.entityName)) {
			dialog.close(); return;
		}
		const id = `bulk-${target.entityName}-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
		(spec!.bulkExtensions ??= []).push({id, entityName: target.entityName, extendedDefinitionClass: target.definitionClass, extendedFields: target.fields?.map(field => ({...field})), fields: [], indexes: []});
		selectedBulkTargetId = id; selectedFieldId = '';
		dialog.close(); changed(true); return;
	}
	if (relationRole === 'bulk-extension-change') {
		const bulkTarget = selectedBulkTarget();
		if (!bulkTarget) { dialog.close(); return; }
		bulkTarget.entityName = target.entityName;
		bulkTarget.extendedDefinitionClass = target.definitionClass;
		bulkTarget.extendedFields = target.fields?.map(field => ({...field}));
		dialog.close(); changed(true); return;
	}
    const item = activeFields().find(field => field.id === relationField)!;
    if (relationRole === 'mapping') {
      item.mappingDefinitionClass = target.definitionClass;
      dialog.close(); changed(true); return;
    }
    const primary = target.fields?.find(field => field.primary);
    Object.assign(item, {targetDefinitionClass: target.definitionClass, targetEntityClass: target.entityClass, targetCollectionClass: target.collectionClass, targetEntityName: target.entityName, referenceField: primary?.propertyName || 'id', referenceStorageName: primary?.storageName || 'id'});
    if (item.kind === 'reference-version') Object.assign(item, {propertyName: `${camel(target.entityName)}VersionId`, storageName: `${target.entityName}_version_id`});
    if ((item.kind === 'foreign-key' || item.kind === 'many-to-one' || item.kind === 'one-to-one') && !item.propertyName) item.propertyName = item.kind === 'foreign-key' ? `${camel(target.entityName)}Id` : camel(target.entityName);
    if (item.kind === 'foreign-key') item.storageName ||= `${target.entityName}_id`;
    if (item.kind === 'many-to-one' || item.kind === 'one-to-one') { item.foreignKeyPropertyName ||= `${item.propertyName}Id`; item.storageName ||= `${snake(item.propertyName)}_id`; }
    if ((item.kind === 'foreign-key' || item.kind === 'many-to-one' || item.kind === 'one-to-one') && !item.usesExistingColumn && target.versionAware) ensureReferenceVersion(target, item.required ?? false);
    dialog.close(); changed(true);
  }));
  if (!dialog.open) dialog.showModal();
}

function isRelation(kind: string): boolean { return ['reference-version', 'foreign-key', 'many-to-one', 'one-to-one', 'one-to-many', 'many-to-many'].includes(kind); }
function isAssociation(kind: string): boolean { return ['many-to-one', 'one-to-one', 'one-to-many', 'many-to-many', 'hierarchy'].includes(kind); }
function isExtensionKind(kind?: DefinitionKind): boolean { return kind === 'extension' || kind === 'bulk-extension'; }

function changeDefinitionKind(kind: DefinitionKind): void {
	if (!spec || (spec.definitionKind ?? 'entity') === kind) return;
	if (spec.collectMethodRaw || definitionHasLockedMethods(spec.definitionBehavior, spec.definitionMetadata) || definitionHasLockedMethods(spec.translation?.definitionBehavior, spec.translation?.definitionMetadata)) return;
	const previousKind = spec.definitionKind ?? 'entity';
	const editing = spec.mode === 'edit';
	const previousFields = structuredClone(activeFields());
	const previousIndexes = structuredClone(activeIndexes());
	const previousBulkTarget = structuredClone(selectedBulkTarget());
	const previousExtensionTarget = previousKind === 'extension' && spec.extendedDefinitionClass ? {
		id: `bulk-${spec.entityName || 'target'}-${Date.now()}`,
		entityName: spec.entityName,
		extendedDefinitionClass: spec.extendedDefinitionClass,
		extendedFields: structuredClone(spec.extendedFields),
		fields: previousFields,
		indexes: previousIndexes,
	} satisfies BulkExtensionTarget : undefined;

	spec.definitionKind = kind;
	if (kind !== 'extension') { spec.fieldModifications = undefined; spec.modifyFieldsMethodRaw = undefined; }
	if (!editing) { spec.definitionClass = undefined; spec.definitionUri = undefined; }
	if (!editing) {
		spec.entityClass = undefined; spec.collectionClass = undefined; spec.entityUri = undefined; spec.collectionUri = undefined;
	}
	if (editing && previousKind === 'entity' && spec.translation) spec.translation.enabled = false;
	else spec.translation = undefined;
	spec.inheritanceAware = false;
	if (kind === 'mapping') {
		if (spec.definitionBehavior) {
			spec.definitionBehavior.overrideDefaultFields = undefined;
			spec.definitionBehavior.defaultFields = undefined;
			spec.definitionBehavior.defaultFieldsMethodRaw = undefined;
			spec.definitionBehavior.inheritanceAwareMethodRaw = undefined;
		}
		if (spec.definitionMetadata) {
			spec.definitionMetadata.childDefaults = undefined;
			spec.definitionMetadata.childDefaultsMethodRaw = undefined;
			spec.definitionMetadata.hydratorClass = undefined;
			spec.definitionMetadata.hydratorMethodRaw = undefined;
		}
	}
	if (kind === 'extension' || kind === 'bulk-extension') {
		spec.definitionBehavior = undefined;
		spec.definitionMetadata = undefined;
	}
	if (kind === 'mapping' || kind === 'bulk-extension') {
		spec.readProtected = false; spec.readProtectionScopes = undefined;
		spec.writeProtected = false; spec.writeProtectionScopes = undefined;
		spec.preservedProtections = undefined; spec.protectionMethodRaw = undefined;
	}

	if (kind === 'bulk-extension') {
		spec.bulkExtensions = previousExtensionTarget ? [previousExtensionTarget] : [];
		spec.fields = [];
		spec.indexes = [];
		spec.entityName = '';
		spec.extendedDefinitionClass = undefined;
		spec.extendedFields = undefined;
		selectedBulkTargetId = spec.bulkExtensions[0]?.id ?? '';
		for (const field of activeFields()) {
			field.translated = false;
			if (field.editable) applyFieldDefaults(field);
		}
		selectedFieldId = activeFields()[0]?.id ?? '';
		changed(true);
		return;
	}

	if (previousKind === 'bulk-extension') {
		spec.fields = previousFields;
		spec.indexes = previousIndexes;
		if (kind === 'extension' && previousBulkTarget) {
			spec.entityName = previousBulkTarget.entityName;
			spec.extendedDefinitionClass = previousBulkTarget.extendedDefinitionClass;
			spec.extendedFields = previousBulkTarget.extendedFields;
		} else {
			spec.entityName = '';
			spec.extendedDefinitionClass = undefined;
			spec.extendedFields = undefined;
		}
	}
	spec.bulkExtensions = undefined;
	selectedBulkTargetId = '';
	for (const field of spec.fields) field.translated = false;
	const supported = new Set((bootstrap?.fieldTypes ?? []).filter(type => !type.definitionKinds?.length || type.definitionKinds.includes(kind)).map(type => type.kind));
	spec.fields = spec.fields.filter(field => !field.editable || supported.has(field.kind));
	if (kind === 'entity') {
		spec.extendedDefinitionClass = undefined;
		spec.extendedFields = undefined;
		if (previousKind === 'extension') spec.entityName = '';
		if (!spec.fields.some(field => field.kind === 'id')) spec.fields.unshift({id: 'id', kind: 'id', propertyName: 'id', storageName: 'id', required: true, primary: true, editable: true});
	} else {
		spec.fields = spec.fields.filter(field => field.kind !== 'id');
		if (kind === 'mapping') {
			spec.extendedDefinitionClass = undefined;
			spec.extendedFields = undefined;
			if (previousKind === 'extension') spec.entityName = '';
		}
		if (kind === 'extension') {
			if (previousKind !== 'bulk-extension') {
				spec.entityName = '';
				spec.extendedFields = undefined;
				spec.indexes = [];
			}
			for (const field of spec.fields) if (field.editable) applyFieldDefaults(field);
		}
	}
	selectedFieldId = spec.fields[0]?.id ?? '';
	changed(true);
}

function clearInheritedFlags(): void {
	if (!spec) return;
	const fields = [...spec.fields, ...(spec.bulkExtensions ?? []).flatMap(target => target.fields)];
	for (const field of fields) {
		field.inherited = false; field.inheritedForeignKey = undefined;
		field.associationInherited = false; field.associationInheritedForeignKey = undefined;
		field.translationInherited = false; field.translationInheritedForeignKey = undefined;
		field.hierarchyChildrenInherited = false; field.hierarchyChildrenInheritedForeignKey = undefined;
		field.hierarchyVersionInherited = false; field.hierarchyVersionInheritedForeignKey = undefined;
	}
	if (spec.translation) {
		spec.translation.associationInherited = false;
		spec.translation.associationInheritedForeignKey = undefined;
	}
}

function availableFieldTypes(currentKind?: string): Bootstrap['fieldTypes'] {
  if (!bootstrap || !spec) return [];
  const kind = spec.definitionKind ?? 'entity';
  return bootstrap.fieldTypes.filter(type => {
	  if (type.kind === currentKind) return true;
	  if (type.definitionKinds?.length && !type.definitionKinds.includes(kind)) return false;
	  return kind !== 'entity' || !type.requiresDefaultFieldsOverride || Boolean(spec?.definitionBehavior?.overrideDefaultFields);
  });
}

function availableDefinitionKinds(): DefinitionKind[] {
	// Older custom server binaries predate the explicit capability. Keep their
	// long-standing three modes and do not advertise bulk generation that they
	// cannot validate or render.
	return bootstrap?.definitionKinds?.length
		? bootstrap.definitionKinds
		: ['entity', 'mapping', 'extension'];
}

function definitionKindLabel(kind: DefinitionKind): string {
	switch (kind) {
		case 'mapping': return 'Mapping definition';
		case 'extension': return 'Entity extension';
		case 'bulk-extension': return 'Bulk entity extension';
		default: return 'Entity definition';
	}
}

function ensureReferenceVersion(target: Target, required: boolean): void {
	const fields = activeFields();
	const existing = fields.find(item => item.kind === 'reference-version' && item.targetDefinitionClass === target.definitionClass);
  if (existing) { if (required) existing.required = true; return; }
	fields.push({id: `reference-version-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`, kind: 'reference-version', propertyName: `${camel(target.entityName)}VersionId`, storageName: `${target.entityName}_version_id`, targetDefinitionClass: target.definitionClass, targetEntityClass: target.entityClass, targetCollectionClass: target.collectionClass, targetEntityName: target.entityName, required, editable: true});
}

function ensureReferenceVersionRequired(targetDefinitionClass: string, required: boolean): void {
  if (!required) return;
	const version = activeFields().find(item => item.kind === 'reference-version' && item.targetDefinitionClass === targetDefinitionClass);
  if (version) version.required = true;
}

function storedColumns(): string[] {
  if (!spec) return [];
	const columns = activeFields().filter(field => !field.translated && !isNonStored(field) && field.kind !== 'locked' && !field.behavior?.runtime && Boolean(field.storageName)).map(field => field.storageName!);
	if ((spec.definitionKind ?? 'entity') === 'entity') {
		if (spec.definitionBehavior?.overrideDefaultFields) columns.push(...(spec.definitionBehavior.defaultFields ?? []).filter(field => !isNonStored(field) && !field.behavior?.runtime && Boolean(field.storageName)).map(field => field.storageName!));
		if (spec.definitionBehavior?.overrideBaseFields) columns.push(...(spec.definitionBehavior.baseFields ?? []).filter(field => !isNonStored(field) && !field.behavior?.runtime && Boolean(field.storageName)).map(field => field.storageName!));
		else columns.push('created_at', 'updated_at');
	}
	if (isExtensionKind(spec.definitionKind)) columns.push(...activeExtendedFields().map(field => field.storageName));
	return Array.from(new Set(columns));
}

function translationColumns(): string[] {
	if (!spec?.translation?.enabled) return [];
	const parentStorage = spec.translation.parentStorageName || `${spec.entityName}_id`;
	const columns = spec.fields.filter(field => field.translated && field.kind !== 'locked' && !field.behavior?.runtime && Boolean(field.storageName)).map(field => field.storageName!);
	columns.push(parentStorage, 'language_id');
	if (spec.translation.definitionBehavior?.overrideDefaultFields) columns.push(...(spec.translation.definitionBehavior.defaultFields ?? []).filter(field => !isNonStored(field) && !field.behavior?.runtime && Boolean(field.storageName)).map(field => field.storageName!));
	if (spec.translation.definitionBehavior?.overrideBaseFields) columns.push(...(spec.translation.definitionBehavior.baseFields ?? []).filter(field => !isNonStored(field) && !field.behavior?.runtime && Boolean(field.storageName)).map(field => field.storageName!));
	else columns.push('created_at', 'updated_at');
	if (spec.fields.some(field => field.kind === 'version')) columns.push(`${parentStorage.replace(/_id$/, '')}_version_id`);
	return Array.from(new Set(columns));
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

function entityRenameDecisionValue(added: string): string {
  const decision = decisions.find(item => (item.kind === 'entityCreate' || item.kind === 'entityRename') && item.to === added);
  return decision?.kind === 'entityCreate' ? 'create' : decision?.from ?? '';
}

function isNonStored(field: Field): boolean { return field.kind === 'one-to-many' || field.kind === 'many-to-many' || (field.kind === 'one-to-one' && Boolean(field.usesExistingColumn)); }
function isTranslatable(kind: string): boolean { return ['binary-id', 'string', 'enum', 'long-text', 'int', 'float', 'bool', 'date', 'datetime', 'json', 'list', 'object', 'blob'].includes(kind); }

function persistState(applied = false): void { vscode.setState({spec, decisions, driftDecision, selectedFieldId, selectedBulkTargetId, applied} satisfies PersistedState); }
function changed(rerender = false): void { syncDerivedFields(); preview = undefined; appliedRevision = ''; destructiveConfirmed = false; errorMessage = ''; successMessage = ''; if (rerender) render(); schedulePreview(); persistState(); }
function syncDerivedFields(): void {
  if (!spec) return;
	const fields = activeFields();
	const versionAware = fields.some(field => field.kind === 'version');
	for (const field of fields) if (field.kind === 'hierarchy') field.hierarchyVersionAware = versionAware;
}
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
