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
	Family    string         `json:"family" jsonschema:"scaffold family: shopware or symfony"`
	Kind      string         `json:"kind" jsonschema:"scaffold kind returned by shopware_scaffold_catalog"`
	Directory string         `json:"directory" jsonschema:"workspace-relative or absolute target directory"`
	Name      string         `json:"name" jsonschema:"artifact name"`
	Options   map[string]any `json:"options,omitempty" jsonschema:"kind-specific Shopware scaffold options"`
	Write     bool           `json:"write,omitempty" jsonschema:"write generated files; false returns a preview diff only"`
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
	Directory string `json:"directory" jsonschema:"workspace-relative or absolute directory inside a Shopware plugin"`
}

type entitySchemaSearchInput struct {
	Query string `json:"query,omitempty" jsonschema:"optional entity name or class query"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum results; defaults to 50 and cannot exceed 200"`
}

type entitySchemaSearchOutput struct {
	Results []scaffold.EntitySchemaRelationTarget `json:"results"`
}

type entitySchemaLoadInput struct {
	DefinitionClass string `json:"definitionClass,omitempty"`
	EntityName      string `json:"entityName,omitempty"`
	Path            string `json:"path,omitempty" jsonschema:"workspace-relative or absolute entity definition file"`
}

type entitySchemaPreviewInput struct {
	Spec          entityschema.EntitySpec `json:"spec"`
	Decisions     []entityschema.Decision `json:"decisions,omitempty"`
	DriftDecision string                  `json:"driftDecision,omitempty"`
}

type entitySchemaApplyInput struct {
	entitySchemaPreviewInput
	Revision         string `json:"revision" jsonschema:"exact revision returned by shopware_entity_schema_preview"`
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
		Name: "shopware_scaffold", Title: "Create Shopware scaffold",
		Description: "Preview or write a validated Shopware or Symfony scaffold. The default is a non-writing unified diff; set write=true to create the files.",
		Annotations: write,
	}, runtime.scaffold)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_entity_schema_bootstrap", Title: "Bootstrap entity schema",
		Description: "Load plugin context, defaults, snapshot history, field types, and relation targets for the DAL entity schema workflow.",
		Annotations: readOnly,
	}, runtime.entitySchemaBootstrap)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_entity_schema_search", Title: "Search entity schemas",
		Description: "Search indexed DAL entity definitions for relation targets.",
		Annotations: readOnly,
	}, runtime.entitySchemaSearch)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_entity_schema_load", Title: "Load entity schema",
		Description: "Import an existing indexed DAL entity definition into the typed entity schema model.",
		Annotations: readOnly,
	}, runtime.entitySchemaLoad)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_entity_schema_preview", Title: "Preview entity schema",
		Description: "Validate a typed DAL entity specification and preview PHP, service, migration, and committed snapshot changes without writing files.",
		Annotations: readOnly,
	}, runtime.entitySchemaPreview)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_entity_schema_apply", Title: "Apply entity schema",
		Description: "Apply the exact revision returned by the entity schema preview. Destructive changes require allowDestructive=true.",
		Annotations: destructiveWrite,
	}, runtime.entitySchemaApply)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_entity_schema_reconcile", Title: "Reconcile entity schema history",
		Description: "Create a merge snapshot for divergent committed entity-schema history.",
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
		{"shopware", "plugin", "Minimal Shopware plugin package and class", []string{"namespace", "description", "author", "license", "package"}},
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
) (*mcp.CallToolResult, scaffold.EntitySchemaPreviewResponse, error) {
	request, err := runtime.entitySchemaPreviewRequest(input)
	if err != nil {
		return nil, scaffold.EntitySchemaPreviewResponse{}, err
	}
	output, err := withMCPSession(ctx, runtime, func(session *cliSession) (scaffold.EntitySchemaPreviewResponse, error) {
		return callMCPCommand[scaffold.EntitySchemaPreviewRequest, scaffold.EntitySchemaPreviewResponse](
			ctx, session, scaffold.EntitySchemaPreviewCommand, request,
		)
	})
	if err == nil {
		runtime.normalizeEntityPreviewOutput(&output)
	}
	return nil, output, err
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

func (runtime *mcpRuntime) normalizeEntityPreviewOutput(
	output *scaffold.EntitySchemaPreviewResponse,
) {
	if output == nil {
		return
	}
	for index := range output.Files {
		output.Files[index].URI = runtime.outputURI(output.Files[index].URI)
	}
	if output.PrimaryFileURI != "" {
		output.PrimaryFileURI = runtime.outputURI(output.PrimaryFileURI)
	}
}
