package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/lsp/scaffold"
	"github.com/shopware/shopware-lsp/internal/shopware/entityschema"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type mcpScaffoldKind struct {
	Family      string   `json:"family"`
	Kind        string   `json:"kind"`
	Description string   `json:"description"`
	Options     []string `json:"options,omitempty"`
}

type scaffoldCatalogInput struct{}

type scaffoldCatalogOutput struct {
	Scaffolds    []mcpScaffoldKind `json:"scaffolds"`
	EntitySchema bool              `json:"entitySchema"`
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
	Directory string `json:"directory" jsonschema:"workspace-relative or absolute directory inside the target Shopware plugin; call this first for every create or edit entity request"`
}

type entitySchemaSearchInput struct {
	Query string `json:"query,omitempty" jsonschema:"entity name or definition class query used to resolve relation targets after bootstrap"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum relation targets; defaults to 50 and cannot exceed 200"`
}

type entitySchemaSearchOutput struct {
	Results []scaffold.EntitySchemaRelationTarget `json:"results"`
}

type entitySchemaLoadInput struct {
	DefinitionClass string `json:"definitionClass,omitempty" jsonschema:"existing indexed definition class to edit after bootstrapping its plugin"`
	EntityName      string `json:"entityName,omitempty" jsonschema:"existing indexed technical entity name to edit"`
	Path            string `json:"path,omitempty" jsonschema:"workspace-relative or absolute existing entity definition file to edit"`
}

type entitySchemaPreviewInput struct {
	Spec          entityschema.EntitySpec `json:"spec" jsonschema:"typed entity specification returned by bootstrap or load, modified for the requested fields and indexes"`
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
	CreatedEntities    []string                      `json:"createdEntities,omitempty"`
	RemovedEntities    []string                      `json:"removedEntities,omitempty"`
	AddedColumns       []string                      `json:"addedColumns,omitempty"`
	RemovedColumns     []string                      `json:"removedColumns,omitempty"`
	ChangedColumns     []string                      `json:"changedColumns,omitempty"`
	RenameQuestions    []entityschema.RenameQuestion `json:"renameQuestions,omitempty"`
	AddedIndexes       []string                      `json:"addedIndexes,omitempty"`
	RemovedIndexes     []string                      `json:"removedIndexes,omitempty"`
	AddedForeignKeys   []string                      `json:"addedForeignKeys,omitempty"`
	RemovedForeignKeys []string                      `json:"removedForeignKeys,omitempty"`
	ChangedPrimaryKeys []string                      `json:"changedPrimaryKeys,omitempty"`
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
		Description: "List the Shopware and Symfony artifacts supported by the production scaffold generators.",
		Annotations: readOnly,
	}, runtime.scaffoldCatalog)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_scaffold", Title: "Create Shopware plugin or scaffold",
		Description: "Always use this production generator when the user asks to create, generate, scaffold, or initialize a Shopware plugin or another supported Shopware/Symfony artifact. For a plugin use family=shopware, kind=plugin, and its parent directory (usually custom/plugins). Set write=true when file creation was requested; otherwise it returns a non-writing unified diff.",
		Annotations: write,
	}, runtime.scaffold)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_entity_schema_bootstrap", Title: "Start creating or editing a Shopware DAL entity",
		Description: "Always call this first when the user asks to create, generate, or edit a Shopware DAL entity or entity definition. It returns the typed spec to modify, plugin paths, field types, snapshot state, and initial relation targets. Do not create entity PHP files manually.",
		Annotations: readOnly,
	}, runtime.entitySchemaBootstrap)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_entity_schema_search", Title: "Find Shopware DAL relation targets",
		Description: "Use after entity-schema bootstrap when a requested association needs an indexed target definition, entity name, fields, or collection class. Feed the selected target into the typed spec before previewing.",
		Annotations: readOnly,
	}, runtime.entitySchemaSearch)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_entity_schema_load", Title: "Load an existing Shopware DAL entity for editing",
		Description: "Use after bootstrapping the plugin when the user asks to edit an existing DAL entity. Import the indexed definition into the typed spec, modify that spec, and send it to entity-schema preview.",
		Annotations: readOnly,
	}, runtime.entitySchemaLoad)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_entity_schema_preview", Title: "Validate and preview a Shopware DAL entity",
		Description: "Required after bootstrap/load and before apply. Validate the modified typed spec and return a compact, self-contained summary of all PHP, services.yaml, migration, and committed snapshot changes without writing. Generated file bodies are intentionally omitted to keep the MCP result inline; do not search temporary VS Code payloads. Resolve every returned issue or rename/drift question and preview again. When status=ready, call apply once with the same spec and exact opaque revision without rerunning preview.",
		Annotations: readOnly,
	}, runtime.entitySchemaPreview)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_entity_schema_apply", Title: "Create or update the Shopware DAL entity",
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
		Scaffolds:    mcpScaffoldKinds(),
		EntitySchema: true,
	}, nil
}

func mcpScaffoldKinds() []mcpScaffoldKind {
	return []mcpScaffoldKind{
		{"shopware", "plugin", "Shopware plugin package, class, and YAML service configuration", []string{"namespace", "description", "author", "license", "package"}},
		{"shopware", "system-config", "Shopware system configuration XML", nil},
		{"shopware", "scheduled-task", "Scheduled task and handler classes", []string{"namespace", "interval", "taskName"}},
		{"shopware", "migration", "Timestamped Shopware migration", []string{"namespace", "timestamp"}},
		{"shopware", "event-listener", "Attribute-based Shopware event listener", []string{"namespace", "event"}},
		{"shopware", "admin-component", "Administration component, Twig, and SCSS files", []string{"mode", "target", "generateTwig", "generateScss", "method", "methodGroup", "parameters"}},
		{"shopware", "admin-module", "Administration module and snippets", nil},
		{"shopware", "cms-block", "Administration CMS block and preview", []string{"category"}},
		{"shopware", "cms-element", "Administration CMS element", nil},
		{"shopware", "app", "Shopware app manifest", []string{"label", "author", "license"}},
		{"shopware", "app-custom-entities", "Shopware app custom entities XML", nil},
		{"shopware", "app-cms", "Shopware app CMS XML", nil},
		{"shopware", "app-script", "Shopware app script hook", []string{"hook"}},
		{"symfony", "command", "Symfony Console command", nil},
		{"symfony", "controller", "Symfony controller with route", nil},
		{"symfony", "form", "Symfony form type", nil},
		{"symfony", "twig-extension", "Twig functions and filters", nil},
		{"symfony", "compiler-pass", "Dependency-injection compiler pass", nil},
		{"symfony", "kernel-test", "KernelTestCase integration test", nil},
		{"symfony", "web-test", "WebTestCase functional test", nil},
		{"symfony", "services-yaml", "YAML service configuration", nil},
		{"symfony", "services-xml", "XML service configuration", nil},
		{"symfony", "services-php", "PHP service configuration", nil},
	}
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
) (*mcp.CallToolResult, scaffold.EntitySchemaBootstrapResponse, error) {
	directoryURI, err := runtime.inputURI(input.Directory, true)
	if err != nil {
		return nil, scaffold.EntitySchemaBootstrapResponse{}, err
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
	return nil, output, err
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
	result := entitySchemaDiffSummary{RenameQuestions: diff.RenameQuestions}
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
