package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shopware/shopware-lsp/internal/integration"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/lsp/scaffold"
	"github.com/shopware/shopware-lsp/internal/shopware/entityschema"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type scaffoldCatalogInput struct{}

type scaffoldCatalogOutput struct {
	ProtocolVersion int                              `json:"protocolVersion"`
	Scaffolds       []integration.ScaffoldDefinition `json:"scaffolds"`
	EntitySchema    bool                             `json:"entitySchema"`
}

type scaffoldInput struct {
	Family    string         `json:"family" jsonschema:"scaffold family: use shopware for Shopware plugins and artifacts, or symfony for Symfony artifacts"`
	Kind      string         `json:"kind" jsonschema:"artifact kind; use plugin whenever the user asks to create, generate, scaffold, or initialize a Shopware plugin"`
	Directory string         `json:"directory" jsonschema:"workspace-relative or absolute target directory; for a new Shopware plugin pass its parent directory, usually custom/plugins"`
	Name      string         `json:"name" jsonschema:"artifact name, for example FroshTools for a new plugin"`
	Options   map[string]any `json:"options,omitempty" jsonschema:"kind-specific options; plugin supports namespace, description, author, license, and package"`
	Write     bool           `json:"write,omitempty" jsonschema:"write generated files; set true when the user requested creation, otherwise false returns a preview diff only"`
}

type scaffoldOutput struct {
	Family          string   `json:"family"`
	Kind            string   `json:"kind"`
	Applied         bool     `json:"applied"`
	PrimaryFile     string   `json:"primaryFile"`
	Files           []string `json:"files"`
	Diff            string   `json:"diff"`
	ShopwareVersion string   `json:"shopwareVersion,omitempty"`
}

type entitySchemaBootstrapInput struct {
	Directory string `json:"directory" jsonschema:"workspace-relative or absolute directory inside the target Shopware plugin; call this first for every create or edit entity, mapping-definition, EntityExtension, or BulkEntityExtension request"`
}

type entitySchemaFieldTypeSummary struct {
	ID                            string `json:"id"`
	Kind                          string `json:"kind"`
	Label                         string `json:"label"`
	Stored                        bool   `json:"stored"`
	Specialized                   bool   `json:"specialized,omitempty"`
	RequiresDefaultFieldsOverride bool   `json:"requiresDefaultFieldsOverride,omitempty"`
}

type entitySchemaBootstrapOutput struct {
	Plugin          entityschema.PluginContext            `json:"plugin"`
	Spec            entityschema.EntitySpec               `json:"spec"`
	DefinitionKinds []entityschema.DefinitionKind         `json:"definitionKinds"`
	FieldTypes      []entitySchemaFieldTypeSummary        `json:"fieldTypes"`
	Graph           scaffold.EntitySchemaGraph            `json:"graph"`
	Editable        []scaffold.EntitySchemaEditableTarget `json:"editable,omitempty"`
	NextAction      string                                `json:"nextAction"`
}

type entitySchemaFieldTypesInput struct {
	Directory      string                      `json:"directory" jsonschema:"same plugin directory passed to shopware_entity_schema_bootstrap"`
	ID             string                      `json:"id,omitempty" jsonschema:"exact field type id returned by bootstrap.fieldTypes or by a catalog listing; provide this to get one copyable template"`
	Query          string                      `json:"query,omitempty" jsonschema:"optional case-insensitive id, kind, or label filter when listing field types"`
	DefinitionKind entityschema.DefinitionKind `json:"definitionKind,omitempty" jsonschema:"optional entity, mapping, extension, or bulk-extension filter"`
	Limit          int                         `json:"limit,omitempty" jsonschema:"maximum compact results; defaults to 100 and cannot exceed 200"`
}

type entitySchemaFieldTypeDetail struct {
	entitySchemaFieldTypeSummary
	DefinitionKinds []entityschema.DefinitionKind `json:"definitionKinds,omitempty"`
	Template        map[string]any                `json:"template,omitempty"`
	Usage           string                        `json:"usage,omitempty"`
}

type entitySchemaFieldTypesOutput struct {
	FieldTypes []entitySchemaFieldTypeDetail `json:"fieldTypes"`
	NextAction string                        `json:"nextAction"`
}

type entitySchemaSearchInput struct {
	Query string `json:"query,omitempty" jsonschema:"entity name or definition class query used to resolve relation targets after bootstrap"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum relation targets; defaults to 50 and cannot exceed 200"`
}

type entitySchemaSearchOutput struct {
	Results []scaffold.EntitySchemaRelationTarget `json:"results"`
}

type entitySchemaLoadInput struct {
	DefinitionClass string                      `json:"definitionClass,omitempty" jsonschema:"existing indexed definition class to edit after bootstrapping its plugin"`
	DefinitionKind  entityschema.DefinitionKind `json:"definitionKind,omitempty" jsonschema:"effective class kind from bootstrap.editable; required with definitionClass for classes using custom abstract DAL bases"`
	EntityName      string                      `json:"entityName,omitempty" jsonschema:"existing indexed technical entity name to edit"`
	Path            string                      `json:"path,omitempty" jsonschema:"workspace-relative or absolute existing entity definition file to edit"`
}

type entitySchemaPreviewInput struct {
	Spec          entityschema.EntitySpec `json:"spec" jsonschema:"typed entity, mapping-definition, EntityExtension, or multi-target BulkEntityExtension specification returned by bootstrap or load"`
	Decisions     []entityschema.Decision `json:"decisions,omitempty" jsonschema:"answers to rename questions returned by a previous preview"`
	DriftDecision string                  `json:"driftDecision,omitempty" jsonschema:"adopt or migrate when preview reports manual schema drift"`
}

type entitySchemaPreviewFile struct {
	Path      string `json:"path"`
	Action    string `json:"action"`
	Language  string `json:"language"`
	SizeBytes int    `json:"sizeBytes"`
}

type entitySchemaDiffSummary struct {
	CreatedEntities       []string                            `json:"createdEntities,omitempty"`
	RemovedEntities       []string                            `json:"removedEntities,omitempty"`
	AddedColumns          []string                            `json:"addedColumns,omitempty"`
	RemovedColumns        []string                            `json:"removedColumns,omitempty"`
	ChangedColumns        []string                            `json:"changedColumns,omitempty"`
	RenameQuestions       []entityschema.RenameQuestion       `json:"renameQuestions,omitempty"`
	EntityRenameQuestions []entityschema.EntityRenameQuestion `json:"entityRenameQuestions,omitempty"`
	AddedIndexes          []string                            `json:"addedIndexes,omitempty"`
	RemovedIndexes        []string                            `json:"removedIndexes,omitempty"`
	AddedForeignKeys      []string                            `json:"addedForeignKeys,omitempty"`
	RemovedForeignKeys    []string                            `json:"removedForeignKeys,omitempty"`
	ChangedPrimaryKeys    []string                            `json:"changedPrimaryKeys,omitempty"`
}

type entitySchemaPreviewOutput struct {
	Status                          string                         `json:"status"`
	ReadyToApply                    bool                           `json:"readyToApply"`
	NextAction                      string                         `json:"nextAction"`
	Revision                        string                         `json:"revision,omitempty"`
	Files                           []entitySchemaPreviewFile      `json:"files,omitempty"`
	Issues                          []entityschema.ValidationIssue `json:"issues,omitempty"`
	Diff                            entitySchemaDiffSummary        `json:"diff"`
	Destructive                     bool                           `json:"destructive"`
	RequiresDestructiveConfirmation bool                           `json:"requiresDestructiveConfirmation"`
	Drift                           bool                           `json:"drift"`
	DriftMessage                    string                         `json:"driftMessage,omitempty"`
	SnapshotID                      string                         `json:"snapshotId,omitempty"`
	PrimaryFile                     string                         `json:"primaryFile,omitempty"`
	MigrationTimestamp              int64                          `json:"migrationTimestamp,omitempty"`
}

type entitySchemaApplyInput struct {
	entitySchemaPreviewInput
	Revision         string `json:"revision" jsonschema:"exact opaque revision returned by shopware_entity_schema_preview; it carries the generated migration timestamp, so do not refresh either value"`
	AllowDestructive bool   `json:"allowDestructive,omitempty" jsonschema:"explicitly permit destructive migration changes"`
}

type entitySchemaReconcileInput struct {
	Directory    string `json:"directory" jsonschema:"workspace-relative or absolute directory inside a Shopware plugin"`
	SelectedLeaf string `json:"selectedLeaf,omitempty" jsonschema:"authoritative snapshot leaf when branches differ"`
}

type entitySchemaWriteOutput struct {
	Applied     bool     `json:"applied"`
	PrimaryFile string   `json:"primaryFile"`
	SnapshotID  string   `json:"snapshotId"`
	Files       []string `json:"files"`
	Diff        string   `json:"diff"`
}

func registerMCPScaffoldTools(
	server *mcp.Server,
	runtime *mcpRuntime,
	readOnly *mcp.ToolAnnotations,
) {
	write := &mcp.ToolAnnotations{
		ReadOnlyHint: false, IdempotentHint: false,
		OpenWorldHint: boolPointer(false),
	}
	destructiveWrite := &mcp.ToolAnnotations{
		ReadOnlyHint: false, IdempotentHint: false,
		DestructiveHint: boolPointer(true), OpenWorldHint: boolPointer(false),
	}
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_scaffold_catalog", Title: "Shopware scaffold catalog",
		Description: "List Shopware and Symfony artifacts with their production workflow and typed options. Entries with workflow=entity-schema must use the dedicated entity-schema tools instead of shopware_scaffold.",
		Annotations: readOnly,
	}, runtime.scaffoldCatalog)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_scaffold", Title: "Create Shopware plugin or scaffold",
		Description: "Always use this production generator when the user asks to create, generate, scaffold, or initialize a Shopware plugin or another supported Shopware/Symfony artifact. For a plugin use family=shopware, kind=plugin, and its parent directory (usually custom/plugins). Set write=true when file creation was requested; otherwise it returns a non-writing unified diff.",
		Annotations: write,
	}, runtime.scaffold)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_entity_schema_bootstrap", Title: "Start creating or editing a Shopware DAL entity, mapping, or extension",
		Description: "Always call this first for a Shopware DAL entity, definition, MappingEntityDefinition, EntityExtension, or BulkEntityExtension. The compact result contains the exact spec to modify, available definition kinds and field-type ids, snapshot state, and editable local classes. It intentionally omits relation catalogs and large templates: use shopware_entity_schema_search for a target and shopware_entity_schema_field_types with an exact id for a copyable field template. Use kind=hierarchy for trees and inheritanceAware=true for variants. Never search content.json or temporary MCP payload files. Preserve loaded *MethodRaw values and use preview/apply rather than writing generated files manually.",
		Annotations: readOnly,
	}, runtime.entitySchemaBootstrap)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_entity_schema_field_types", Title: "List or inspect Shopware DAL field types",
		Description: "Use after entity-schema bootstrap instead of inspecting content.json. With no id it returns a small searchable list of field ids, kinds, labels, and storage behavior. With an exact id it returns one copyable field template and focused usage guidance. Keep specialized template implementation metadata unchanged. Set translated=true on an ordinary scalar field when translation storage is wanted; createdAt and updatedAt are inherited framework fields and normally must not be added.",
		Annotations: readOnly,
	}, runtime.entitySchemaFieldTypes)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_entity_schema_search", Title: "Find Shopware DAL relation targets",
		Description: "Use after entity-schema bootstrap when a requested association needs an indexed target definition, entity name, fields, or collection class. Feed the selected target into the typed spec before previewing.",
		Annotations: readOnly,
	}, runtime.entitySchemaSearch)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_entity_schema_load", Title: "Load an existing Shopware DAL entity, mapping, or extension for editing",
		Description: "Use after bootstrapping the plugin when the user asks to edit an existing DAL entity, mapping definition, EntityExtension, or BulkEntityExtension. Select it from bootstrap.editable and pass its path, definitionClass, and definitionKind so custom abstract DAL bases remain resolvable. Preserve the loaded definitionKind, all extension targets, and every locked *MethodRaw value, then send the modified typed spec to entity-schema preview.",
		Annotations: readOnly,
	}, runtime.entitySchemaLoad)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_entity_schema_preview", Title: "Validate and preview a Shopware DAL entity, mapping, or extension",
		Description: "Required after bootstrap/load and before apply. Validate the modified typed spec and return a compact, self-contained summary of all PHP, services.yaml, migration, and committed snapshot changes without writing. Generated file bodies are intentionally omitted to keep the MCP result inline; do not search temporary VS Code payloads. Resolve every returned issue or rename/drift question and preview again. For entityRenameQuestions answer with kind=entityRename, entity/to equal the added table, and from equal the selected old table; use kind=entityCreate when it is intentionally new. When status=ready, call apply once with the same spec and exact opaque revision without rerunning preview.",
		Annotations: readOnly,
	}, runtime.entitySchemaPreview)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_entity_schema_apply", Title: "Create or update the Shopware DAL entity, mapping, or extension",
		Description: "Final entity workflow step: write the exact clean revision returned by entity-schema preview. Reuse the previewed spec and opaque revision without refreshing the generated migration timestamp. Use it when the user requested creation or editing; never reproduce the preview with manual file edits. Destructive changes require explicit user confirmation and allowDestructive=true.",
		Annotations: destructiveWrite,
	}, runtime.entitySchemaApply)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_entity_schema_reconcile", Title: "Repair divergent entity-schema history",
		Description: "Use only when bootstrap or preview reports divergent committed entity-schema snapshot leaves. Create a merge snapshot, then restart the bootstrap/preview/apply workflow.",
		Annotations: write,
	}, runtime.entitySchemaReconcile)
}

func (runtime *mcpRuntime) scaffoldCatalog(
	context.Context,
	*mcp.CallToolRequest,
	scaffoldCatalogInput,
) (*mcp.CallToolResult, scaffoldCatalogOutput, error) {
	return nil, scaffoldCatalogOutput{
		ProtocolVersion: integration.ProtocolVersion,
		Scaffolds:       integration.Scaffolds(),
		EntitySchema:    true,
	}, nil
}

func (runtime *mcpRuntime) scaffold(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input scaffoldInput,
) (*mcp.CallToolResult, scaffoldOutput, error) {
	family := strings.ToLower(strings.TrimSpace(input.Family))
	if family != "shopware" && family != "symfony" {
		return nil, scaffoldOutput{}, errors.New("family must be shopware or symfony")
	}
	kind := strings.ToLower(strings.TrimSpace(input.Kind))
	if kind == "" {
		return nil, scaffoldOutput{}, errors.New("kind is required")
	}
	directory, err := runtime.resolvePath(input.Directory)
	if err != nil {
		return nil, scaffoldOutput{}, err
	}
	if strings.TrimSpace(input.Name) == "" {
		return nil, scaffoldOutput{}, errors.New("name is required")
	}
	output, err := withMCPSession(ctx, runtime, func(session *cliSession) (scaffoldOutput, error) {
		edit, primary, version, commandErr := runtime.scaffoldEdit(
			ctx, session, family, kind, directory, input,
		)
		if commandErr != nil {
			return scaffoldOutput{}, commandErr
		}
		files, diff, applyErr := runtime.applyMCPWorkspaceEdit(edit, input.Write)
		if applyErr != nil {
			return scaffoldOutput{}, applyErr
		}
		return scaffoldOutput{
			Family: family, Kind: kind, Applied: input.Write,
			PrimaryFile: runtime.outputURI(primary), Files: files,
			Diff: diff, ShopwareVersion: version,
		}, nil
	})
	return nil, output, err
}

func (runtime *mcpRuntime) scaffoldEdit(
	ctx context.Context,
	session *cliSession,
	family,
	kind,
	directory string,
	input scaffoldInput,
) (*protocol.WorkspaceEdit, string, string, error) {
	directoryURI := uriutil.FileURI(directory)
	if family == "symfony" {
		response, err := callMCPCommand[scaffold.Request, scaffold.Response](
			ctx, session, scaffold.CreateSymfonyScaffoldCommand,
			scaffold.Request{Kind: kind, DirectoryURI: directoryURI, Name: input.Name},
		)
		if err != nil {
			return nil, "", "", err
		}
		edit := createFileWorkspaceEdit(response.FileURI, response.Content)
		return edit, response.FileURI, "", nil
	}
	response, err := callMCPCommand[scaffold.ShopwareRequest, scaffold.ShopwareResponse](
		ctx, session, scaffold.CreateShopwareScaffoldCommand,
		scaffold.ShopwareRequest{
			Kind: kind, DirectoryURI: directoryURI,
			Name: input.Name, Options: input.Options,
		},
	)
	if err != nil {
		return nil, "", "", err
	}
	return response.Edit, response.PrimaryFileURI, response.ShopwareVersion, nil
}

func createFileWorkspaceEdit(uri, content string) *protocol.WorkspaceEdit {
	return &protocol.WorkspaceEdit{DocumentChanges: []protocol.DocumentChange{
		{Kind: protocol.CreateFileOperation, URI: uri},
		{
			TextDocument: &protocol.OptionalVersionedTextDocumentIdentifier{URI: uri},
			Edits:        []protocol.TextEdit{{Range: protocol.Range{}, NewText: content}},
		},
	}}
}

func callMCPCommand[Request, Response any](
	ctx context.Context,
	session *cliSession,
	command string,
	request Request,
) (Response, error) {
	var response Response
	if err := session.call(ctx, command, request, &response); err != nil {
		return response, fmt.Errorf("%s: %w", command, err)
	}
	return response, nil
}

func (runtime *mcpRuntime) applyMCPWorkspaceEdit(
	edit *protocol.WorkspaceEdit,
	write bool,
) ([]string, string, error) {
	if err := runtime.validateWorkspaceEdit(edit); err != nil {
		return nil, "", err
	}
	var diff bytes.Buffer
	if err := applyWorkspaceEdit(&diff, edit, editMode{Write: write, Diff: true}); err != nil {
		return nil, "", err
	}
	return runtime.workspaceEditPaths(edit), diff.String(), nil
}

func (runtime *mcpRuntime) entitySchemaBootstrap(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input entitySchemaBootstrapInput,
) (*mcp.CallToolResult, entitySchemaBootstrapOutput, error) {
	output, err := runtime.entitySchemaBootstrapResponse(ctx, input.Directory)
	if err != nil {
		return nil, entitySchemaBootstrapOutput{}, err
	}
	return nil, compactEntitySchemaBootstrap(output), nil
}

func (runtime *mcpRuntime) entitySchemaBootstrapResponse(
	ctx context.Context,
	directory string,
) (scaffold.EntitySchemaBootstrapResponse, error) {
	directoryURI, err := runtime.inputURI(directory, true)
	if err != nil {
		return scaffold.EntitySchemaBootstrapResponse{}, err
	}
	output, err := withMCPSession(ctx, runtime, func(session *cliSession) (scaffold.EntitySchemaBootstrapResponse, error) {
		return callMCPCommand[scaffold.EntitySchemaBootstrapRequest, scaffold.EntitySchemaBootstrapResponse](
			ctx, session, scaffold.EntitySchemaBootstrapCommand,
			scaffold.EntitySchemaBootstrapRequest{DirectoryURI: directoryURI},
		)
	})
	if err == nil {
		runtime.normalizeBootstrapOutput(&output)
	}
	return output, err
}

func (runtime *mcpRuntime) entitySchemaFieldTypes(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input entitySchemaFieldTypesInput,
) (*mcp.CallToolResult, entitySchemaFieldTypesOutput, error) {
	limit, err := boundedLimit(input.Limit, 100, 200, "limit")
	if err != nil {
		return nil, entitySchemaFieldTypesOutput{}, err
	}
	bootstrap, err := runtime.entitySchemaBootstrapResponse(ctx, input.Directory)
	if err != nil {
		return nil, entitySchemaFieldTypesOutput{}, err
	}
	if input.DefinitionKind != "" && !containsDefinitionKind(bootstrap.DefinitionKinds, input.DefinitionKind) {
		return nil, entitySchemaFieldTypesOutput{}, fmt.Errorf(
			"definition kind %q is unavailable for Shopware constraint %q",
			input.DefinitionKind, bootstrap.Plugin.ShopwareVersion,
		)
	}
	exactID := strings.TrimSpace(input.ID)
	query := strings.ToLower(strings.TrimSpace(input.Query))
	result := entitySchemaFieldTypesOutput{}
	for _, fieldType := range bootstrap.FieldTypes {
		id := entitySchemaFieldTypeID(fieldType)
		if exactID != "" && !strings.EqualFold(id, exactID) {
			continue
		}
		if input.DefinitionKind != "" && !containsDefinitionKind(fieldType.DefinitionKinds, input.DefinitionKind) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(id+" "+fieldType.Kind+" "+fieldType.Label), query) {
			continue
		}
		detail := entitySchemaFieldTypeDetail{entitySchemaFieldTypeSummary: summarizeEntitySchemaFieldType(fieldType)}
		if exactID != "" {
			detail.DefinitionKinds = append([]entityschema.DefinitionKind(nil), fieldType.DefinitionKinds...)
			detail.Template = entitySchemaFieldTemplate(fieldType)
			detail.Usage = entitySchemaFieldTypeUsage(fieldType)
		}
		result.FieldTypes = append(result.FieldTypes, detail)
		if len(result.FieldTypes) >= limit {
			break
		}
	}
	if exactID != "" && len(result.FieldTypes) == 0 {
		return nil, entitySchemaFieldTypesOutput{}, fmt.Errorf(
			"field type %q is unavailable; call shopware_entity_schema_field_types without id to list valid ids",
			exactID,
		)
	}
	if exactID == "" {
		result.NextAction = "Call shopware_entity_schema_field_types again with one exact id to receive its copyable template; do not inspect content.json or temporary payload files."
	} else {
		result.NextAction = "Copy template into the bootstrapped spec.fields, replace the example identifiers and requested options, then call shopware_entity_schema_preview with the complete spec."
	}
	return nil, result, nil
}

func (runtime *mcpRuntime) entitySchemaSearch(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input entitySchemaSearchInput,
) (*mcp.CallToolResult, entitySchemaSearchOutput, error) {
	limit, err := boundedLimit(input.Limit, 50, 200, "limit")
	if err != nil {
		return nil, entitySchemaSearchOutput{}, err
	}
	output, err := withMCPSession(ctx, runtime, func(session *cliSession) ([]scaffold.EntitySchemaRelationTarget, error) {
		return callMCPCommand[scaffold.EntitySchemaSearchRequest, []scaffold.EntitySchemaRelationTarget](
			ctx, session, scaffold.EntitySchemaSearchCommand,
			scaffold.EntitySchemaSearchRequest{Query: input.Query, Limit: limit},
		)
	})
	if err == nil {
		runtime.normalizeRelationTargets(output)
	}
	return nil, entitySchemaSearchOutput{Results: output}, err
}

func (runtime *mcpRuntime) entitySchemaLoad(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input entitySchemaLoadInput,
) (*mcp.CallToolResult, entityschema.EntitySpec, error) {
	fileURI, err := runtime.inputURI(input.Path, false)
	if err != nil {
		return nil, entityschema.EntitySpec{}, err
	}
	if strings.TrimSpace(input.DefinitionClass) == "" &&
		strings.TrimSpace(input.EntityName) == "" && fileURI == "" {
		return nil, entityschema.EntitySpec{}, errors.New("definitionClass, entityName, or path is required")
	}
	output, err := withMCPSession(ctx, runtime, func(session *cliSession) (entityschema.EntitySpec, error) {
		return callMCPCommand[scaffold.EntitySchemaLoadRequest, entityschema.EntitySpec](
			ctx, session, scaffold.EntitySchemaLoadCommand,
			scaffold.EntitySchemaLoadRequest{
				DefinitionClass: input.DefinitionClass,
				DefinitionKind:  input.DefinitionKind,
				EntityName:      input.EntityName, FileURI: fileURI,
			},
		)
	})
	if err == nil {
		runtime.normalizeEntitySpecOutput(&output)
	}
	return nil, output, err
}

func (runtime *mcpRuntime) entitySchemaPreview(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input entitySchemaPreviewInput,
) (*mcp.CallToolResult, entitySchemaPreviewOutput, error) {
	request, err := runtime.entitySchemaPreviewRequest(input)
	if err != nil {
		return nil, entitySchemaPreviewOutput{}, err
	}
	output, err := withMCPSession(ctx, runtime, func(session *cliSession) (scaffold.EntitySchemaPreviewResponse, error) {
		return callMCPCommand[scaffold.EntitySchemaPreviewRequest, scaffold.EntitySchemaPreviewResponse](
			ctx, session, scaffold.EntitySchemaPreviewCommand, request,
		)
	})
	if err != nil {
		return nil, entitySchemaPreviewOutput{}, err
	}
	return nil, runtime.entitySchemaPreviewOutput(output), nil
}

func (runtime *mcpRuntime) entitySchemaApply(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input entitySchemaApplyInput,
) (*mcp.CallToolResult, entitySchemaWriteOutput, error) {
	if strings.TrimSpace(input.Revision) == "" {
		return nil, entitySchemaWriteOutput{}, errors.New("revision is required")
	}
	preview, err := runtime.entitySchemaPreviewRequest(input.entitySchemaPreviewInput)
	if err != nil {
		return nil, entitySchemaWriteOutput{}, err
	}
	request := scaffold.EntitySchemaApplyRequest{
		EntitySchemaPreviewRequest: preview,
		Revision:                   input.Revision, AllowDestructive: input.AllowDestructive,
	}
	output, err := withMCPSession(ctx, runtime, func(session *cliSession) (entitySchemaWriteOutput, error) {
		response, callErr := callMCPCommand[scaffold.EntitySchemaApplyRequest, scaffold.EntitySchemaApplyResponse](
			ctx, session, scaffold.EntitySchemaApplyCommand, request,
		)
		if callErr != nil {
			return entitySchemaWriteOutput{}, callErr
		}
		return runtime.applyEntitySchemaResponse(response)
	})
	return nil, output, err
}

func (runtime *mcpRuntime) entitySchemaReconcile(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input entitySchemaReconcileInput,
) (*mcp.CallToolResult, entitySchemaWriteOutput, error) {
	directoryURI, err := runtime.inputURI(input.Directory, true)
	if err != nil {
		return nil, entitySchemaWriteOutput{}, err
	}
	output, err := withMCPSession(ctx, runtime, func(session *cliSession) (entitySchemaWriteOutput, error) {
		response, callErr := callMCPCommand[scaffold.EntitySchemaReconcileRequest, scaffold.EntitySchemaApplyResponse](
			ctx, session, scaffold.EntitySchemaReconcileCommand,
			scaffold.EntitySchemaReconcileRequest{
				DirectoryURI: directoryURI, SelectedLeaf: input.SelectedLeaf,
			},
		)
		if callErr != nil {
			return entitySchemaWriteOutput{}, callErr
		}
		return runtime.applyEntitySchemaResponse(response)
	})
	return nil, output, err
}

func (runtime *mcpRuntime) entitySchemaPreviewRequest(
	input entitySchemaPreviewInput,
) (scaffold.EntitySchemaPreviewRequest, error) {
	spec := input.Spec
	for _, value := range []struct {
		name     string
		target   *string
		required bool
	}{
		{"spec.pluginRootUri", &spec.PluginRootURI, true},
		{"spec.directoryUri", &spec.DirectoryURI, true},
		{"spec.definitionUri", &spec.DefinitionURI, false},
		{"spec.entityUri", &spec.EntityURI, false},
		{"spec.collectionUri", &spec.CollectionURI, false},
		{"spec.serviceUri", &spec.ServiceURI, false},
	} {
		uri, err := runtime.inputURI(*value.target, value.required)
		if err != nil {
			return scaffold.EntitySchemaPreviewRequest{}, fmt.Errorf("%s: %w", value.name, err)
		}
		*value.target = uri
	}
	if spec.Translation != nil {
		for _, value := range []struct {
			name   string
			target *string
		}{
			{"spec.translation.definitionUri", &spec.Translation.DefinitionURI},
			{"spec.translation.entityUri", &spec.Translation.EntityURI},
			{"spec.translation.collectionUri", &spec.Translation.CollectionURI},
		} {
			uri, err := runtime.inputURI(*value.target, false)
			if err != nil {
				return scaffold.EntitySchemaPreviewRequest{}, fmt.Errorf("%s: %w", value.name, err)
			}
			*value.target = uri
		}
	}
	return scaffold.EntitySchemaPreviewRequest{
		Spec: spec, Decisions: input.Decisions,
		DriftDecision: input.DriftDecision,
	}, nil
}

func (runtime *mcpRuntime) applyEntitySchemaResponse(
	response scaffold.EntitySchemaApplyResponse,
) (entitySchemaWriteOutput, error) {
	files, diff, err := runtime.applyMCPWorkspaceEdit(response.Edit, true)
	if err != nil {
		return entitySchemaWriteOutput{}, err
	}
	return entitySchemaWriteOutput{
		Applied: true, PrimaryFile: runtime.outputURI(response.PrimaryFileURI),
		SnapshotID: response.SnapshotID, Files: files, Diff: diff,
	}, nil
}

func (runtime *mcpRuntime) inputURI(value string, required bool) (string, error) {
	if strings.TrimSpace(value) == "" {
		if required {
			return "", errors.New("path is required")
		}
		return "", nil
	}
	path, err := runtime.resolvePath(value)
	if err != nil {
		return "", err
	}
	return uriutil.FileURI(path), nil
}

func compactEntitySchemaBootstrap(
	input scaffold.EntitySchemaBootstrapResponse,
) entitySchemaBootstrapOutput {
	fieldTypes := make([]entitySchemaFieldTypeSummary, 0, len(input.FieldTypes))
	for _, fieldType := range input.FieldTypes {
		fieldTypes = append(fieldTypes, summarizeEntitySchemaFieldType(fieldType))
	}
	return entitySchemaBootstrapOutput{
		Plugin: input.Plugin, Spec: input.Spec,
		DefinitionKinds: append([]entityschema.DefinitionKind(nil), input.DefinitionKinds...),
		FieldTypes:      fieldTypes, Graph: input.Graph,
		Editable:   append([]scaffold.EntitySchemaEditableTarget(nil), input.Editable...),
		NextAction: "Modify this exact spec. A normal scalar field is {id, kind, propertyName, storageName, editable:true}; add translated:true for translated storage. Call shopware_entity_schema_field_types with an exact id for a copyable template, or shopware_entity_schema_search for relation targets. Then preview the complete spec. Do not inspect content.json or temporary payload files.",
	}
}

func summarizeEntitySchemaFieldType(
	fieldType scaffold.EntitySchemaFieldType,
) entitySchemaFieldTypeSummary {
	return entitySchemaFieldTypeSummary{
		ID: entitySchemaFieldTypeID(fieldType), Kind: fieldType.Kind,
		Label: fieldType.Label, Stored: fieldType.Stored,
		Specialized:                   fieldType.Template != nil,
		RequiresDefaultFieldsOverride: fieldType.RequiresDefaultFieldsOverride,
	}
}

func entitySchemaFieldTypeID(fieldType scaffold.EntitySchemaFieldType) string {
	if strings.TrimSpace(fieldType.ID) != "" {
		return fieldType.ID
	}
	return fieldType.Kind
}

func containsDefinitionKind(kinds []entityschema.DefinitionKind, target entityschema.DefinitionKind) bool {
	for _, kind := range kinds {
		if kind == target {
			return true
		}
	}
	return false
}

func entitySchemaFieldTemplate(fieldType scaffold.EntitySchemaFieldType) map[string]any {
	field := entitySchemaBasicFieldTemplate(entityschema.FieldKind(fieldType.Kind))
	if fieldType.Template != nil {
		field = *fieldType.Template
		field.ID = "replace-me"
		field.Editable = true
		field.Raw = ""
	}
	encoded, err := json.Marshal(field)
	if err != nil {
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil
	}
	return result
}

func entitySchemaBasicFieldTemplate(kind entityschema.FieldKind) entityschema.FieldSpec {
	field := entityschema.FieldSpec{
		ID: "replace-me", Kind: kind, PropertyName: "replaceMe",
		StorageName: "replace_me", Editable: true,
	}
	switch kind {
	case entityschema.FieldID:
		field.ID, field.PropertyName, field.StorageName = "id", "id", "id"
		field.Required, field.Primary = true, true
	case entityschema.FieldAutoIncrement:
		field.PropertyName, field.StorageName, field.Required = "autoIncrement", "auto_increment", true
	case entityschema.FieldVersion:
		field.PropertyName, field.StorageName, field.Required, field.Primary = "versionId", "version_id", true, true
	case entityschema.FieldCreatedAt:
		field.PropertyName, field.StorageName, field.Required = "createdAt", "created_at", true
	case entityschema.FieldUpdatedAt:
		field.PropertyName, field.StorageName = "updatedAt", "updated_at"
	case entityschema.FieldHierarchy:
		field.PropertyName, field.StorageName = "children", ""
	case entityschema.FieldReferenceVersion:
		field.PropertyName, field.StorageName = "replaceMeVersionId", "replace_me_version_id"
		field.TargetDefinitionClass = `Vendor\Plugin\Content\ReplaceMe\ReplaceMeDefinition`
	case entityschema.FieldForeignKey:
		field.PropertyName, field.StorageName = "replaceMeId", "replace_me_id"
		field.TargetDefinitionClass = `Vendor\Plugin\Content\ReplaceMe\ReplaceMeDefinition`
		field.ReferenceField, field.ReferenceStorageName = "id", "id"
	case entityschema.FieldManyToOne, entityschema.FieldOneToOne:
		field.ForeignKeyPropertyName = "replaceMeId"
		field.TargetDefinitionClass = `Vendor\Plugin\Content\ReplaceMe\ReplaceMeDefinition`
		field.TargetEntityClass = `Vendor\Plugin\Content\ReplaceMe\ReplaceMeEntity`
		field.TargetCollectionClass = `Vendor\Plugin\Content\ReplaceMe\ReplaceMeCollection`
		field.TargetEntityName = "replace_me"
		field.ReferenceField, field.ReferenceStorageName = "id", "id"
	case entityschema.FieldOneToMany:
		field.PropertyName, field.StorageName = "replaceMes", ""
		field.TargetDefinitionClass = `Vendor\Plugin\Content\ReplaceMe\ReplaceMeDefinition`
		field.TargetEntityClass = `Vendor\Plugin\Content\ReplaceMe\ReplaceMeEntity`
		field.TargetCollectionClass = `Vendor\Plugin\Content\ReplaceMe\ReplaceMeCollection`
		field.TargetEntityName = "replace_me"
		field.ReferenceStorageName, field.SourceColumn = "owner_id", "id"
	case entityschema.FieldManyToMany:
		field.PropertyName, field.StorageName = "replaceMes", ""
		field.TargetDefinitionClass = `Vendor\Plugin\Content\ReplaceMe\ReplaceMeDefinition`
		field.TargetEntityClass = `Vendor\Plugin\Content\ReplaceMe\ReplaceMeEntity`
		field.TargetCollectionClass = `Vendor\Plugin\Content\ReplaceMe\ReplaceMeCollection`
		field.TargetEntityName = "replace_me"
		field.MappingDefinitionClass = `Vendor\Plugin\Content\ReplaceMe\ReplaceMeMappingDefinition`
		field.MappingLocalColumn, field.MappingReferenceColumn = "owner_id", "replace_me_id"
		field.SourceColumn, field.ReferenceField = "id", "id"
	}
	return field
}

func entitySchemaFieldTypeUsage(fieldType scaffold.EntitySchemaFieldType) string {
	if fieldType.Template != nil {
		return "Copy the complete template and retain implementation unchanged. Replace id and only the storage/property values that are not fixed by the returned implementation metadata."
	}
	switch entityschema.FieldKind(fieldType.Kind) {
	case entityschema.FieldManyToOne, entityschema.FieldOneToOne,
		entityschema.FieldOneToMany, entityschema.FieldManyToMany,
		entityschema.FieldForeignKey, entityschema.FieldReferenceVersion:
		return "Call shopware_entity_schema_search, replace every ReplaceMe target value with one returned target, and adjust relation/mapping columns."
	case entityschema.FieldEnum:
		return "Replace the example identifiers and set enumClass, enumCase, and enumBackingType to string or int."
	case entityschema.FieldCreatedAt, entityschema.FieldUpdatedAt:
		return "Normal EntityDefinition classes inherit this field already. Add it only for a mapping or an explicit defaultFields override advertised by the catalog."
	case entityschema.FieldHierarchy:
		return "Use exactly one hierarchy row. The server derives parent FK, associations, and version pairing; do not add those rows separately."
	default:
		return "Replace the example id, propertyName, and storageName. Set translated=true for translation storage; leave createdAt and updatedAt implicit on normal entities."
	}
}

func (runtime *mcpRuntime) normalizeBootstrapOutput(
	output *scaffold.EntitySchemaBootstrapResponse,
) {
	if output == nil {
		return
	}
	output.Plugin.RootURI = runtime.outputURI(output.Plugin.RootURI)
	output.Plugin.SourceRootURI = runtime.outputURI(output.Plugin.SourceRootURI)
	for index := range output.Plugin.ServiceURIs {
		output.Plugin.ServiceURIs[index] = runtime.outputURI(output.Plugin.ServiceURIs[index])
	}
	runtime.normalizeEntitySpecOutput(&output.Spec)
	runtime.normalizeRelationTargets(output.Existing)
	for index := range output.Editable {
		if output.Editable[index].FileURI != "" {
			output.Editable[index].FileURI = runtime.outputURI(output.Editable[index].FileURI)
		}
	}
}

func (runtime *mcpRuntime) normalizeRelationTargets(
	targets []scaffold.EntitySchemaRelationTarget,
) {
	for index := range targets {
		if targets[index].FileURI != "" {
			targets[index].FileURI = runtime.outputURI(targets[index].FileURI)
		}
	}
}

func (runtime *mcpRuntime) normalizeEntitySpecOutput(spec *entityschema.EntitySpec) {
	if spec == nil {
		return
	}
	for _, target := range []*string{
		&spec.PluginRootURI, &spec.DirectoryURI, &spec.DefinitionURI,
		&spec.EntityURI, &spec.CollectionURI, &spec.ServiceURI,
	} {
		if *target != "" {
			*target = runtime.outputURI(*target)
		}
	}
	if spec.Translation != nil {
		for _, target := range []*string{
			&spec.Translation.DefinitionURI,
			&spec.Translation.EntityURI,
			&spec.Translation.CollectionURI,
		} {
			if *target != "" {
				*target = runtime.outputURI(*target)
			}
		}
	}
}

func (runtime *mcpRuntime) entitySchemaPreviewOutput(
	preview scaffold.EntitySchemaPreviewResponse,
) entitySchemaPreviewOutput {
	output := entitySchemaPreviewOutput{
		Revision: preview.Revision, Issues: preview.Issues,
		Diff:                            compactEntitySchemaDiff(preview.Diff),
		Destructive:                     preview.Destructive,
		RequiresDestructiveConfirmation: preview.Destructive,
		Drift:                           preview.Drift, DriftMessage: preview.DriftMessage,
		SnapshotID:         preview.SnapshotID,
		MigrationTimestamp: preview.MigrationTimestamp,
	}
	for _, file := range preview.Files {
		output.Files = append(output.Files, entitySchemaPreviewFile{
			Path: runtime.outputURI(file.URI), Action: file.Action,
			Language: file.Language, SizeBytes: len(file.After),
		})
	}
	if preview.PrimaryFileURI != "" {
		output.PrimaryFile = runtime.outputURI(preview.PrimaryFileURI)
	}
	switch {
	case len(preview.Issues) != 0:
		output.Status = "needs-input"
		output.NextAction = "Resolve every reported issue or rename question, then call shopware_entity_schema_preview again. Do not apply this result."
	case preview.Drift:
		output.Status = "needs-input"
		output.NextAction = "Choose driftDecision=adopt or driftDecision=migrate as described, then call shopware_entity_schema_preview again. Do not apply this result."
	case preview.Revision == "":
		output.Status = "blocked"
		output.NextAction = "The preview did not produce an applicable revision. Resolve the reported state and preview again."
	case preview.Destructive:
		output.Status = "requires-destructive-confirmation"
		output.ReadyToApply = true
		output.NextAction = "Obtain explicit user confirmation, then call shopware_entity_schema_apply once with the unchanged spec, this exact revision, and allowDestructive=true. Do not preview again or inspect temporary payload files."
	default:
		output.Status = "ready"
		output.ReadyToApply = true
		output.NextAction = "Call shopware_entity_schema_apply once with the unchanged spec and this exact revision. Do not preview again or inspect temporary payload files."
	}
	return output
}

func compactEntitySchemaDiff(diff entityschema.SchemaDiff) entitySchemaDiffSummary {
	result := entitySchemaDiffSummary{
		RenameQuestions:       diff.RenameQuestions,
		EntityRenameQuestions: diff.EntityRenameQuestions,
	}
	for _, entity := range diff.CreatedEntities {
		result.CreatedEntities = append(result.CreatedEntities, entity.Name)
	}
	for _, entity := range diff.RemovedEntities {
		result.RemovedEntities = append(result.RemovedEntities, entity.Name)
	}
	for _, change := range diff.AddedColumns {
		if change.After != nil {
			result.AddedColumns = append(result.AddedColumns, change.Entity+"."+change.After.Name)
		}
	}
	for _, change := range diff.RemovedColumns {
		if change.Before != nil {
			result.RemovedColumns = append(result.RemovedColumns, change.Entity+"."+change.Before.Name)
		}
	}
	for _, change := range diff.ChangedColumns {
		name := ""
		if change.After != nil {
			name = change.After.Name
		} else if change.Before != nil {
			name = change.Before.Name
		}
		result.ChangedColumns = append(result.ChangedColumns, change.Entity+"."+name)
	}
	for _, change := range diff.AddedIndexes {
		result.AddedIndexes = append(result.AddedIndexes, change.Entity+"."+change.Index.Name)
	}
	for _, change := range diff.RemovedIndexes {
		result.RemovedIndexes = append(result.RemovedIndexes, change.Entity+"."+change.Index.Name)
	}
	for _, change := range diff.AddedForeignKeys {
		result.AddedForeignKeys = append(result.AddedForeignKeys, change.Entity+"."+change.ForeignKey.Name)
	}
	for _, change := range diff.RemovedForeignKeys {
		result.RemovedForeignKeys = append(result.RemovedForeignKeys, change.Entity+"."+change.ForeignKey.Name)
	}
	for _, change := range diff.ChangedPrimaryKeys {
		result.ChangedPrimaryKeys = append(result.ChangedPrimaryKeys, change.Entity)
	}
	return result
}
