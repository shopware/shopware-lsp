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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
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
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Stored bool   `json:"stored"`
}

type EntitySchemaBootstrapResponse struct {
	Plugin     entityschema.PluginContext   `json:"plugin"`
	Spec       entityschema.EntitySpec      `json:"spec"`
	FieldTypes []EntitySchemaFieldType      `json:"fieldTypes"`
	Graph      EntitySchemaGraph            `json:"graph"`
	Existing   []EntitySchemaRelationTarget `json:"existing,omitempty"`
}

type EntitySchemaRelationTarget struct {
	EntityName      string                             `json:"entityName"`
	DefinitionClass string                             `json:"definitionClass"`
	EntityClass     string                             `json:"entityClass,omitempty"`
	CollectionClass string                             `json:"collectionClass,omitempty"`
	FileURI         string                             `json:"fileUri,omitempty"`
	Fields          []entityschema.RelationTargetField `json:"fields,omitempty"`
	VersionAware    bool                               `json:"versionAware,omitempty"`
}

type EntitySchemaSearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type EntitySchemaLoadRequest struct {
	DefinitionClass string                          `json:"definitionClass,omitempty"`
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
}

type entitySchemaSource struct {
	path    string
	content string
	version *int
	exists  bool
}

type entitySchemaSources struct {
	definition entitySchemaSource
	entity     entitySchemaSource
	collection entitySchemaSource
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
		Namespace: plugin.Namespace, ClassName: name, EntityName: snakeCase(name), CreateMigration: true,
		Fields: []entityschema.FieldSpec{
			{ID: "id", Kind: entityschema.FieldID, Editable: true},
			{ID: "created-at", Kind: entityschema.FieldCreatedAt, Editable: true},
			{ID: "updated-at", Kind: entityschema.FieldUpdatedAt, Editable: true},
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
	return EntitySchemaBootstrapResponse{
		Plugin: plugin, Spec: spec, Graph: EntitySchemaGraph{SnapshotCount: len(snapshots), Leaves: leaves, Missing: graph.Missing, NeedsReconciliation: len(graph.Leaves) > 1 || len(graph.Missing) > 0},
		FieldTypes: entitySchemaFieldTypes(), Existing: existing,
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
	query = strings.ToLower(strings.TrimSpace(query))
	var results []EntitySchemaRelationTarget
	for _, definition := range definitions {
		haystack := strings.ToLower(definition.Name + " " + definition.FullyQualifiedClass + " " + definition.Class)
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		target := EntitySchemaRelationTarget{EntityName: definition.Name, DefinitionClass: definition.FullyQualifiedClass, EntityClass: definition.EntityClass, CollectionClass: definition.CollectionClass, FileURI: uriutil.FileURI(definition.File), VersionAware: definition.VersionAware}
		for _, field := range definition.Fields {
			target.Fields = append(target.Fields, entityschema.RelationTargetField{PropertyName: field.Name, StorageName: field.StorageName, Primary: field.Primary})
		}
		results = append(results, target)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].EntityName < results[j].EntityName })
	return results, nil
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
	spec, err := p.importEntityDefinition(content)
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
	spec.EntityURI = uriutil.FileURI(filepath.Join(filepath.Dir(path), entityschema.ShortClass(spec.EntityClass)+".php"))
	spec.CollectionURI = uriutil.FileURI(filepath.Join(filepath.Dir(path), entityschema.ShortClass(spec.CollectionClass)+".php"))
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
		if entity, found := graph.Leaves[0].Snapshot.Schema.Entities[spec.EntityName]; found {
			spec.Indexes = entityschema.IndexSpecsFromEntity(spec, entity)
		}
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
	return entityschema.ImportDefinition(content, lookup)
}

func (p *Provider) entityRelationLookup() (entityschema.RelationLookup, error) {
	targets, err := p.relationTargets("")
	if err != nil {
		return nil, err
	}
	lookupMap := make(map[string]entityschema.RelationTarget, len(targets))
	for _, target := range targets {
		lookupMap[target.DefinitionClass] = entityschema.RelationTarget{DefinitionClass: target.DefinitionClass, EntityClass: target.EntityClass, CollectionClass: target.CollectionClass, EntityName: target.EntityName, FileURI: target.FileURI, Fields: target.Fields, VersionAware: target.VersionAware}
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
	prepared, err := p.prepareEntitySchema(ctx, request.EntitySchemaPreviewRequest)
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
		if !file.exists {
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
	response := EntitySchemaPreviewResponse{Issues: entityschema.ValidateSpec(spec), MigrationTimestamp: spec.MigrationTimestamp}
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
	if spec.PluginRootURI != "" && spec.PluginRootURI != uriutil.FileURI(plugin.Root) {
		return entitySchemaPrepared{}, errors.New("entity plugin root changed since the designer was opened")
	}
	if strings.Trim(spec.Namespace, `\`) != strings.Trim(plugin.Namespace, `\`) {
		return entitySchemaPrepared{}, fmt.Errorf("entity namespace %s does not match the Composer PSR-4 directory namespace %s", spec.Namespace, plugin.Namespace)
	}
	if generatedTimestamp {
		spec.MigrationTimestamp = availableMigrationTimestamp(plugin.SourceRoot, spec.MigrationTimestamp)
		response.MigrationTimestamp = spec.MigrationTimestamp
	}
	sources, err := entitySchemaSourcesFor(directory, spec, request.Documents)
	if err != nil {
		return entitySchemaPrepared{}, err
	}
	definitionPath := sources.definition.path
	definitionSource := sources.definition.content
	definitionVersion := sources.definition.version
	definitionExists := sources.definition.exists
	entityPath := sources.entity.path
	entitySource := sources.entity.content
	entityVersion := sources.entity.version
	entityExists := sources.entity.exists
	collectionPath := sources.collection.path
	collectionSource := sources.collection.content
	collectionVersion := sources.collection.version
	collectionExists := sources.collection.exists
	relationLookup, err := p.entityRelationLookup()
	if err != nil {
		return entitySchemaPrepared{}, err
	}
	scanned, _, err := entityschema.ScanPluginSchemaWithLookup(plugin.Root, relationLookup)
	if err != nil {
		return entitySchemaPrepared{}, err
	}
	var previousSpec *entityschema.EntitySpec
	if spec.Mode == "edit" && definitionSource != "" {
		imported, importErr := p.importEntityDefinition(definitionSource)
		if importErr != nil {
			return entitySchemaPrepared{}, fmt.Errorf("import current entity definition: %w", importErr)
		}
		previousSpec = &imported
		currentEntity, schemaErr := entityschema.SchemaFromSpec(imported)
		if schemaErr != nil {
			return entitySchemaPrepared{}, fmt.Errorf("normalize current entity definition: %w", schemaErr)
		}
		scanned.Entities[imported.EntityName] = currentEntity
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
	next := scanned.Clone()
	entity, err := entityschema.SchemaFromSpec(spec)
	if err != nil && len(response.Issues) == 0 {
		return entitySchemaPrepared{}, err
	}
	if err == nil {
		next.Entities[spec.EntityName] = entity
	}
	response.Diff = entityschema.DiffSchemas(previous, next)
	response.Destructive = response.Diff.Destructive()
	if len(response.Diff.RenameQuestions) != 0 {
		if _, _, decisionErr := entityschema.ResolveRenameQuestions(response.Diff, request.Decisions); decisionErr != nil {
			response.Issues = append(response.Issues, entityIssue("entity.column.rename.decision", decisionErr.Error()))
		}
	}
	response.Issues = append(response.Issues, entityschema.ValidateMigration(previous, next, response.Diff, request.Decisions)...)
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
	definitionAfter, entityAfter, collectionAfter := "", "", ""
	if spec.Mode == "edit" && definitionSource != "" {
		if previousSpec == nil {
			return entitySchemaPrepared{}, errors.New("cannot safely edit an entity that was not imported from the plugin")
		}
		definitionAfter, err = entityschema.RewriteDefinition(definitionSource, spec)
		if err == nil {
			entityAfter, err = entityschema.RewriteEntity(entitySource, *previousSpec, spec)
		}
		if collectionSource != "" {
			collectionAfter = collectionSource
		} else {
			collectionAfter, err = entityschema.RenderCollection(spec)
		}
	} else {
		definitionAfter, err = entityschema.RenderDefinition(spec)
		if err == nil {
			entityAfter, err = entityschema.RenderEntity(spec)
		}
		if err == nil {
			collectionAfter, err = entityschema.RenderCollection(spec)
		}
	}
	if err != nil {
		return entitySchemaPrepared{}, err
	}
	appendChangedFile(&pending, definitionPath, definitionSource, definitionAfter, definitionVersion, definitionExists)
	appendChangedFile(&pending, entityPath, entitySource, entityAfter, entityVersion, entityExists)
	appendChangedFile(&pending, collectionPath, collectionSource, collectionAfter, collectionVersion, collectionExists)
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
	serviceAfter, err := entityschema.PatchServiceConfiguration(servicePath, serviceSource, spec.DefinitionClass)
	if err != nil {
		return entitySchemaPrepared{}, err
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
		migrations = append(migrations, entityschema.MigrationReference{Path: filepath.ToSlash(relative), Class: plugin.BaseNamespace + `\Migration\` + className, Timestamp: timestamp, SHA256: entityschema.FileSHA256([]byte(migration))})
	}
	snapshot, err := (entityschema.Snapshot{Parents: parents, Kind: entityschema.SnapshotMigration, Plugin: entityschema.PluginIdentity{ComposerName: plugin.ComposerName, PluginClass: plugin.PluginClass}, ShopwareVersion: plugin.ShopwareVersion, Migrations: migrations, Schema: next, Decisions: request.Decisions}).Seal()
	if err != nil {
		return entitySchemaPrepared{}, err
	}
	snapshotContent, err := entityschema.MarshalSnapshot(snapshot)
	if err != nil {
		return entitySchemaPrepared{}, err
	}
	appendChangedFile(&pending, filepath.Join(plugin.SnapshotDirectory, strconv.FormatInt(timestamp, 10)+"-"+snapshot.ID[:12]+".snapshot.json"), "", string(snapshotContent), nil, false)
	response.SnapshotID = snapshot.ID
	response.PrimaryFileURI = uriutil.FileURI(definitionPath)
	for _, file := range pending {
		if !safePluginTarget(plugin.Root, file.path) {
			return entitySchemaPrepared{}, fmt.Errorf("entity output is outside plugin %s: %s", plugin.Root, file.path)
		}
		response.Files = append(response.Files, EntitySchemaFilePreview{URI: uriutil.FileURI(file.path), Action: map[bool]string{true: "update", false: "create"}[file.exists], Language: languageForPath(file.path), Before: file.before, After: file.after})
	}
	revisionRequest := request
	revisionRequest.Spec = spec
	revisionContent, _ := json.Marshal(struct {
		Request EntitySchemaPreviewRequest
		Files   []EntitySchemaFilePreview
	}{revisionRequest, response.Files})
	hash := sha256.Sum256(revisionContent)
	response.Revision = hex.EncodeToString(hash[:])
	return entitySchemaPrepared{response: response, files: pending}, nil
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
		Kind: entityschema.SnapshotBaseline,
		Plugin: entityschema.PluginIdentity{
			ComposerName: plugin.ComposerName,
			PluginClass:  plugin.PluginClass,
		},
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
	response.DriftMessage = "Entity definitions differ from the latest committed snapshot. Choose whether to adopt the current code as a baseline or generate migration SQL for it."
	switch driftDecision {
	case "adopt":
		return adoptEntitySchemaDrift(plugin, leaf, spec, history)
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
	leaf entityschema.Snapshot,
	spec entityschema.EntitySpec,
	history entitySchemaHistory,
) (entitySchemaHistory, error) {
	adopted, err := (entityschema.Snapshot{
		Parents:         history.parents,
		Kind:            entityschema.SnapshotBaseline,
		Plugin:          leaf.Plugin,
		ShopwareVersion: leaf.ShopwareVersion,
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
	merged, err := (entityschema.Snapshot{Parents: parents, Kind: entityschema.SnapshotMerge, Plugin: selectedFile.Snapshot.Plugin, ShopwareVersion: selectedFile.Snapshot.ShopwareVersion, Schema: selectedFile.Snapshot.Schema, Decisions: []entityschema.Decision{{Kind: "branchReconcile", Reason: "selected " + selected}}}).Seal()
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

func entitySchemaFieldTypes() []EntitySchemaFieldType {
	return []EntitySchemaFieldType{
		{"id", "Primary ID", true},
		{"auto-increment", "Auto increment", true},
		{"version", "Version ID", true},
		{"reference-version", "Reference version", true},
		{"string", "String", true},
		{"long-text", "Long text", true},
		{"blob", "Blob", true},
		{"int", "Integer", true},
		{"float", "Float", true},
		{"bool", "Boolean", true},
		{"date", "Date", true},
		{"datetime", "Date/time", true},
		{"json", "JSON", true},
		{"list", "List (JSON)", true},
		{"object", "Object (JSON)", true},
		{"created-at", "Created at", true},
		{"updated-at", "Updated at", true},
		{"many-to-one", "Many to one", true},
		{"one-to-one", "One to one", true},
		{"one-to-many", "One to many", false},
		{"many-to-many", "Many to many", false},
	}
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

func entitySchemaSourcesFor(
	directory string,
	spec entityschema.EntitySpec,
	documents map[string]EntitySchemaDocument,
) (entitySchemaSources, error) {
	paths := []string{
		filepath.Join(directory, entityschema.ShortClass(spec.DefinitionClass)+".php"),
		filepath.Join(directory, entityschema.ShortClass(spec.EntityClass)+".php"),
		filepath.Join(directory, entityschema.ShortClass(spec.CollectionClass)+".php"),
	}
	result := make([]entitySchemaSource, len(paths))
	for index, path := range paths {
		content, version, exists, err := sourceForPath(path, documents)
		if err != nil {
			return entitySchemaSources{}, err
		}
		result[index] = entitySchemaSource{
			path:    path,
			content: content,
			version: version,
			exists:  exists,
		}
	}
	return entitySchemaSources{
		definition: result[0],
		entity:     result[1],
		collection: result[2],
	}, nil
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
