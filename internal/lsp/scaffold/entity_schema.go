package scaffold

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/rewrite"
	"github.com/shopware/shopware-lsp/internal/shopware/entityschema"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	EntitySchemaBootstrapCommand = "shopware/entity-schema/bootstrap"
	EntitySchemaSearchCommand    = "shopware/entity-schema/search"
	EntitySchemaLoadCommand      = "shopware/entity-schema/load"
	EntitySchemaPreviewCommand   = "shopware/entity-schema/preview"
	EntitySchemaApplyCommand     = "shopware/entity-schema/apply"
	EntitySchemaReconcileCommand = "shopware/entity-schema/reconcile"
)

type EntitySchemaDocument struct {
	Text    string `json:"text"`
	Version *int   `json:"version,omitempty"`
}

type EntitySchemaBootstrapRequest struct {
	DirectoryURI string `json:"directoryUri"`
}

type EntitySchemaGraph struct {
	SnapshotCount       int                 `json:"snapshotCount"`
	Leaves              []string            `json:"leaves,omitempty"`
	Missing             map[string][]string `json:"missing,omitempty"`
	NeedsReconciliation bool                `json:"needsReconciliation"`
}

type EntitySchemaFieldType struct {
	Kind                          string                        `json:"kind"`
	Label                         string                        `json:"label"`
	Stored                        bool                          `json:"stored"`
	DefinitionKinds               []entityschema.DefinitionKind `json:"definitionKinds,omitempty"`
	RequiresDefaultFieldsOverride bool                          `json:"requiresDefaultFieldsOverride,omitempty"`
	ID                            string                        `json:"id,omitempty"`
	Template                      *entityschema.FieldSpec       `json:"template,omitempty"`
}

type EntitySchemaBootstrapResponse struct {
	Plugin          entityschema.PluginContext    `json:"plugin"`
	Spec            entityschema.EntitySpec       `json:"spec"`
	DefinitionKinds []entityschema.DefinitionKind `json:"definitionKinds"`
	FieldTypes      []EntitySchemaFieldType       `json:"fieldTypes"`
	Graph           EntitySchemaGraph             `json:"graph"`
	Existing        []EntitySchemaRelationTarget  `json:"existing,omitempty"`
	Editable        []EntitySchemaEditableTarget  `json:"editable,omitempty"`
}

type EntitySchemaEditableTarget struct {
	EntityName      string                      `json:"entityName"`
	DefinitionClass string                      `json:"definitionClass"`
	DefinitionKind  entityschema.DefinitionKind `json:"definitionKind"`
	FileURI         string                      `json:"fileUri"`
}

type EntitySchemaRelationTarget struct {
	EntityName       string                             `json:"entityName"`
	DefinitionClass  string                             `json:"definitionClass"`
	DefinitionKind   entityschema.DefinitionKind        `json:"definitionKind,omitempty"`
	EntityClass      string                             `json:"entityClass,omitempty"`
	CollectionClass  string                             `json:"collectionClass,omitempty"`
	FileURI          string                             `json:"fileUri,omitempty"`
	Fields           []entityschema.RelationTargetField `json:"fields,omitempty"`
	VersionAware     bool                               `json:"versionAware,omitempty"`
	InheritanceAware bool                               `json:"inheritanceAware,omitempty"`
}

type EntitySchemaSearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type EntitySchemaLoadRequest struct {
	DefinitionClass string                          `json:"definitionClass,omitempty"`
	DefinitionKind  entityschema.DefinitionKind     `json:"definitionKind,omitempty"`
	EntityName      string                          `json:"entityName,omitempty"`
	FileURI         string                          `json:"fileUri,omitempty"`
	Documents       map[string]EntitySchemaDocument `json:"documents,omitempty"`
}

type EntitySchemaPreviewRequest struct {
	Spec          entityschema.EntitySpec         `json:"spec"`
	Decisions     []entityschema.Decision         `json:"decisions,omitempty"`
	DriftDecision string                          `json:"driftDecision,omitempty"`
	Documents     map[string]EntitySchemaDocument `json:"documents,omitempty"`
}

type EntitySchemaFilePreview struct {
	URI      string `json:"uri"`
	Action   string `json:"action"`
	Language string `json:"language"`
	Before   string `json:"before,omitempty"`
	After    string `json:"after"`
}

type EntitySchemaPreviewResponse struct {
	Revision           string                         `json:"revision"`
	Files              []EntitySchemaFilePreview      `json:"files,omitempty"`
	Issues             []entityschema.ValidationIssue `json:"issues,omitempty"`
	Diff               entityschema.SchemaDiff        `json:"diff"`
	Destructive        bool                           `json:"destructive"`
	Drift              bool                           `json:"drift"`
	DriftMessage       string                         `json:"driftMessage,omitempty"`
	SnapshotID         string                         `json:"snapshotId,omitempty"`
	PrimaryFileURI     string                         `json:"primaryFileUri,omitempty"`
	MigrationTimestamp int64                          `json:"migrationTimestamp,omitempty"`
}

type EntitySchemaApplyRequest struct {
	EntitySchemaPreviewRequest
	Revision         string `json:"revision"`
	AllowDestructive bool   `json:"allowDestructive"`
}

type EntitySchemaApplyResponse struct {
	Edit           *protocol.WorkspaceEdit `json:"edit"`
	PrimaryFileURI string                  `json:"primaryFileUri"`
	SnapshotID     string                  `json:"snapshotId"`
}

type entitySchemaPrepared struct {
	response EntitySchemaPreviewResponse
	files    []entitySchemaPreparedFile
}

type entitySchemaPreparedFile struct {
	path, before, after string
	version             *int
	exists              bool
	delete              bool
}

type entitySchemaSource struct {
	path    string
	content string
	version *int
	exists  bool
}

type entitySchemaSources struct {
	definition            entitySchemaSource
	entity                entitySchemaSource
	collection            entitySchemaSource
	translationDefinition entitySchemaSource
	translationEntity     entitySchemaSource
	translationCollection entitySchemaSource
}

type entitySchemaHistory struct {
	scanned  entityschema.Schema
	previous entityschema.Schema
	parents  []string
	pending  []entitySchemaPreparedFile
	stop     bool
}

func (p *Provider) entitySchemaBootstrap(ctx context.Context, raw *json.RawMessage) (interface{}, error) {
	var request EntitySchemaBootstrapRequest
	if err := decodeScaffoldRequest(raw, &request); err != nil {
		return nil, err
	}
	directory, plugin, err := p.entityPlugin(request.DirectoryURI)
	if err != nil {
		return nil, err
	}
	if !safePluginTarget(plugin.SourceRoot, directory) {
		directory = plugin.SourceRoot
		plugin, err = entityschema.FindPluginContext(p.root, directory)
		if err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	plugin.RootURI = uriutil.FileURI(plugin.Root)
	plugin.SourceRootURI = uriutil.FileURI(plugin.SourceRoot)
	for index := range plugin.ServiceURIs {
		plugin.ServiceURIs[index] = uriutil.FileURI(plugin.ServiceURIs[index])
	}
	snapshots, err := entityschema.ReadSnapshots(plugin.Root)
	if err != nil {
		return nil, err
	}
	graph, err := entityschema.BuildSnapshotGraph(snapshots)
	if err != nil {
		return nil, err
	}
	leaves := make([]string, 0, len(graph.Leaves))
	for _, leaf := range graph.Leaves {
		leaves = append(leaves, leaf.Snapshot.ID)
	}
	name := pascalName(filepath.Base(directory))
	if name == "" || name == "Src" || name == "Entity" {
		name = "Example"
	}
	spec := entityschema.CompleteSpec(entityschema.EntitySpec{
		Mode: "new", PluginRootURI: plugin.RootURI, DirectoryURI: uriutil.FileURI(directory),
		Namespace: plugin.Namespace, ClassName: name, EntityName: snakeCase(name), ShopwareVersion: plugin.ShopwareVersion, CreateMigration: true,
		Fields: []entityschema.FieldSpec{
			{ID: "id", Kind: entityschema.FieldID, Editable: true},
		},
	})
	if len(plugin.ServiceURIs) != 0 {
		spec.ServiceURI = plugin.ServiceURIs[0]
	}
	spec.BaseSnapshotIDs = append([]string(nil), leaves...)
	existing, _ := p.relationTargets("")
	pluginExisting := existing[:0]
	for _, target := range existing {
		path, pathErr := uriutil.Path(target.FileURI)
		if pathErr != nil {
			continue
		}
		relative, relErr := filepath.Rel(plugin.Root, path)
		if relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			pluginExisting = append(pluginExisting, target)
		}
	}
	existing = pluginExisting
	if len(existing) > 100 {
		existing = existing[:100]
	}
	var editable []EntitySchemaEditableTarget
	lookup, lookupErr := p.entityRelationLookup()
	if lookupErr == nil {
		_, definitions, scanErr := p.scanEntitySchema(ctx, plugin.Root, lookup)
		if scanErr == nil {
			for _, definition := range definitions {
				editable = append(editable, EntitySchemaEditableTarget{
					EntityName: definition.Spec.EntityName, DefinitionClass: definition.Spec.DefinitionClass,
					DefinitionKind: definition.Spec.DefinitionKind, FileURI: uriutil.FileURI(definition.Path),
				})
			}
		}
	}
	return EntitySchemaBootstrapResponse{
		Plugin: plugin, Spec: spec, Graph: EntitySchemaGraph{SnapshotCount: len(snapshots), Leaves: leaves, Missing: graph.Missing, NeedsReconciliation: len(graph.Leaves) > 1 || len(graph.Missing) > 0},
		DefinitionKinds: entityschema.DefinitionKindsForVersion(plugin.ShopwareVersion),
		FieldTypes:      entitySchemaFieldTypes(plugin.ShopwareVersion, p.specializedFieldClassAvailable), Existing: existing, Editable: editable,
	}, nil
}

func (p *Provider) entitySchemaSearch(ctx context.Context, raw *json.RawMessage) (interface{}, error) {
	var request EntitySchemaSearchRequest
	if err := decodeScaffoldRequest(raw, &request); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	results, err := p.relationTargets(request.Query)
	if err != nil {
		return nil, err
	}
	limit := request.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func (p *Provider) relationTargets(query string) ([]EntitySchemaRelationTarget, error) {
	if p.dal == nil {
		return nil, nil
	}
	definitions, err := p.dal.Definitions()
	if err != nil {
		return nil, err
	}
	if p.phpIndex != nil {
		candidates, candidateErr := p.dal.UnresolvedDefinitions()
		if candidateErr != nil {
			return nil, candidateErr
		}
		snapshot := p.phpIndex.SemanticSnapshot()
		for _, candidate := range candidates {
			switch {
			case snapshot.IsSubtypeOf(candidate.FullyQualifiedClass, `Shopware\Core\Framework\DataAbstractionLayer\EntityTranslationDefinition`):
				candidate.Kind = "translation"
			case snapshot.IsSubtypeOf(candidate.FullyQualifiedClass, `Shopware\Core\Framework\DataAbstractionLayer\MappingEntityDefinition`):
				candidate.Kind = "mapping"
			case snapshot.IsSubtypeOf(candidate.FullyQualifiedClass, `Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition`):
				candidate.Kind = "entity"
			default:
				continue
			}
			definitions = append(definitions, candidate)
		}
	}
	query = strings.ToLower(strings.TrimSpace(query))
	var results []EntitySchemaRelationTarget
	for _, definition := range definitions {
		haystack := strings.ToLower(definition.Name + " " + definition.FullyQualifiedClass + " " + definition.Class)
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		target := EntitySchemaRelationTarget{EntityName: definition.Name, DefinitionClass: definition.FullyQualifiedClass, DefinitionKind: entityschema.DefinitionKind(definition.Kind), EntityClass: definition.EntityClass, CollectionClass: definition.CollectionClass, FileURI: uriutil.FileURI(definition.File), VersionAware: definition.VersionAware, InheritanceAware: definition.InheritanceAware}
		for _, field := range definition.Fields {
			if !field.Stored {
				continue
			}
			target.Fields = append(target.Fields, entityschema.RelationTargetField{PropertyName: field.Name, StorageName: field.StorageName, Primary: field.Primary})
		}
		if target.DefinitionKind == entityschema.DefinitionEntity {
			target.Fields = appendMissingRelationTargetFields(target.Fields,
				entityschema.RelationTargetField{PropertyName: "createdAt", StorageName: "created_at"},
				entityschema.RelationTargetField{PropertyName: "updatedAt", StorageName: "updated_at"},
			)
		}
		results = append(results, target)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].EntityName < results[j].EntityName })
	return results, nil
}

func appendMissingRelationTargetFields(
	fields []entityschema.RelationTargetField,
	additional ...entityschema.RelationTargetField,
) []entityschema.RelationTargetField {
	seen := make(map[string]struct{}, len(fields)+len(additional))
	for _, field := range fields {
		seen[field.StorageName] = struct{}{}
	}
	for _, field := range additional {
		if _, found := seen[field.StorageName]; found {
			continue
		}
		fields = append(fields, field)
		seen[field.StorageName] = struct{}{}
	}
	return fields
}

func (p *Provider) entitySchemaLoad(ctx context.Context, raw *json.RawMessage) (interface{}, error) {
	var request EntitySchemaLoadRequest
	if err := decodeScaffoldRequest(raw, &request); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := ""
	if request.FileURI != "" {
		var pathErr error
		path, pathErr = uriutil.Path(request.FileURI)
		if pathErr != nil {
			return nil, pathErr
		}
	}
	if path == "" && p.dal != nil {
		definitions, err := p.dal.Definitions()
		if err != nil {
			return nil, err
		}
		for _, definition := range definitions {
			if (request.EntityName != "" && definition.Name == request.EntityName) || (request.DefinitionClass != "" && strings.EqualFold(strings.Trim(definition.FullyQualifiedClass, `\`), strings.Trim(request.DefinitionClass, `\`))) {
				path = definition.File
				break
			}
		}
	}
	if path == "" {
		return nil, errors.New("entity definition was not found in the index")
	}
	content, _, exists, err := sourceForPath(path, request.Documents)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.New("entity definition no longer exists")
	}
	var spec entityschema.EntitySpec
	if request.DefinitionKind != "" && request.DefinitionClass != "" {
		lookup, lookupErr := p.entityRelationLookup()
		if lookupErr != nil {
			return nil, lookupErr
		}
		spec, err = entityschema.ImportClassBasedDefinition(content, request.DefinitionClass, request.DefinitionKind, lookup)
	} else {
		spec, err = p.importEntityDefinition(content)
	}
	if err != nil {
		return nil, err
	}
	plugin, err := entityschema.FindPluginContext(p.root, filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	spec.Mode = "edit"
	spec.PluginRootURI = uriutil.FileURI(plugin.Root)
	spec.DirectoryURI = uriutil.FileURI(filepath.Dir(path))
	spec.DefinitionURI = uriutil.FileURI(path)
	if spec.DefinitionKind == entityschema.DefinitionEntity {
		spec.EntityURI = uriutil.FileURI(filepath.Join(filepath.Dir(path), entityschema.ShortClass(spec.EntityClass)+".php"))
		spec.CollectionURI = uriutil.FileURI(filepath.Join(filepath.Dir(path), entityschema.ShortClass(spec.CollectionClass)+".php"))
	} else {
		spec.EntityURI = ""
		spec.CollectionURI = ""
	}
	spec.ShopwareVersion = plugin.ShopwareVersion
	if spec.Translation != nil && spec.Translation.Enabled {
		translationPath := ""
		if p.dal != nil {
			definitions, definitionsErr := p.dal.Definitions()
			if definitionsErr != nil {
				return nil, definitionsErr
			}
			for _, definition := range definitions {
				if strings.EqualFold(strings.Trim(definition.FullyQualifiedClass, `\`), strings.Trim(spec.Translation.DefinitionClass, `\`)) {
					translationPath = definition.File
					break
				}
			}
		}
		if translationPath == "" {
			translationPath = defaultTranslationDirectory(filepath.Dir(path), spec)
			translationPath = filepath.Join(translationPath, entityschema.ShortClass(spec.Translation.DefinitionClass)+".php")
		}
		translationSource, _, translationExists, translationErr := sourceForPath(translationPath, request.Documents)
		if translationErr != nil {
			return nil, translationErr
		}
		if !translationExists {
			return nil, fmt.Errorf("translation definition %s was not found", spec.Translation.DefinitionClass)
		}
		lookup, lookupErr := p.entityRelationLookup()
		if lookupErr != nil {
			return nil, lookupErr
		}
		importedTranslation, importErr := entityschema.ImportTranslationDefinition(translationSource, lookup)
		if importErr != nil {
			return nil, importErr
		}
		if !samePHPClass(spec.Translation.DefinitionClass, importedTranslation.Spec.DefinitionClass) ||
			!samePHPClass(spec.DefinitionClass, importedTranslation.Spec.ParentDefinitionClass) {
			return nil, errors.New("translation definition does not match its parent association")
		}
		translationDirectory := filepath.Dir(translationPath)
		importedTranslation.Spec.DefinitionURI = uriutil.FileURI(translationPath)
		importedTranslation.Spec.EntityURI = uriutil.FileURI(filepath.Join(translationDirectory, entityschema.ShortClass(importedTranslation.Spec.EntityClass)+".php"))
		importedTranslation.Spec.CollectionURI = uriutil.FileURI(filepath.Join(translationDirectory, entityschema.ShortClass(importedTranslation.Spec.CollectionClass)+".php"))
		spec = entityschema.AttachTranslation(spec, importedTranslation)
	}
	if p.entities != nil {
		p.entities.EnrichSpec(&spec)
	}
	if len(plugin.ServiceURIs) != 0 {
		spec.ServiceURI = uriutil.FileURI(plugin.ServiceURIs[0])
	}
	snapshots, err := entityschema.ReadSnapshots(plugin.Root)
	if err != nil {
		return nil, err
	}
	graph, err := entityschema.BuildSnapshotGraph(snapshots)
	if err != nil {
		return nil, err
	}
	if len(graph.Leaves) == 1 {
		entityschema.RestoreSpecIndexesFromEntities(&spec, graph.Leaves[0].Snapshot.Schema)
	}
	for _, leaf := range graph.Leaves {
		spec.BaseSnapshotIDs = append(spec.BaseSnapshotIDs, leaf.Snapshot.ID)
	}
	return spec, nil
}

func (p *Provider) importEntityDefinition(content string) (entityschema.EntitySpec, error) {
	lookup, err := p.entityRelationLookup()
	if err != nil {
		return entityschema.EntitySpec{}, err
	}
	if spec, definitionErr := entityschema.ImportDefinition(content, lookup); definitionErr == nil {
		return spec, nil
	}
	if spec, extensionErr := entityschema.ImportExtension(content, lookup); extensionErr == nil {
		return spec, nil
	}
	return entityschema.ImportBulkExtension(content, lookup)
}

func (p *Provider) entityRelationLookup() (entityschema.RelationLookup, error) {
	targets, err := p.relationTargets("")
	if err != nil {
		return nil, err
	}
	lookupMap := make(map[string]entityschema.RelationTarget, len(targets))
	for _, target := range targets {
		relation := entityschema.RelationTarget{DefinitionClass: target.DefinitionClass, DefinitionKind: target.DefinitionKind, EntityClass: target.EntityClass, CollectionClass: target.CollectionClass, EntityName: target.EntityName, FileURI: target.FileURI, Fields: target.Fields, VersionAware: target.VersionAware, InheritanceAware: target.InheritanceAware}
		lookupMap[target.DefinitionClass] = relation
		if target.EntityName != "" {
			lookupMap[target.EntityName] = relation
		}
	}
	return func(class string) (entityschema.RelationTarget, bool) {
		value, ok := lookupMap[class]
		return value, ok
	}, nil
}

func (p *Provider) entitySchemaPreview(ctx context.Context, raw *json.RawMessage) (interface{}, error) {
	var request EntitySchemaPreviewRequest
	if err := decodeScaffoldRequest(raw, &request); err != nil {
		return nil, err
	}
	prepared, err := p.prepareEntitySchema(ctx, request)
	if err != nil {
		return nil, err
	}
	return prepared.response, nil
}

func (p *Provider) entitySchemaApply(ctx context.Context, raw *json.RawMessage) (interface{}, error) {
	var request EntitySchemaApplyRequest
	if err := decodeScaffoldRequest(raw, &request); err != nil {
		return nil, err
	}
	previewRequest := request.EntitySchemaPreviewRequest
	if previewRequest.Spec.MigrationTimestamp <= 0 {
		if timestamp, ok := entitySchemaRevisionTimestamp(request.Revision); ok {
			previewRequest.Spec.MigrationTimestamp = timestamp
		}
	}
	prepared, err := p.prepareEntitySchema(ctx, previewRequest)
	if err != nil {
		return nil, err
	}
	if request.Revision == "" || request.Revision != prepared.response.Revision {
		return nil, errors.New("entity preview is stale; preview again before applying")
	}
	if len(prepared.response.Issues) != 0 {
		return nil, errors.New("entity schema has unresolved validation, drift, or rename questions")
	}
	if prepared.response.Destructive && !request.AllowDestructive {
		return nil, errors.New("destructive entity schema changes require explicit confirmation")
	}
	plan := rewrite.WorkspacePlan{}
	for _, file := range prepared.files {
		uri := uriutil.FileURI(file.path)
		if file.delete {
			plan.Deletes = append(plan.Deletes, rewrite.DeleteFilePlan{URI: uri, Version: file.version, Source: file.before})
		} else if !file.exists {
			plan.Creates = append(plan.Creates, rewrite.CreateFilePlan{URI: uri, Content: file.after})
		} else {
			plan.Documents = append(plan.Documents, rewrite.NewDocumentPlan(uri, file.version, file.before, []rewrite.Edit{{Range: cst.TextRange{Start: 0, End: uint32(len(file.before))}, NewText: file.after}}))
		}
	}
	edit, err := plan.WorkspaceEdit()
	if err != nil {
		return nil, err
	}
	return EntitySchemaApplyResponse{Edit: edit, PrimaryFileURI: prepared.response.PrimaryFileURI, SnapshotID: prepared.response.SnapshotID}, nil
}

func (p *Provider) prepareEntitySchema(ctx context.Context, request EntitySchemaPreviewRequest) (entitySchemaPrepared, error) {
	if err := ctx.Err(); err != nil {
		return entitySchemaPrepared{}, err
	}
	spec := entityschema.CompleteSpec(request.Spec)
	generatedTimestamp := spec.MigrationTimestamp <= 0
	if generatedTimestamp {
		spec.MigrationTimestamp = time.Now().Unix()
	}
	response := EntitySchemaPreviewResponse{MigrationTimestamp: spec.MigrationTimestamp}
	directory, err := uriutil.Path(spec.DirectoryURI)
	if err != nil {
		return entitySchemaPrepared{}, fmt.Errorf("resolve entity directory: %w", err)
	}
	directory, err = p.validatedDirectory(directory)
	if err != nil {
		return entitySchemaPrepared{}, err
	}
	plugin, err := entityschema.FindPluginContext(p.root, directory)
	if err != nil {
		return entitySchemaPrepared{}, err
	}
	if !safePluginTarget(plugin.SourceRoot, directory) {
		return entitySchemaPrepared{}, fmt.Errorf(
			"entity directory %s is outside the Composer PSR-4 source root %s",
			directory,
			plugin.SourceRoot,
		)
	}
	if spec.PluginRootURI != "" && spec.PluginRootURI != uriutil.FileURI(plugin.Root) {
		return entitySchemaPrepared{}, errors.New("entity plugin root changed since the designer was opened")
	}
	if strings.Trim(spec.Namespace, `\`) != strings.Trim(plugin.Namespace, `\`) {
		return entitySchemaPrepared{}, fmt.Errorf("entity namespace %s does not match the Composer PSR-4 directory namespace %s", spec.Namespace, plugin.Namespace)
	}
	spec.ShopwareVersion = plugin.ShopwareVersion
	if generatedTimestamp {
		spec.MigrationTimestamp = availableMigrationTimestamp(plugin.SourceRoot, spec.MigrationTimestamp)
		response.MigrationTimestamp = spec.MigrationTimestamp
	}
	sources, err := entitySchemaSourcesFor(directory, spec, request.Documents)
	if err != nil {
		return entitySchemaPrepared{}, err
	}
	relationLookup, err := p.entityRelationLookup()
	if err != nil {
		return entitySchemaPrepared{}, err
	}
	previousSpec, err := importPreviousEntitySpec(spec, sources, relationLookup)
	if err != nil {
		return entitySchemaPrepared{}, err
	}
	if p.entities != nil {
		p.entities.EnrichSpec(&spec)
		p.entities.EnrichSpec(previousSpec)
	}
	if previousSpec != nil && strings.TrimSpace(previousSpec.CollectMethodRaw) != "" &&
		(spec.DefinitionKind != entityschema.DefinitionBulkExtension || spec.CollectMethodRaw != previousSpec.CollectMethodRaw ||
			len(spec.BulkExtensions) != 0 || len(spec.Fields) != 0 || len(spec.Indexes) != 0) {
		response.Issues = append(response.Issues, entityIssue(
			"entity.bulkExtension.collectRaw.locked",
			"This custom BulkEntityExtension collect method is preserved losslessly and cannot be changed in the entity designer",
		))
		return entitySchemaPrepared{response: response}, nil
	}
	if previousSpec != nil && !preservedDefinitionRawMethodsEqual(*previousSpec, spec) {
		response.Issues = append(response.Issues, entityIssue(
			"entity.definition.raw.locked",
			"Custom definition behavior is preserved losslessly and cannot be changed in the entity designer",
		))
		return entitySchemaPrepared{response: response}, nil
	}
	spec = normalizeDefinitionTransition(spec, previousSpec)
	if spec.DefinitionKind == entityschema.DefinitionExtension {
		spec.ExtendedFields = nil
		target, found := relationLookup(spec.ExtendedDefinitionClass)
		if !found || target.EntityName == "" {
			response.Issues = append(response.Issues, entityIssue("entity.extension.target.missing", "Select an indexed entity definition to extend"))
		} else {
			spec.ExtendedFields = append([]entityschema.RelationTargetField(nil), target.Fields...)
			if spec.EntityName != target.EntityName {
				response.Issues = append(response.Issues, entityIssue("entity.extension.target.mismatch", "Extension technical entity name does not match the selected indexed definition"))
			}
		}
	}
	if spec.DefinitionKind == entityschema.DefinitionBulkExtension {
		for index := range spec.BulkExtensions {
			targetSpec := &spec.BulkExtensions[index]
			targetSpec.ExtendedFields = nil
			if targetSpec.ExtendedDefinitionClass == "" {
				continue
			}
			target, found := relationLookup(targetSpec.ExtendedDefinitionClass)
			if !found || target.EntityName == "" {
				issue := entityIssue("entity.bulkExtension.target.missing", "Select an indexed entity definition for every bulk extension target")
				issue.FieldID = targetSpec.ID
				response.Issues = append(response.Issues, issue)
				continue
			}
			targetSpec.ExtendedFields = append([]entityschema.RelationTargetField(nil), target.Fields...)
			if targetSpec.EntityName != target.EntityName {
				issue := entityIssue("entity.bulkExtension.target.mismatch", "Bulk extension target entity name does not match its indexed definition")
				issue.FieldID = targetSpec.ID
				response.Issues = append(response.Issues, issue)
			}
		}
	}
	response.Issues = append(response.Issues, validateEntitySchemaSpec(p, spec)...)
	scanned, scannedSpecs, err := p.scanEntitySchema(ctx, plugin.Root, relationLookup)
	if err != nil {
		return entitySchemaPrepared{}, err
	}
	previousIssues, err := mergePreviousEntitySpec(spec, previousSpec, scannedSpecs, &scanned)
	if err != nil {
		return entitySchemaPrepared{}, err
	}
	if len(previousIssues) != 0 {
		response.Issues = append(response.Issues, previousIssues...)
		return entitySchemaPrepared{response: response}, nil
	}
	history, err := prepareEntitySchemaHistory(
		plugin,
		scanned,
		spec,
		request,
		&response,
	)
	if err != nil {
		return entitySchemaPrepared{}, err
	}
	if history.stop {
		return entitySchemaPrepared{response: response}, nil
	}
	scanned = history.scanned
	previous := history.previous
	parents := history.parents
	pending := history.pending
	if previousSpec != nil {
		entityschema.RestoreSpecIndexesFromEntities(previousSpec, previous)
	}
	next := scanned.Clone()
	if err := entityschema.ReplaceSpecSchema(&next, previousSpec, spec); err != nil && len(response.Issues) == 0 {
		return entitySchemaPrepared{}, err
	}
	resolvedPrevious, resolvedDiff, _, decisionErr := entityschema.ResolveSchemaDiff(previous, next, request.Decisions)
	response.Diff = resolvedDiff
	response.Destructive = response.Diff.Destructive()
	if decisionErr != nil {
		code := "entity.table.rename.decision"
		if strings.Contains(decisionErr.Error(), "column") {
			code = "entity.column.rename.decision"
		}
		response.Issues = append(response.Issues, entityIssue(code, decisionErr.Error()))
	}
	response.Issues = append(response.Issues, entityschema.ValidateMigration(resolvedPrevious, next, response.Diff, request.Decisions)...)
	if len(response.Issues) != 0 {
		return entitySchemaPrepared{response: response}, nil
	}
	statements, _, err := entityschema.MigrationStatements(previous, next, request.Decisions)
	if err != nil {
		return entitySchemaPrepared{}, err
	}
	if response.Diff.DatabaseChanged() && !spec.CreateMigration {
		response.Issues = append(response.Issues, entityIssue("entity.migration.required", "Database changes require a migration"))
		return entitySchemaPrepared{response: response}, nil
	}
	return finalizeEntitySchemaPreview(spec, previousSpec, sources, plugin, request, pending, statements, parents, next, response)
}

func finalizeEntitySchemaPreview(
	spec entityschema.EntitySpec,
	previousSpec *entityschema.EntitySpec,
	sources entitySchemaSources,
	plugin entityschema.PluginContext,
	request EntitySchemaPreviewRequest,
	pending []entitySchemaPreparedFile,
	statements, parents []string,
	next entityschema.Schema,
	response EntitySchemaPreviewResponse,
) (entitySchemaPrepared, error) {
	phpFiles, err := renderEntitySchemaFiles(spec, previousSpec, sources)
	if err != nil {
		return entitySchemaPrepared{}, err
	}
	pending = append(pending, phpFiles...)
	servicePath := ""
	if spec.ServiceURI != "" {
		servicePath, _ = uriutil.Path(spec.ServiceURI)
	}
	if servicePath == "" && len(plugin.ServiceURIs) != 0 {
		servicePath = plugin.ServiceURIs[0]
	}
	if servicePath == "" {
		servicePath = filepath.Join(plugin.SourceRoot, "Resources", "config", "services.yaml")
	}
	if !safePluginTarget(plugin.Root, servicePath) {
		return entitySchemaPrepared{}, fmt.Errorf("service configuration is outside plugin %s", plugin.Root)
	}
	serviceSource, serviceVersion, serviceExists, err := sourceForPath(servicePath, request.Documents)
	if err != nil {
		return entitySchemaPrepared{}, err
	}
	serviceTag := entitySchemaServiceTag(spec.DefinitionKind)
	serviceAfter := serviceSource
	if previousSpec != nil {
		previousTag := entitySchemaServiceTag(previousSpec.DefinitionKind)
		if previousTag != serviceTag || !samePHPClass(previousSpec.DefinitionClass, spec.DefinitionClass) {
			serviceAfter, err = entityschema.RemoveServiceConfiguration(servicePath, serviceAfter, previousSpec.DefinitionClass)
			if err != nil {
				return entitySchemaPrepared{}, err
			}
		}
	}
	serviceAfter, err = entityschema.PatchTaggedServiceConfiguration(servicePath, serviceAfter, spec.DefinitionClass, serviceTag)
	if err != nil {
		return entitySchemaPrepared{}, err
	}
	if previousSpec != nil && previousSpec.Translation != nil && previousSpec.Translation.Enabled &&
		(spec.Translation == nil || !spec.Translation.Enabled) {
		serviceAfter, err = entityschema.RemoveServiceConfiguration(servicePath, serviceAfter, previousSpec.Translation.DefinitionClass)
		if err != nil {
			return entitySchemaPrepared{}, err
		}
	}
	if spec.Translation != nil && spec.Translation.Enabled {
		serviceAfter, err = entityschema.PatchServiceConfiguration(servicePath, serviceAfter, spec.Translation.DefinitionClass)
		if err != nil {
			return entitySchemaPrepared{}, err
		}
	}
	appendChangedFile(&pending, servicePath, serviceSource, serviceAfter, serviceVersion, serviceExists)
	timestamp := spec.MigrationTimestamp
	var migrations []entityschema.MigrationReference
	if len(statements) != 0 {
		name := pascalName(spec.MigrationName)
		if name == "" {
			name = "Update" + pascalName(spec.ClassName)
		}
		className := "Migration" + strconv.FormatInt(timestamp, 10) + name
		migrationPath := filepath.Join(plugin.SourceRoot, "Migration", className+".php")
		_, _, migrationExists, readErr := sourceForPath(migrationPath, request.Documents)
		if readErr != nil {
			return entitySchemaPrepared{}, readErr
		}
		if migrationExists {
			return entitySchemaPrepared{}, fmt.Errorf("migration target already exists: %s", migrationPath)
		}
		migration := entityschema.RenderMigration(plugin.BaseNamespace, className, timestamp, statements)
		appendChangedFile(&pending, migrationPath, "", migration, nil, false)
		relative, _ := filepath.Rel(plugin.Root, migrationPath)
		migrations = append(migrations, entityschema.MigrationReference{Path: filepath.ToSlash(relative), Timestamp: timestamp, SHA256: entityschema.FileSHA256([]byte(migration))})
	}
	snapshot, err := (entityschema.Snapshot{Parents: parents, Kind: entityschema.SnapshotMigration, Plugin: entityschema.PluginIdentity{ComposerName: plugin.ComposerName}, ShopwareVersion: plugin.ShopwareVersion, Migrations: migrations, Schema: next, Decisions: request.Decisions}).Seal()
	if err != nil {
		return entitySchemaPrepared{}, err
	}
	snapshotContent, err := entityschema.MarshalSnapshot(snapshot)
	if err != nil {
		return entitySchemaPrepared{}, err
	}
	appendChangedFile(&pending, filepath.Join(plugin.SnapshotDirectory, strconv.FormatInt(timestamp, 10)+"-"+snapshot.ID[:12]+".snapshot.json"), "", string(snapshotContent), nil, false)
	response.SnapshotID = snapshot.ID
	response.PrimaryFileURI = uriutil.FileURI(sources.definition.path)
	for _, file := range pending {
		if !safePluginTarget(plugin.Root, file.path) {
			return entitySchemaPrepared{}, fmt.Errorf("entity output is outside plugin %s: %s", plugin.Root, file.path)
		}
		action := "create"
		if file.delete {
			action = "delete"
		} else if file.exists {
			action = "update"
		}
		response.Files = append(response.Files, EntitySchemaFilePreview{URI: uriutil.FileURI(file.path), Action: action, Language: languageForPath(file.path), Before: file.before, After: file.after})
	}
	revisionRequest := request
	revisionRequest.Spec = spec
	revisionContent, _ := json.Marshal(struct {
		Request EntitySchemaPreviewRequest
		Files   []EntitySchemaFilePreview
	}{revisionRequest, response.Files})
	hash := sha256.Sum256(revisionContent)
	response.Revision = entitySchemaRevision(spec.MigrationTimestamp, hash)
	return entitySchemaPrepared{response: response, files: pending}, nil
}

func (p *Provider) scanEntitySchema(
	ctx context.Context,
	pluginRoot string,
	lookup entityschema.RelationLookup,
) (entityschema.Schema, []entityschema.ScannedDefinition, error) {
	if p != nil && p.entities != nil {
		return p.entities.ScanContext(ctx, pluginRoot, lookup)
	}
	// Parser/importer unit tests intentionally construct a provider without a
	// workspace index. Production providers always use the indexed catalog.
	return entityschema.ScanPluginSchemaWithLookup(pluginRoot, lookup)
}

func preservedDefinitionRawMethodsEqual(previous, next entityschema.EntitySpec) bool {
	behaviorRaw := func(value *entityschema.DefinitionBehaviorSpec) []string {
		if value == nil {
			return nil
		}
		return []string{value.ParentDefinitionMethodRaw, value.VersionAwareMethodRaw, value.InheritanceAwareMethodRaw, value.DefaultFieldsMethodRaw, value.BaseFieldsMethodRaw, value.RestrictDeleteMetaMethodRaw}
	}
	metadataRaw := func(value *entityschema.DefinitionMetadataSpec) []string {
		if value == nil {
			return nil
		}
		return []string{value.SinceMethodRaw, value.DefaultsMethodRaw, value.ChildDefaultsMethodRaw, value.HydratorMethodRaw}
	}
	if !reflect.DeepEqual(behaviorRaw(previous.DefinitionBehavior), behaviorRaw(next.DefinitionBehavior)) ||
		!reflect.DeepEqual(metadataRaw(previous.DefinitionMetadata), metadataRaw(next.DefinitionMetadata)) {
		return false
	}
	var previousTranslationBehavior, nextTranslationBehavior *entityschema.DefinitionBehaviorSpec
	var previousTranslationMetadata, nextTranslationMetadata *entityschema.DefinitionMetadataSpec
	if previous.Translation != nil {
		previousTranslationBehavior = previous.Translation.DefinitionBehavior
		previousTranslationMetadata = previous.Translation.DefinitionMetadata
	}
	if next.Translation != nil {
		nextTranslationBehavior = next.Translation.DefinitionBehavior
		nextTranslationMetadata = next.Translation.DefinitionMetadata
	}
	return reflect.DeepEqual(behaviorRaw(previousTranslationBehavior), behaviorRaw(nextTranslationBehavior)) &&
		reflect.DeepEqual(metadataRaw(previousTranslationMetadata), metadataRaw(nextTranslationMetadata))
}

func normalizeDefinitionTransition(
	next entityschema.EntitySpec,
	previous *entityschema.EntitySpec,
) entityschema.EntitySpec {
	if previous == nil || previous.DefinitionKind == next.DefinitionKind {
		return next
	}
	if next.DefinitionKind == entityschema.DefinitionMapping {
		// The old URIs remain on the edit request so entity/collection files can
		// be deleted, while the desired mapping definition must not reference
		// those now-obsolete companion classes.
		next.EntityClass = ""
		next.CollectionClass = ""
		if next.DefinitionMetadata != nil {
			next.DefinitionMetadata.ChildDefaults = nil
			next.DefinitionMetadata.ChildDefaultsMethodRaw = ""
			next.DefinitionMetadata.HydratorClass = ""
			next.DefinitionMetadata.HydratorMethodRaw = ""
		}
		if next.DefinitionBehavior != nil {
			next.DefinitionBehavior.OverrideDefaultFields = false
			next.DefinitionBehavior.DefaultFields = nil
			next.DefinitionBehavior.DefaultFieldsMethodRaw = ""
			next.DefinitionBehavior.InheritanceAwareMethodRaw = ""
		}
	}
	if next.DefinitionKind == entityschema.DefinitionExtension || next.DefinitionKind == entityschema.DefinitionBulkExtension {
		next.DefinitionBehavior = nil
		next.DefinitionMetadata = nil
	}
	if (previous.DefinitionKind == entityschema.DefinitionExtension || previous.DefinitionKind == entityschema.DefinitionBulkExtension) &&
		next.DefinitionKind != entityschema.DefinitionExtension && next.DefinitionKind != entityschema.DefinitionBulkExtension {
		for index := range next.Fields {
			clearEntityExtensionFlag(&next.Fields[index].Metadata)
			clearEntityExtensionFlag(&next.Fields[index].AssociationMetadata)
		}
	}
	return entityschema.CompleteSpec(next)
}

func clearEntityExtensionFlag(metadata **entityschema.FieldMetadata) {
	if *metadata == nil {
		return
	}
	(*metadata).Extension = false
	if reflect.DeepEqual(**metadata, entityschema.FieldMetadata{}) {
		*metadata = nil
	}
}

func entitySchemaServiceTag(kind entityschema.DefinitionKind) string {
	switch kind {
	case entityschema.DefinitionExtension:
		return "shopware.entity.extension"
	case entityschema.DefinitionBulkExtension:
		return "shopware.bulk.entity.extension"
	default:
		return "shopware.entity.definition"
	}
}

func validateEntitySchemaSpec(p *Provider, spec entityschema.EntitySpec) []entityschema.ValidationIssue {
	issues := entityschema.ValidateSpec(spec)
	appendUnavailable := func(field entityschema.FieldSpec) {
		if field.Implementation == nil || p.specializedFieldClassAvailable(field.Implementation.Class) {
			return
		}
		issues = append(issues, entityschema.ValidationIssue{
			Code:    "entity.field.implementation.unavailable",
			Message: fmt.Sprintf("Specialized field class %s is not installed in this Shopware project", field.Implementation.Class),
			FieldID: field.ID, Severity: "error",
		})
	}
	appendEnumIssue := func(field entityschema.FieldSpec) {
		if field.Kind != entityschema.FieldEnum || p == nil || p.phpIndex == nil || field.EnumClass == "" {
			return
		}
		enum, found := p.phpIndex.FindClass(field.EnumClass)
		if !found || enum.Kind != semantic.EnumSymbol {
			issues = append(issues, entityschema.ValidationIssue{
				Code: "entity.field.enum.class.unavailable", Message: fmt.Sprintf("Enum class %s was not found in the PHP index", field.EnumClass),
				FieldID: field.ID, Severity: "error",
			})
			return
		}
		snapshot := p.phpIndex.SemanticSnapshot()
		caseFound := false
		for _, member := range snapshot.Members(enum.ID, field.EnumCase) {
			if member.Kind == semantic.EnumCaseSymbol {
				caseFound = true
				break
			}
		}
		if !caseFound {
			issues = append(issues, entityschema.ValidationIssue{
				Code: "entity.field.enum.case.unavailable", Message: fmt.Sprintf("Enum case %s::%s was not found", field.EnumClass, field.EnumCase),
				FieldID: field.ID, Severity: "error",
			})
		}
		actualBacking := ""
		for _, member := range snapshot.Members(enum.ID, "value") {
			if member.Kind != semantic.PropertySymbol {
				continue
			}
			switch member.Type.String() {
			case "string":
				actualBacking = "string"
			case "int":
				actualBacking = "int"
			}
		}
		if actualBacking == "" {
			issues = append(issues, entityschema.ValidationIssue{
				Code: "entity.field.enum.backing.unavailable", Message: fmt.Sprintf("Enum %s is not a backed enum", field.EnumClass),
				FieldID: field.ID, Severity: "error",
			})
		} else if field.EnumBackingType != "" && field.EnumBackingType != actualBacking {
			issues = append(issues, entityschema.ValidationIssue{
				Code: "entity.field.enum.backing.mismatch", Message: fmt.Sprintf("Enum %s is %s-backed, not %s-backed", field.EnumClass, actualBacking, field.EnumBackingType),
				FieldID: field.ID, Severity: "error",
			})
		}
	}
	appendField := func(field entityschema.FieldSpec) {
		appendUnavailable(field)
		appendEnumIssue(field)
	}
	for _, field := range spec.Fields {
		appendField(field)
	}
	if spec.DefinitionBehavior != nil {
		for _, field := range spec.DefinitionBehavior.DefaultFields {
			appendField(field)
		}
		for _, field := range spec.DefinitionBehavior.BaseFields {
			appendField(field)
		}
	}
	if spec.Translation != nil && spec.Translation.DefinitionBehavior != nil {
		for _, field := range spec.Translation.DefinitionBehavior.DefaultFields {
			appendField(field)
		}
		for _, field := range spec.Translation.DefinitionBehavior.BaseFields {
			appendField(field)
		}
	}
	for _, target := range spec.BulkExtensions {
		for _, field := range target.Fields {
			appendField(field)
		}
	}
	return issues
}

func mergePreviousEntitySpec(
	requested entityschema.EntitySpec,
	previous *entityschema.EntitySpec,
	scannedSpecs []entityschema.ScannedDefinition,
	scanned *entityschema.Schema,
) ([]entityschema.ValidationIssue, error) {
	if previous == nil {
		return nil, nil
	}
	var diskSpec *entityschema.EntitySpec
	for index := range scannedSpecs {
		if samePHPClass(scannedSpecs[index].Spec.DefinitionClass, previous.DefinitionClass) {
			copySpec := scannedSpecs[index].Spec
			diskSpec = &copySpec
			break
		}
	}
	if err := entityschema.ReplaceSpecSchema(scanned, diskSpec, *previous); err != nil {
		return nil, fmt.Errorf("normalize current entity definition: %w", err)
	}
	return nil, nil
}

func importPreviousEntitySpec(
	spec entityschema.EntitySpec,
	sources entitySchemaSources,
	lookup entityschema.RelationLookup,
) (*entityschema.EntitySpec, error) {
	if spec.Mode != "edit" || sources.definition.content == "" {
		return nil, nil
	}
	var imported entityschema.EntitySpec
	var err error
	imported, err = entityschema.ImportDefinition(sources.definition.content, lookup)
	if err != nil {
		imported, err = entityschema.ImportExtension(sources.definition.content, lookup)
	}
	if err != nil {
		imported, err = entityschema.ImportBulkExtension(sources.definition.content, lookup)
	}
	if err != nil && spec.DefinitionClass != "" && spec.DefinitionKind != "" {
		imported, err = entityschema.ImportClassBasedDefinition(
			sources.definition.content, spec.DefinitionClass, spec.DefinitionKind, lookup,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("import current entity definition: %w", err)
	}
	if imported.Translation == nil || !imported.Translation.Enabled {
		return &imported, nil
	}
	if sources.translationDefinition.content == "" {
		return nil, errors.New("current entity references a translation definition that could not be loaded")
	}
	translation, err := entityschema.ImportTranslationDefinition(sources.translationDefinition.content, lookup)
	if err != nil {
		return nil, fmt.Errorf("import current translation definition: %w", err)
	}
	if !samePHPClass(imported.Translation.DefinitionClass, translation.Spec.DefinitionClass) ||
		!samePHPClass(imported.DefinitionClass, translation.Spec.ParentDefinitionClass) {
		return nil, errors.New("current translation definition does not match its parent association")
	}
	translation.Spec.DefinitionURI = uriutil.FileURI(sources.translationDefinition.path)
	translation.Spec.EntityURI = uriutil.FileURI(sources.translationEntity.path)
	translation.Spec.CollectionURI = uriutil.FileURI(sources.translationCollection.path)
	imported = entityschema.AttachTranslation(imported, translation)
	return &imported, nil
}

func renderEntitySchemaFiles(
	spec entityschema.EntitySpec,
	previous *entityschema.EntitySpec,
	sources entitySchemaSources,
) ([]entitySchemaPreparedFile, error) {
	var definition string
	var err error
	if spec.Mode == "edit" && sources.definition.content != "" {
		if previous == nil {
			return nil, fmt.Errorf("cannot safely edit an %s that was not imported from the plugin", spec.DefinitionKind)
		}
		definition, err = entityschema.RewriteDefinitionFrom(sources.definition.content, *previous, spec)
	} else {
		definition, err = entityschema.RenderDefinition(spec)
	}
	if err != nil {
		return nil, err
	}
	var files []entitySchemaPreparedFile
	appendEntitySchemaSource(&files, sources.definition, definition)
	if spec.DefinitionKind == entityschema.DefinitionEntity {
		var entity, collection string
		if previous != nil && previous.DefinitionKind == entityschema.DefinitionEntity && sources.entity.content != "" {
			entity, err = entityschema.RewriteEntity(sources.entity.content, *previous, spec)
			if err == nil && sources.collection.content != "" {
				collection, err = entityschema.RewriteCollection(sources.collection.content, *previous, spec)
			}
		} else {
			entity, err = entityschema.RenderEntity(spec)
		}
		if collection == "" && err == nil {
			collection, err = entityschema.RenderCollection(spec)
		}
		if err != nil {
			return nil, err
		}
		appendEntitySchemaSource(&files, sources.entity, entity)
		appendEntitySchemaSource(&files, sources.collection, collection)
	} else if previous != nil && previous.DefinitionKind == entityschema.DefinitionEntity {
		appendDeletedEntitySchemaSource(&files, sources.entity)
		appendDeletedEntitySchemaSource(&files, sources.collection)
	}
	translationFiles, err := renderTranslationSchemaFiles(spec, previous, sources)
	if err != nil {
		return nil, err
	}
	return append(files, translationFiles...), nil
}

func renderTranslationSchemaFiles(
	spec entityschema.EntitySpec,
	previous *entityschema.EntitySpec,
	sources entitySchemaSources,
) ([]entitySchemaPreparedFile, error) {
	if spec.Translation == nil || !spec.Translation.Enabled {
		if previous != nil && previous.Translation != nil && previous.Translation.Enabled {
			var files []entitySchemaPreparedFile
			appendDeletedEntitySchemaSource(&files, sources.translationDefinition)
			appendDeletedEntitySchemaSource(&files, sources.translationEntity)
			appendDeletedEntitySchemaSource(&files, sources.translationCollection)
			return files, nil
		}
		return nil, nil
	}
	var definition, entity, collection string
	var err error
	if spec.Mode == "edit" && sources.translationDefinition.content != "" {
		if previous == nil || previous.Translation == nil {
			return nil, errors.New("cannot safely edit a translation bundle that was not imported from the plugin")
		}
		definition, err = entityschema.RewriteTranslationDefinitionFrom(sources.translationDefinition.content, *previous, spec)
		if err == nil {
			entity, err = entityschema.RewriteTranslationEntity(sources.translationEntity.content, *previous, spec)
		}
		if err == nil && sources.translationCollection.content != "" {
			collection, err = entityschema.RewriteTranslationCollection(sources.translationCollection.content, *previous, spec)
		}
		if collection == "" && err == nil {
			collection, err = entityschema.RenderTranslationCollection(spec)
		}
	} else {
		definition, err = entityschema.RenderTranslationDefinition(spec)
		if err == nil {
			entity, err = entityschema.RenderTranslationEntity(spec)
		}
		if err == nil {
			collection, err = entityschema.RenderTranslationCollection(spec)
		}
	}
	if err != nil {
		return nil, err
	}
	var files []entitySchemaPreparedFile
	appendEntitySchemaSource(&files, sources.translationDefinition, definition)
	appendEntitySchemaSource(&files, sources.translationEntity, entity)
	appendEntitySchemaSource(&files, sources.translationCollection, collection)
	return files, nil
}

func appendEntitySchemaSource(files *[]entitySchemaPreparedFile, source entitySchemaSource, after string) {
	appendChangedFile(files, source.path, source.content, after, source.version, source.exists)
}

func appendDeletedEntitySchemaSource(files *[]entitySchemaPreparedFile, source entitySchemaSource) {
	if !source.exists {
		return
	}
	*files = append(*files, entitySchemaPreparedFile{
		path: filepath.Clean(source.path), before: source.content,
		version: source.version, exists: true, delete: true,
	})
}

func entitySchemaRevision(timestamp int64, hash [sha256.Size]byte) string {
	return strconv.FormatInt(timestamp, 10) + ":" + hex.EncodeToString(hash[:])
}

func entitySchemaRevisionTimestamp(revision string) (int64, bool) {
	timestampText, digest, found := strings.Cut(revision, ":")
	if !found || len(digest) != sha256.Size*2 {
		return 0, false
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return 0, false
	}
	timestamp, err := strconv.ParseInt(timestampText, 10, 64)
	return timestamp, err == nil && timestamp > 0
}

func prepareEntitySchemaHistory(
	plugin entityschema.PluginContext,
	scanned entityschema.Schema,
	spec entityschema.EntitySpec,
	request EntitySchemaPreviewRequest,
	response *EntitySchemaPreviewResponse,
) (entitySchemaHistory, error) {
	history := entitySchemaHistory{scanned: scanned}
	snapshots, err := entityschema.ReadSnapshots(plugin.Root)
	if err != nil {
		return history, err
	}
	graph, err := entityschema.BuildSnapshotGraph(snapshots)
	if err != nil {
		return history, err
	}
	if len(graph.Missing) != 0 {
		response.Issues = append(response.Issues, entityIssue(
			"entity.snapshot.parent.missing",
			"Snapshot history references missing parents",
		))
		history.stop = true
		return history, nil
	}
	if len(graph.Leaves) > 1 {
		response.Issues = append(response.Issues, entityIssue(
			"entity.snapshot.reconcile.required",
			"Snapshot history has multiple leaves; reconcile branches before generating a migration",
		))
		history.stop = true
		return history, nil
	}
	currentLeaves := make([]string, 0, len(graph.Leaves))
	for _, leaf := range graph.Leaves {
		currentLeaves = append(currentLeaves, leaf.Snapshot.ID)
	}
	if len(spec.BaseSnapshotIDs) != 0 &&
		!sameStringSet(spec.BaseSnapshotIDs, currentLeaves) {
		response.Issues = append(response.Issues, entityIssue(
			"entity.snapshot.stale",
			"Snapshot history changed since the entity was opened; reload the designer before applying another migration",
		))
		history.stop = true
		return history, nil
	}
	if len(graph.Leaves) == 0 {
		return newEntitySchemaBaseline(plugin, history)
	}
	return reconcileEntitySchemaDrift(
		plugin,
		graph.Leaves[0].Snapshot,
		spec,
		request.DriftDecision,
		response,
		history,
	)
}

func newEntitySchemaBaseline(
	plugin entityschema.PluginContext,
	history entitySchemaHistory,
) (entitySchemaHistory, error) {
	baseline, err := (entityschema.Snapshot{
		Kind:            entityschema.SnapshotBaseline,
		Plugin:          entityschema.PluginIdentity{ComposerName: plugin.ComposerName},
		ShopwareVersion: plugin.ShopwareVersion,
		Schema:          history.scanned,
	}).Seal()
	if err != nil {
		return history, err
	}
	encoded, err := entityschema.MarshalSnapshot(baseline)
	if err != nil {
		return history, err
	}
	history.pending = append(history.pending, entitySchemaPreparedFile{
		path: filepath.Join(
			plugin.SnapshotDirectory,
			"0000000000-baseline-"+baseline.ID[:12]+".snapshot.json",
		),
		after: string(encoded),
	})
	history.parents = []string{baseline.ID}
	history.previous = history.scanned
	return history, nil
}

func reconcileEntitySchemaDrift(
	plugin entityschema.PluginContext,
	leaf entityschema.Snapshot,
	spec entityschema.EntitySpec,
	driftDecision string,
	response *EntitySchemaPreviewResponse,
	history entitySchemaHistory,
) (entitySchemaHistory, error) {
	history.parents = []string{leaf.ID}
	history.previous = leaf.Schema
	history.scanned = entityschema.RestoreSnapshotOnlyIndexes(
		history.scanned,
		history.previous,
	)
	if sameEntitySchema(history.previous, history.scanned) {
		return history, nil
	}
	response.Drift = true
	response.Diff = entityschema.DiffSchemas(history.previous, history.scanned)
	response.DriftMessage = "Entity definitions differ from the latest committed schema snapshot. Review the returned diff, then preview again with driftDecision=adopt when the current PHP definitions are authoritative, or driftDecision=migrate when the committed snapshot is authoritative and the code changes need migration SQL."
	switch driftDecision {
	case "adopt":
		return adoptEntitySchemaDrift(plugin, spec, history)
	case "migrate":
		return history, nil
	default:
		response.Issues = append(response.Issues, entityIssue(
			"entity.snapshot.drift.decision",
			"Choose Adopt current schema or Generate migration before applying",
		))
		history.stop = true
		return history, nil
	}
}

func adoptEntitySchemaDrift(
	plugin entityschema.PluginContext,
	spec entityschema.EntitySpec,
	history entitySchemaHistory,
) (entitySchemaHistory, error) {
	adopted, err := (entityschema.Snapshot{
		Parents:         history.parents,
		Kind:            entityschema.SnapshotBaseline,
		Plugin:          entityschema.PluginIdentity{ComposerName: plugin.ComposerName},
		ShopwareVersion: plugin.ShopwareVersion,
		Schema:          history.scanned,
		Decisions: []entityschema.Decision{{
			Kind:   "driftAdopt",
			Reason: "adopt current plugin entity definitions",
		}},
	}).Seal()
	if err != nil {
		return history, err
	}
	encoded, _ := entityschema.MarshalSnapshot(adopted)
	history.pending = append(history.pending, entitySchemaPreparedFile{
		path: filepath.Join(
			plugin.SnapshotDirectory,
			strconv.FormatInt(spec.MigrationTimestamp, 10)+
				"-adopt-"+adopted.ID[:12]+".snapshot.json",
		),
		after: string(encoded),
	})
	history.parents = []string{adopted.ID}
	history.previous = history.scanned
	return history, nil
}

type EntitySchemaReconcileRequest struct {
	DirectoryURI string `json:"directoryUri"`
	SelectedLeaf string `json:"selectedLeaf,omitempty"`
}

func (p *Provider) entitySchemaReconcile(ctx context.Context, raw *json.RawMessage) (interface{}, error) {
	var request EntitySchemaReconcileRequest
	if err := decodeScaffoldRequest(raw, &request); err != nil {
		return nil, err
	}
	_, plugin, err := p.entityPlugin(request.DirectoryURI)
	if err != nil {
		return nil, err
	}
	files, err := entityschema.ReadSnapshots(plugin.Root)
	if err != nil {
		return nil, err
	}
	graph, err := entityschema.BuildSnapshotGraph(files)
	if err != nil {
		return nil, err
	}
	if len(graph.Leaves) < 2 {
		return nil, errors.New("snapshot history does not require reconciliation")
	}
	selected := request.SelectedLeaf
	if selected == "" {
		first := graph.Leaves[0].Snapshot.Schema
		for _, leaf := range graph.Leaves[1:] {
			if !sameEntitySchema(first, leaf.Snapshot.Schema) {
				return nil, errors.New("snapshot leaves differ; select the leaf whose schema is authoritative")
			}
		}
		selected = graph.Leaves[0].Snapshot.ID
	}
	selectedFile, found := graph.Files[selected]
	if !found {
		return nil, errors.New("selected snapshot leaf was not found")
	}
	isLeaf := false
	for _, leaf := range graph.Leaves {
		if leaf.Snapshot.ID == selected {
			isLeaf = true
			break
		}
	}
	if !isLeaf {
		return nil, errors.New("selected snapshot is not a graph leaf")
	}
	parents := make([]string, 0, len(graph.Leaves))
	for _, leaf := range graph.Leaves {
		parents = append(parents, leaf.Snapshot.ID)
	}
	merged, err := (entityschema.Snapshot{Parents: parents, Kind: entityschema.SnapshotMerge, Plugin: entityschema.PluginIdentity{ComposerName: plugin.ComposerName}, ShopwareVersion: plugin.ShopwareVersion, Schema: selectedFile.Snapshot.Schema, Decisions: []entityschema.Decision{{Kind: "branchReconcile", Reason: "selected " + selected}}}).Seal()
	if err != nil {
		return nil, err
	}
	content, _ := entityschema.MarshalSnapshot(merged)
	path := filepath.Join(plugin.SnapshotDirectory, strconv.FormatInt(time.Now().Unix(), 10)+"-merge-"+merged.ID[:12]+".snapshot.json")
	plan := rewrite.WorkspacePlan{Creates: []rewrite.CreateFilePlan{{URI: uriutil.FileURI(path), Content: string(content)}}}
	edit, err := plan.WorkspaceEdit()
	if err != nil {
		return nil, err
	}
	return EntitySchemaApplyResponse{Edit: edit, PrimaryFileURI: uriutil.FileURI(path), SnapshotID: merged.ID}, nil
}

func (p *Provider) entityPlugin(directoryURI string) (string, entityschema.PluginContext, error) {
	directory, err := uriutil.Path(directoryURI)
	if err != nil {
		return "", entityschema.PluginContext{}, err
	}
	directory, err = p.validatedDirectory(directory)
	if err != nil {
		return "", entityschema.PluginContext{}, err
	}
	plugin, err := entityschema.FindPluginContext(p.root, directory)
	return directory, plugin, err
}

func decodeScaffoldRequest(raw *json.RawMessage, target any) error {
	if raw == nil {
		return errors.New("missing entity schema request")
	}
	if err := json.Unmarshal(*raw, target); err != nil {
		return fmt.Errorf("invalid entity schema request: %w", err)
	}
	return nil
}

func entitySchemaFieldTypes(versionConstraint string, available func(string) bool) []EntitySchemaFieldType {
	all := entityschema.DefinitionKindsForVersion(versionConstraint)
	entityMapping := []entityschema.DefinitionKind{entityschema.DefinitionEntity, entityschema.DefinitionMapping}
	entityOnly := []entityschema.DefinitionKind{entityschema.DefinitionEntity}
	entityExtension := []entityschema.DefinitionKind{entityschema.DefinitionEntity, entityschema.DefinitionExtension}
	if entityschema.BulkEntityExtensionSupported(versionConstraint) {
		entityExtension = append(entityExtension, entityschema.DefinitionBulkExtension)
	}
	base := func(kind, label string, stored bool, kinds []entityschema.DefinitionKind) EntitySchemaFieldType {
		return EntitySchemaFieldType{Kind: kind, Label: label, Stored: stored, DefinitionKinds: kinds}
	}
	result := []EntitySchemaFieldType{
		base("id", "Primary ID", true, entityMapping),
		base("binary-id", "Binary ID", true, all),
		base("auto-increment", "Auto increment", true, entityMapping),
		base("version", "Version ID", true, entityOnly),
		base("reference-version", "Reference version", true, all),
		base("foreign-key", "Foreign key", true, entityMapping),
		base("string", "String", true, all),
		base("long-text", "Long text", true, all),
		base("blob", "Blob", true, all),
		base("int", "Integer", true, all),
		base("float", "Float", true, all),
		base("bool", "Boolean", true, all),
		base("date", "Date", true, all),
		base("datetime", "Date/time", true, all),
		base("json", "JSON", true, all),
		base("list", "List (JSON)", true, all),
		base("object", "Object (JSON)", true, all),
		{Kind: "created-at", Label: "Created at", Stored: true, DefinitionKinds: entityMapping, RequiresDefaultFieldsOverride: true},
		{Kind: "updated-at", Label: "Updated at", Stored: true, DefinitionKinds: entityMapping, RequiresDefaultFieldsOverride: true},
		base("many-to-one", "Many to one", true, all),
		base("one-to-one", "One to one", true, entityExtension),
		base("one-to-many", "One to many", false, entityExtension),
		base("many-to-many", "Many to many", false, entityExtension),
		base("hierarchy", "Parent / children hierarchy", true, entityOnly),
	}
	if entityschema.EnumFieldSupported(versionConstraint) &&
		(available == nil || available(`Shopware\Core\Framework\DataAbstractionLayer\Field\EnumField`)) {
		result = append(result, base("enum", "Backed enum", true, all))
	}
	for _, template := range entityschema.SpecializedFieldTemplates() {
		field := template.Field
		if !entityschema.SpecializedFieldSupported(field.Implementation.Class, versionConstraint) ||
			available != nil && !available(field.Implementation.Class) {
			continue
		}
		result = append(result, EntitySchemaFieldType{
			Kind: string(field.Kind), Label: template.Label, Stored: true,
			DefinitionKinds: all, ID: template.ID, Template: &field,
		})
	}
	return result
}

func (p *Provider) specializedFieldClassAvailable(className string) bool {
	if p == nil || p.phpIndex == nil {
		return true
	}
	if _, coreIndexed := p.phpIndex.FindClass(`Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition`); !coreIndexed {
		return true
	}
	_, found := p.phpIndex.FindClass(className)
	return found
}

func entityIssue(code, message string) entityschema.ValidationIssue {
	return entityschema.ValidationIssue{Code: code, Message: message, Severity: "error"}
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy, rightCopy := append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	for index := range leftCopy {
		if leftCopy[index] != rightCopy[index] {
			return false
		}
	}
	return true
}

func samePHPClass(left, right string) bool {
	return strings.EqualFold(strings.Trim(left, `\`), strings.Trim(right, `\`))
}

func entitySchemaSourcesFor(
	directory string,
	spec entityschema.EntitySpec,
	documents map[string]EntitySchemaDocument,
) (entitySchemaSources, error) {
	definitionPath, err := entitySchemaPath(spec.DefinitionURI, filepath.Join(directory, entityschema.ShortClass(spec.DefinitionClass)+".php"))
	if err != nil {
		return entitySchemaSources{}, err
	}
	readSource := func(path string) (entitySchemaSource, error) {
		content, version, exists, readErr := sourceForPath(path, documents)
		if readErr != nil {
			return entitySchemaSource{}, readErr
		}
		return entitySchemaSource{path: path, content: content, version: version, exists: exists}, nil
	}
	definition, err := readSource(definitionPath)
	if err != nil {
		return entitySchemaSources{}, err
	}
	sources := entitySchemaSources{definition: definition}
	var current *entityschema.EntitySpec
	if definition.content != "" {
		if imported, importErr := entityschema.ImportDefinition(definition.content, nil); importErr == nil {
			current = &imported
		}
	}
	entityClass := spec.EntityClass
	collectionClass := spec.CollectionClass
	entityURI := spec.EntityURI
	collectionURI := spec.CollectionURI
	if current != nil && current.DefinitionKind == entityschema.DefinitionEntity && spec.DefinitionKind != entityschema.DefinitionEntity {
		entityClass = current.EntityClass
		collectionClass = current.CollectionClass
	}
	if spec.DefinitionKind == entityschema.DefinitionEntity || current != nil && current.DefinitionKind == entityschema.DefinitionEntity {
		entityPath, pathErr := entitySchemaPath(entityURI, filepath.Join(directory, entityschema.ShortClass(entityClass)+".php"))
		if pathErr != nil {
			return entitySchemaSources{}, pathErr
		}
		collectionPath, pathErr := entitySchemaPath(collectionURI, filepath.Join(directory, entityschema.ShortClass(collectionClass)+".php"))
		if pathErr != nil {
			return entitySchemaSources{}, pathErr
		}
		entity, readErr := readSource(entityPath)
		if readErr != nil {
			return entitySchemaSources{}, readErr
		}
		collection, readErr := readSource(collectionPath)
		if readErr != nil {
			return entitySchemaSources{}, readErr
		}
		sources.entity = entity
		sources.collection = collection
	}
	translation := spec.Translation
	translationOwner := spec
	if (translation == nil || !translation.Enabled) && current != nil && current.Translation != nil && current.Translation.Enabled {
		translation = current.Translation
		translationOwner = *current
	}
	if translation != nil {
		translationDirectory := defaultTranslationDirectory(directory, translationOwner)
		translationDefinitionPath, pathErr := entitySchemaPath(translation.DefinitionURI, filepath.Join(translationDirectory, entityschema.ShortClass(translation.DefinitionClass)+".php"))
		if pathErr != nil {
			return entitySchemaSources{}, pathErr
		}
		translationEntityPath, pathErr := entitySchemaPath(translation.EntityURI, filepath.Join(translationDirectory, entityschema.ShortClass(translation.EntityClass)+".php"))
		if pathErr != nil {
			return entitySchemaSources{}, pathErr
		}
		translationCollectionPath, pathErr := entitySchemaPath(translation.CollectionURI, filepath.Join(translationDirectory, entityschema.ShortClass(translation.CollectionClass)+".php"))
		if pathErr != nil {
			return entitySchemaSources{}, pathErr
		}
		sources.translationDefinition, err = readSource(translationDefinitionPath)
		if err != nil {
			return entitySchemaSources{}, err
		}
		sources.translationEntity, err = readSource(translationEntityPath)
		if err != nil {
			return entitySchemaSources{}, err
		}
		sources.translationCollection, err = readSource(translationCollectionPath)
		if err != nil {
			return entitySchemaSources{}, err
		}
	}
	return sources, nil
}

func entitySchemaPath(uri, fallback string) (string, error) {
	if uri == "" {
		return fallback, nil
	}
	path, err := uriutil.Path(uri)
	if err != nil {
		return "", fmt.Errorf("resolve entity source URI: %w", err)
	}
	return path, nil
}

func defaultTranslationDirectory(parentDirectory string, spec entityschema.EntitySpec) string {
	return filepath.Join(parentDirectory, "Aggregate", spec.ClassName+"Translation")
}

func sourceForPath(path string, documents map[string]EntitySchemaDocument) (string, *int, bool, error) {
	uri := uriutil.FileURI(path)
	if document, found := documents[uri]; found {
		return document.Text, document.Version, true, nil
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, err
	}
	return string(content), nil, true, nil
}

func appendChangedFile(files *[]entitySchemaPreparedFile, path, before, after string, version *int, exists bool) {
	if before == after {
		return
	}
	*files = append(*files, entitySchemaPreparedFile{path: filepath.Clean(path), before: before, after: after, version: version, exists: exists})
}

func availableMigrationTimestamp(sourceRoot string, timestamp int64) int64 {
	for {
		matches, _ := filepath.Glob(filepath.Join(sourceRoot, "Migration", "Migration"+strconv.FormatInt(timestamp, 10)+"*.php"))
		if len(matches) == 0 {
			return timestamp
		}
		timestamp++
	}
}

func safePluginTarget(pluginRoot, target string) bool {
	canonicalRoot := resolvedDirectoryPath(pluginRoot)
	canonicalTarget := resolvedPathThroughExistingAncestor(target)
	relative, err := filepath.Rel(canonicalRoot, canonicalTarget)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func resolvedPathThroughExistingAncestor(path string) string {
	path = filepath.Clean(path)
	probe := path
	var suffix []string
	for {
		if _, err := os.Lstat(probe); err == nil {
			if resolved, resolveErr := filepath.EvalSymlinks(probe); resolveErr == nil {
				for index := len(suffix) - 1; index >= 0; index-- {
					resolved = filepath.Join(resolved, suffix[index])
				}
				return filepath.Clean(resolved)
			}
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		suffix = append(suffix, filepath.Base(probe))
		probe = parent
	}
	return path
}

func sameEntitySchema(left, right entityschema.Schema) bool {
	leftJSON, _ := json.Marshal(left.Normalize())
	rightJSON, _ := json.Marshal(right.Normalize())
	return string(leftJSON) == string(rightJSON)
}

func languageForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php":
		return "php"
	case ".xml":
		return "xml"
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	default:
		return "plaintext"
	}
}
