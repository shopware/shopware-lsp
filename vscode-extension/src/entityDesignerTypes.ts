import type {WorkspaceEdit as ProtocolWorkspaceEdit} from 'vscode-languageserver-protocol';

export interface EntityFieldBehavior {
  runtime?: boolean;
  runtimeDependencies?: string[];
  runtimeDependenciesExpression?: string;
  computed?: boolean;
  noConstraint?: boolean;
}

export interface EntityFieldImplementation {
  class: string;
  constructorMode: 'storage-property' | 'fixed';
  additionalArguments?: string[];
  fixedArguments?: string[];
  fixedStorageName?: string;
  fixedPropertyName?: string;
  entityType?: string;
  entityBooleanGetter?: boolean;
  entityTrait?: string;
  manageEntity?: boolean;
  implicitComputed?: boolean;
  maxLengthArgument?: boolean;
  minimumAdditionalArguments?: number;
}

export interface EntityFieldMetadata {
  allowHtmlSanitized?: boolean;
  allowEmptyString?: boolean;
  asArray?: boolean;
  immutable?: boolean;
  since?: string;
  deprecated?: {deprecatedSince: string; willBeRemovedIn: string; replacedBy?: string};
  ignoreInOpenapiSchema?: boolean;
  ignoreInUnusedMediaSearch?: boolean;
  apiCriteriaAware?: boolean;
  ruleAreas?: string[];
  choice?: {values: string[]; strict?: boolean};
  doNotUseContext?: boolean;
  extension?: boolean;
}

export type EntityFieldFlagKind =
  | 'required' | 'primary-key' | 'api-aware' | 'search-ranking'
  | 'cascade-delete' | 'set-null-on-delete' | 'restrict-delete'
  | 'runtime' | 'computed' | 'no-constraint' | 'inherited'
  | 'reverse-inherited' | 'write-protected' | 'allow-html'
  | 'allow-empty-string' | 'as-array' | 'immutable' | 'since'
  | 'deprecated' | 'ignore-in-openapi-schema'
  | 'ignore-in-unused-media-search' | 'api-criteria-aware'
  | 'rule-areas' | 'choice' | 'do-not-use-context' | 'extension';

export interface EntityFieldFlagSpec {
  kind: EntityFieldFlagKind;
  apiSources?: string[];
  searchRanking?: number;
  searchTokenize?: boolean;
  runtimeDependencies?: string[];
  runtimeDependenciesExpression?: string;
  inheritedForeignKey?: string;
  reverseProperty?: string;
  writeScopes?: string[];
  allowHtmlSanitized?: boolean;
  cloneRelevant?: boolean;
  enforcedByConstraint?: boolean;
  since?: string;
  deprecated?: {deprecatedSince: string; willBeRemovedIn: string; replacedBy?: string};
  ruleAreas?: string[];
  choice?: {values: string[]; strict?: boolean};
}

export interface EntityFieldModification {
  id: string;
  propertyName: string;
  addFlags?: EntityFieldFlagSpec[];
  removeFlags?: EntityFieldFlagKind[];
}

export type EntityDefinitionKind = 'entity' | 'mapping' | 'extension' | 'bulk-extension';

export interface EntityField {
  id: string;
  kind: string;
  propertyName: string;
  foreignKeyPropertyName?: string;
  storageName?: string;
  required?: boolean;
  primary?: boolean;
  apiAware?: boolean;
  apiAwareSources?: string[];
  searchRanking?: number;
  searchRankingTokenize?: boolean;
  behavior?: EntityFieldBehavior;
  implementation?: EntityFieldImplementation;
  metadata?: EntityFieldMetadata;
  writeProtected?: boolean;
  writeProtectedScopes?: string[];
  inherited?: boolean;
  inheritedForeignKey?: string;
  preservedFlags?: string[];
  modifiersBeforeFlags?: string[];
  modifiersAfterFlags?: string[];
  associationFlags?: string[];
  associationModifiersBeforeFlags?: string[];
  associationModifiersAfterFlags?: string[];
  maxLength?: number;
  min?: number;
  max?: number;
  elementTypeClass?: string;
  jsonPropertyMappingExpression?: string;
  jsonDefaultExpression?: string;
  enumClass?: string;
  enumCase?: string;
  enumBackingType?: 'string' | 'int';
  targetDefinitionClass?: string;
  targetEntityClass?: string;
  targetCollectionClass?: string;
  targetEntityName?: string;
  referenceField?: string;
  referenceStorageName?: string;
  mappingDefinitionClass?: string;
  mappingLocalColumn?: string;
  mappingReferenceColumn?: string;
  sourceColumn?: string;
  usesExistingColumn?: boolean;
  deleteBehavior?: string;
  deleteCloneRelevant?: boolean;
  deleteEnforcedByConstraint?: boolean;
  associationApiAware?: boolean;
  associationApiAwareSources?: string[];
  associationSearchRanking?: number;
  associationSearchRankingTokenize?: boolean;
  associationBehavior?: EntityFieldBehavior;
  associationMetadata?: EntityFieldMetadata;
  associationAutoload?: boolean;
  conditionalAssociation?: {conditionExpression: string; alternativeKind: 'many-to-one' | 'one-to-one'; alternativeAutoload?: boolean};
  associationWriteProtected?: boolean;
  associationWriteProtectedScopes?: string[];
  associationInherited?: boolean;
  associationInheritedForeignKey?: string;
  reverseInheritedProperty?: string;
  hierarchyParentProperty?: string;
  hierarchyChildrenFlags?: string[];
  hierarchyChildrenModifiersBeforeFlags?: string[];
  hierarchyChildrenModifiersAfterFlags?: string[];
  hierarchyChildrenApiAware?: boolean;
  hierarchyChildrenApiAwareSources?: string[];
  hierarchyChildrenSearchRanking?: number;
  hierarchyChildrenSearchRankingTokenize?: boolean;
  hierarchyChildrenBehavior?: EntityFieldBehavior;
  hierarchyChildrenMetadata?: EntityFieldMetadata;
  hierarchyChildrenWriteProtected?: boolean;
  hierarchyChildrenWriteProtectedScopes?: string[];
  hierarchyChildrenInherited?: boolean;
  hierarchyChildrenInheritedForeignKey?: string;
  hierarchyChildrenReverseInheritedProperty?: string;
  hierarchyVersionAware?: boolean;
  hierarchyVersionApiAware?: boolean;
  hierarchyVersionApiAwareSources?: string[];
  hierarchyVersionWriteProtected?: boolean;
  hierarchyVersionWriteProtectedScopes?: string[];
  hierarchyVersionInherited?: boolean;
  hierarchyVersionInheritedForeignKey?: string;
  hierarchyVersionFlags?: string[];
  hierarchyVersionBehavior?: EntityFieldBehavior;
  hierarchyVersionMetadata?: EntityFieldMetadata;
  hierarchyVersionModifiersBeforeFlags?: string[];
  hierarchyVersionModifiersAfterFlags?: string[];
  migrationDefault?: string;
  translated?: boolean;
  translationDefinitionOnly?: boolean;
  translationUseForSorting?: boolean;
  translationApiAware?: boolean;
  translationApiAwareSources?: string[];
  translationSearchRanking?: number;
  translationSearchRankingTokenize?: boolean;
  translationBehavior?: EntityFieldBehavior;
  translationMetadata?: EntityFieldMetadata;
  translationWriteProtected?: boolean;
  translationWriteProtectedScopes?: string[];
  translationInherited?: boolean;
  translationInheritedForeignKey?: string;
  translationFlags?: string[];
  translationModifiersBeforeFlags?: string[];
  translationModifiersAfterFlags?: string[];
  editable: boolean;
  raw?: string;
}

export interface EntityIndex {
  name: string;
  kind: 'index' | 'unique';
  columns: string[];
  translation?: boolean;
}

export interface EntityBulkExtensionTarget {
  id: string;
  entityName: string;
  extendedDefinitionClass?: string;
  extendedFields?: {propertyName: string; storageName: string; primary?: boolean}[];
  fields: EntityField[];
  indexes?: EntityIndex[];
}

export interface EntityDefinitionDefault {
  propertyName: string;
  valueExpression: string;
}

export interface EntityDefinitionMetadata {
  since?: string;
  defaults?: EntityDefinitionDefault[];
  childDefaults?: EntityDefinitionDefault[];
  hydratorClass?: string;
  sinceMethodRaw?: string;
  defaultsMethodRaw?: string;
  childDefaultsMethodRaw?: string;
  hydratorMethodRaw?: string;
}

export interface EntityDefinitionBehavior {
  parentDefinitionClass?: string;
  versionAware?: boolean;
  overrideDefaultFields?: boolean;
  defaultFields?: EntityField[];
  overrideBaseFields?: boolean;
  baseFields?: EntityField[];
  restrictDeleteMetaProperties?: string[];
  parentDefinitionMethodRaw?: string;
  versionAwareMethodRaw?: string;
  inheritanceAwareMethodRaw?: string;
  defaultFieldsMethodRaw?: string;
  baseFieldsMethodRaw?: string;
  restrictDeleteMetaMethodRaw?: string;
}

export interface EntityTranslationSpec {
  enabled: boolean;
  entityName?: string;
  definitionClass?: string;
  entityClass?: string;
  collectionClass?: string;
  definitionUri?: string;
  entityUri?: string;
  collectionUri?: string;
  parentDefinitionClass?: string;
  parentStorageName?: string;
  parentPropertyName?: string;
  associationProperty?: string;
  associationLocalField?: string;
  associationRequired?: boolean;
  associationApiAware?: boolean;
  associationApiAwareSources?: string[];
  associationBehavior?: EntityFieldBehavior;
  associationMetadata?: EntityFieldMetadata;
  associationWriteProtected?: boolean;
  associationWriteProtectedScopes?: string[];
  associationInherited?: boolean;
  associationInheritedForeignKey?: string;
  reverseInheritedProperty?: string;
  associationFlags?: string[];
  associationModifiersBeforeFlags?: string[];
  associationModifiersAfterFlags?: string[];
  definitionBehavior?: EntityDefinitionBehavior;
  definitionMetadata?: EntityDefinitionMetadata;
}

export interface EntitySpec {
  mode: string;
  definitionKind?: EntityDefinitionKind;
  pluginRootUri: string;
  directoryUri: string;
  namespace: string;
  className: string;
  entityName: string;
  extendedDefinitionClass?: string;
  extendedFields?: {propertyName: string; storageName: string; primary?: boolean}[];
  inheritanceAware?: boolean;
  definitionBehavior?: EntityDefinitionBehavior;
  definitionMetadata?: EntityDefinitionMetadata;
  readProtected?: boolean;
  readProtectionScopes?: string[];
  writeProtected?: boolean;
  writeProtectionScopes?: string[];
  preservedProtections?: string[];
  protectionMethodRaw?: string;
  fieldModifications?: EntityFieldModification[];
  modifyFieldsMethodRaw?: string;
  collectMethodRaw?: string;
  bulkExtensions?: EntityBulkExtensionTarget[];
  shopwareVersion?: string;
  definitionClass?: string;
  entityClass?: string;
  collectionClass?: string;
  definitionUri?: string;
  entityUri?: string;
  collectionUri?: string;
  fields: EntityField[];
  indexes?: EntityIndex[];
  translation?: EntityTranslationSpec;
  serviceUri?: string;
  createMigration: boolean;
  migrationName?: string;
  migrationTimestamp?: number;
  baseSnapshotIds?: string[];
}

export interface EntityRelationTarget {
  entityName: string;
  definitionClass: string;
  definitionKind?: EntityDefinitionKind;
  entityClass?: string;
  collectionClass?: string;
  fileUri?: string;
  fields?: {propertyName: string; storageName: string; primary?: boolean}[];
  versionAware?: boolean;
  inheritanceAware?: boolean;
}

export interface EntityBootstrap {
  plugin: {
    rootUri: string;
    composerName: string;
    pluginClass: string;
    sourceRootUri: string;
    baseNamespace: string;
    namespace: string;
    shopwareVersion?: string;
    serviceUris?: string[];
  };
  spec: EntitySpec;
  definitionKinds?: EntityDefinitionKind[];
  fieldTypes: {id?: string; kind: string; label: string; stored: boolean; definitionKinds?: EntityDefinitionKind[]; requiresDefaultFieldsOverride?: boolean; template?: EntityField}[];
  graph: {
    snapshotCount: number;
    leaves?: string[];
    missing?: Record<string, string[]>;
    needsReconciliation: boolean;
  };
  existing?: EntityRelationTarget[];
  editable?: {
    entityName: string;
    definitionClass: string;
    definitionKind: EntityDefinitionKind;
    fileUri: string;
  }[];
}

export interface EntityDecision {
  kind: string;
  entity: string;
  from?: string;
  to?: string;
  reason?: string;
}

export interface EntityPreview {
  revision: string;
  files?: {uri: string; action: string; language: string; before?: string; after: string}[];
  issues?: {code: string; message: string; fieldId?: string; severity: string}[];
  diff: {
    renameQuestions?: {entity: string; added: string; candidates: {from: string; score: number}[]}[];
    entityRenameQuestions?: {added: string; candidates: {from: string; score: number}[]}[];
    createdEntities?: {name: string}[];
    removedEntities?: {name: string}[];
    addedColumns?: {entity: string; after?: EntityColumn}[];
    removedColumns?: {entity: string; before?: EntityColumn}[];
    changedColumns?: {entity: string; before?: EntityColumn; after?: EntityColumn}[];
    addedIndexes?: {entity: string; index: EntityDatabaseIndex}[];
    removedIndexes?: {entity: string; index: EntityDatabaseIndex}[];
    addedForeignKeys?: {entity: string; foreignKey: {name: string}}[];
    removedForeignKeys?: {entity: string; foreignKey: {name: string}}[];
    changedPrimaryKeys?: {entity: string; before?: string[]; after?: string[]}[];
  };
  destructive: boolean;
  drift: boolean;
  driftMessage?: string;
  snapshotId?: string;
  primaryFileUri?: string;
  migrationTimestamp?: number;
}

interface EntityColumn {
  name: string;
  sqlType: string;
  notNull: boolean;
}

interface EntityDatabaseIndex {
  name: string;
  unique: boolean;
  columns: string[];
}

export interface EntityApplyResponse {
  edit: ProtocolWorkspaceEdit;
  primaryFileUri: string;
  snapshotId: string;
}
