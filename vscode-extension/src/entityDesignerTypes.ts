import type {WorkspaceEdit as ProtocolWorkspaceEdit} from 'vscode-languageserver-protocol';

export interface EntityField {
  id: string;
  kind: string;
  propertyName: string;
  foreignKeyPropertyName?: string;
  storageName?: string;
  required?: boolean;
  primary?: boolean;
  apiAware?: boolean;
  searchRanking?: number;
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
  associationApiAware?: boolean;
  associationSearchRanking?: number;
  migrationDefault?: string;
  editable: boolean;
  raw?: string;
}

export interface EntityIndex {
  name: string;
  kind: 'index' | 'unique';
  columns: string[];
}

export interface EntitySpec {
  mode: string;
  pluginRootUri: string;
  directoryUri: string;
  namespace: string;
  className: string;
  entityName: string;
  definitionClass?: string;
  entityClass?: string;
  collectionClass?: string;
  definitionUri?: string;
  entityUri?: string;
  collectionUri?: string;
  fields: EntityField[];
  indexes?: EntityIndex[];
  serviceUri?: string;
  createMigration: boolean;
  migrationName?: string;
  migrationTimestamp?: number;
  baseSnapshotIds?: string[];
}

export interface EntityRelationTarget {
  entityName: string;
  definitionClass: string;
  entityClass?: string;
  collectionClass?: string;
  fileUri?: string;
  fields?: {propertyName: string; storageName: string; primary?: boolean}[];
  versionAware?: boolean;
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
  fieldTypes: {kind: string; label: string; stored: boolean}[];
  graph: {
    snapshotCount: number;
    leaves?: string[];
    missing?: Record<string, string[]>;
    needsReconciliation: boolean;
  };
  existing?: EntityRelationTarget[];
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
