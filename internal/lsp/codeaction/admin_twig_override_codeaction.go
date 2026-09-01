package codeaction

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	adminTwigOverrideAction  = "shopware.admin.overrideTwigBlock"
	adminTwigOverrideCommand = "shopware/admin/twig/override"
)

// AdminTwigOverrideProvider keeps the Administration override workflow
// separate from storefront template inheritance. The editor owns plugin
// selection while component resolution and all filesystem validation remain
// server-side.
type AdminTwigOverrideProvider struct {
	adminIndex     *admin.AdminComponentIndexer
	extensionIndex *extension.ExtensionIndexer
}

func NewAdminTwigOverrideProvider(
	adminIndex *admin.AdminComponentIndexer,
	extensionIndex *extension.ExtensionIndexer,
) *AdminTwigOverrideProvider {
	return &AdminTwigOverrideProvider{
		adminIndex:     adminIndex,
		extensionIndex: extensionIndex,
	}
}

func (*AdminTwigOverrideProvider) GetCodeActionKinds() []protocol.CodeActionKind {
	return []protocol.CodeActionKind{protocol.CodeActionRefactorExtract}
}

func (p *AdminTwigOverrideProvider) GetCodeActions(
	ctx context.Context,
	request *lsp.CodeActionRequest,
) []protocol.CodeAction {
	if ctx.Err() != nil || p == nil || p.adminIndex == nil || request == nil ||
		request.CodeActionParams == nil || request.Node == nil ||
		request.Token == nil || request.Document == nil {
		return nil
	}
	documentPath, err := uriutil.Path(request.TextDocument.URI)
	if err != nil || !isAdministrationTwigPath(documentPath) {
		return nil
	}
	blockNode := twigquery.BlockAt(request.Node)
	blockName := twigquery.BlockName(blockNode)
	if blockNode == nil || blockName == "" || request.Token.Text() != blockName {
		return nil
	}
	component, err := p.adminIndex.GetComponentRegistrationByTemplatePath(
		documentPath,
	)
	if err != nil || component == nil || component.Name == "" {
		return nil
	}
	return []protocol.CodeAction{{
		Title: fmt.Sprintf(
			"Override %s for Administration component %s in a plugin",
			blockName,
			component.Name,
		),
		Kind: protocol.CodeActionRefactorExtract,
		Command: &protocol.CommandAction{
			Title:     "Override Administration Twig block",
			Command:   adminTwigOverrideAction,
			Arguments: []any{request.TextDocument.URI, blockName},
		},
	}}
}

func (p *AdminTwigOverrideProvider) GetCommands(
	_ context.Context,
) map[string]lsp.CommandFunc {
	return map[string]lsp.CommandFunc{
		adminTwigOverrideCommand: p.generateAdminTwigOverride,
	}
}

type adminTwigOverrideRequest struct {
	TextURI   string `json:"textUri"`
	BlockName string `json:"blockName"`
	Extension string `json:"extension"`
}

type adminTwigOverrideResponse struct {
	URI       string `json:"uri"`
	Line      int    `json:"line"`
	Component string `json:"component"`
	ScriptURI string `json:"scriptUri"`
}

func (p *AdminTwigOverrideProvider) generateAdminTwigOverride(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	if p == nil || p.adminIndex == nil || p.extensionIndex == nil {
		return protocol.NewLspError(
			"Administration override generator is not available",
			"admin.override.unavailable",
		), nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var params adminTwigOverrideRequest
	if raw == nil || json.Unmarshal(*raw, &params) != nil {
		return protocol.NewLspError(
			"Invalid Administration override request",
			"admin.override.invalid_request",
		), nil
	}
	params.TextURI = strings.TrimSpace(params.TextURI)
	params.BlockName = strings.TrimSpace(params.BlockName)
	params.Extension = strings.TrimSpace(params.Extension)
	if params.TextURI == "" || !safeGeneratedName(params.BlockName, false) ||
		params.Extension == "" {
		return protocol.NewLspError(
			"The template, block, and target plugin are required",
			"admin.override.invalid_request",
		), nil
	}

	sourcePath, err := uriutil.Path(params.TextURI)
	if err != nil || !isAdministrationTwigPath(sourcePath) {
		return protocol.NewLspError(
			"The selected block is not in an Administration Twig template",
			"admin.override.not_administration_template",
		), nil
	}
	component, err := p.adminIndex.GetComponentRegistrationByTemplatePath(
		sourcePath,
	)
	if err != nil {
		return nil, fmt.Errorf("resolve Administration component: %w", err)
	}
	if component == nil || !safeGeneratedName(component.Name, true) {
		return protocol.NewLspError(
			"No Administration component owns this Twig template",
			"admin.override.component_not_found",
		), nil
	}

	target, found, err := p.extensionIndex.FindByName(params.Extension)
	if err != nil {
		return nil, fmt.Errorf("find target plugin: %w", err)
	}
	if !found {
		return protocol.NewLspError(
			"Target plugin not found",
			"extension.not_found",
		), nil
	}
	if target.Type != extension.ShopwareExtensionTypeBundle {
		return protocol.NewLspError(
			"Administration component overrides can only be generated in plugins",
			"admin.override.plugin_required",
		), nil
	}

	result, generationErr := generateAdminOverrideFiles(
		target,
		component.Name,
		params.BlockName,
	)
	if generationErr != nil {
		return protocol.NewLspError(
			generationErr.Error(),
			"admin.override.file_conflict",
		), nil
	}
	return result, nil
}

func generateAdminOverrideFiles(
	target extension.ShopwareExtension,
	componentName,
	blockName string,
) (*adminTwigOverrideResponse, error) {
	administrationSource := target.GetAdministrationSourcePath()
	overrideDirectory := filepath.Join(
		administrationSource,
		"extension",
		componentName,
	)
	templateName := componentName + ".html.twig"
	templatePath := filepath.Join(overrideDirectory, templateName)
	scriptPath := filepath.Join(overrideDirectory, "index.js")

	scriptContent, scriptExists, err := readOptionalFile(scriptPath)
	if err != nil {
		return nil, fmt.Errorf("read component override: %w", err)
	}
	if scriptExists {
		if err := validateAdminOverrideScript(
			scriptContent,
			componentName,
			"./"+templateName,
		); err != nil {
			return nil, fmt.Errorf("existing %s: %w", scriptPath, err)
		}
	} else {
		scriptContent = []byte(fmt.Sprintf(
			"import template from './%s';\n\n"+
				"Shopware.Component.override('%s', {\n"+
				"    template,\n"+
				"});\n",
			templateName,
			componentName,
		))
	}

	templateContent, templateExists, err := readOptionalFile(templatePath)
	if err != nil {
		return nil, fmt.Errorf("read Twig override: %w", err)
	}
	templateChanged := !templateExists
	line := 0
	if templateExists {
		parsed, parseErr := twig.ParseTwig(templatePath, templateContent)
		if parseErr != nil {
			return nil, fmt.Errorf("parse existing %s: %w", templatePath, parseErr)
		}
		if block, exists := parsed.Blocks[blockName]; exists {
			line = max(block.Line-1, 0)
		} else {
			templateContent = appendTwigOverrideBlock(templateContent, blockName)
			templateChanged = true
			line = twigBlockStartLine(templateContent, blockName)
		}
	} else {
		templateContent = []byte(fmt.Sprintf(
			"{%% block %s %%}\n\n{%% endblock %%}\n",
			blockName,
		))
	}

	entryPath, entryContent, entryExists, err := administrationEntry(
		administrationSource,
	)
	if err != nil {
		return nil, err
	}
	entryImport := "./extension/" + componentName
	if !hasJSImport(entryContent, entryImport) {
		entryContent = prependJSImport(entryContent, entryImport)
		entryExists = false
	}

	if err := os.MkdirAll(overrideDirectory, 0o755); err != nil {
		return nil, fmt.Errorf("create override directory: %w", err)
	}
	if !scriptExists {
		if err := os.WriteFile(scriptPath, scriptContent, 0o644); err != nil {
			return nil, fmt.Errorf("write component override: %w", err)
		}
	}
	if templateChanged {
		if err := os.WriteFile(templatePath, templateContent, 0o644); err != nil {
			return nil, fmt.Errorf("write Twig override: %w", err)
		}
	}
	if !entryExists {
		if err := os.WriteFile(entryPath, entryContent, 0o644); err != nil {
			return nil, fmt.Errorf("write Administration entry point: %w", err)
		}
	}

	return &adminTwigOverrideResponse{
		URI:       uriutil.FileURI(templatePath),
		Line:      line,
		Component: componentName,
		ScriptURI: uriutil.FileURI(scriptPath),
	}, nil
}

func isAdministrationTwigPath(filePath string) bool {
	normalized := filepath.ToSlash(filepath.Clean(filePath))
	return strings.HasSuffix(normalized, ".twig") && strings.Contains(
		normalized,
		"/Resources/app/administration/src/",
	)
}

func safeGeneratedName(value string, allowHyphen bool) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '_' ||
			allowHyphen && character == '-' {
			continue
		}
		return false
	}
	return true
}

func readOptionalFile(filePath string) ([]byte, bool, error) {
	content, err := os.ReadFile(filePath)
	if err == nil {
		return content, true, nil
	}
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	return nil, false, err
}

func validateAdminOverrideScript(
	content []byte,
	componentName,
	templateImport string,
) error {
	root := javascriptparser.Parse(string(content)).Tree.Root
	if jsquery.ImportPath(root, "template") != templateImport {
		return fmt.Errorf("template import must point to %q", templateImport)
	}
	for call := range jsquery.IterateCalls(
		root,
		"Shopware.Component.override",
		"Component.override",
	) {
		if jsquery.StringValue(jsquery.StringArgument(call, 0)) != componentName {
			continue
		}
		definition := jsquery.ObjectArgument(call, 1)
		if definition == nil || jsquery.Property(definition, "template") == nil {
			return fmt.Errorf("override for %q does not use template", componentName)
		}
		return nil
	}
	return fmt.Errorf("does not override component %q", componentName)
}

func appendTwigOverrideBlock(content []byte, blockName string) []byte {
	result := append([]byte(nil), content...)
	if len(result) != 0 && result[len(result)-1] != '\n' {
		result = append(result, '\n')
	}
	if len(result) != 0 {
		result = append(result, '\n')
	}
	return append(result, []byte(fmt.Sprintf(
		"{%% block %s %%}\n\n{%% endblock %%}\n",
		blockName,
	))...)
}

func twigBlockStartLine(content []byte, blockName string) int {
	parsed, err := twig.ParseTwig("generated.html.twig", content)
	if err != nil {
		return 0
	}
	block, exists := parsed.Blocks[blockName]
	if !exists {
		return 0
	}
	return max(block.Line-1, 0)
}

func administrationEntry(
	administrationSource string,
) (string, []byte, bool, error) {
	for _, name := range []string{"main.js", "main.ts"} {
		entryPath := filepath.Join(administrationSource, name)
		content, exists, err := readOptionalFile(entryPath)
		if err != nil {
			return "", nil, false, fmt.Errorf(
				"read Administration entry point: %w",
				err,
			)
		}
		if exists {
			return entryPath, content, true, nil
		}
	}
	return filepath.Join(administrationSource, "main.js"), nil, false, nil
}

func hasJSImport(content []byte, importPath string) bool {
	root := javascriptparser.Parse(string(content)).Tree.Root
	for _, statement := range jsquery.Nodes(root, jssyntax.JsImportStatement) {
		current := path.Clean(jsquery.ImportPath(statement, ""))
		if current == path.Clean(importPath) ||
			current == path.Clean(importPath+"/index") {
			return true
		}
	}
	return false
}

func prependJSImport(content []byte, importPath string) []byte {
	statement := []byte(fmt.Sprintf("import '%s';\n", importPath))
	return append(statement, content...)
}
