package diagnostics

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/shopware/shopware-lsp/internal/appscript"
	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

var (
	appScriptServicePattern    = regexp.MustCompile(`\bservices\s*\.\s*([A-Za-z_][A-Za-z0-9_]*)`)
	appScriptRepositoryPattern = regexp.MustCompile(`\bservices\s*\.\s*repository\s*\.\s*(?:search|searchIds|aggregate)\s*\(\s*['"]([^'"]+)['"]`)
)

type AppScriptAnalyzer struct {
	index      *appscript.Index
	extensions *extension.ExtensionIndexer
}

func NewAppScriptAnalyzer(
	index *appscript.Index,
	extensions *extension.ExtensionIndexer,
) *AppScriptAnalyzer {
	return &AppScriptAnalyzer{index: index, extensions: extensions}
}

func (a *AppScriptAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if a == nil || a.index == nil || document == nil ||
		!strings.HasSuffix(strings.ToLower(document.URI), ".twig") ||
		!strings.Contains(filepath.ToSlash(document.URI), "/Resources/scripts/") {
		return nil, nil
	}
	path, err := uriutil.Path(document.URI)
	if err != nil {
		return nil, err
	}
	hookName := scriptHookName(path)
	available, hookFound, err := a.index.ServicesForHook(hookName)
	if err != nil {
		return nil, err
	}
	var problems []lsp.Problem
	if hookFound {
		for _, match := range appScriptServicePattern.FindAllStringSubmatchIndex(document.Source, -1) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if len(match) < 4 || appScriptMatchInComment(document, match[2]) {
				continue
			}
			service := document.Source[match[2]:match[3]]
			if _, exists := available[service]; exists {
				continue
			}
			problems = append(problems, lsp.Problem{
				ID:       "app_script.service-unavailable",
				Range:    cst.TextRange{Start: uint32(match[2]), End: uint32(match[3])},
				Message:  "Service '" + service + "' is not available in hook '" + hookName + "'",
				Severity: protocol.DiagnosticSeverityError,
				Source:   "shopware-lsp",
				Payload:  map[string]any{"hook": hookName, "service": service},
			})
		}
	}
	if a.extensions == nil {
		return problems, nil
	}
	app, err := a.extensions.FindAppForFile(path)
	if err != nil || app == nil {
		return problems, err
	}
	readPermissions := make(map[string]struct{})
	for _, permission := range app.Permissions {
		if permission.Operation == "read" {
			readPermissions[permission.Entity] = struct{}{}
		}
	}
	for _, match := range appScriptRepositoryPattern.FindAllStringSubmatchIndex(document.Source, -1) {
		if len(match) < 4 || appScriptMatchInComment(document, match[2]) {
			continue
		}
		entity := document.Source[match[2]:match[3]]
		if _, exists := readPermissions[entity]; exists {
			continue
		}
		problems = append(problems, lsp.Problem{
			ID:       "app_script.permission-missing",
			Range:    cst.TextRange{Start: uint32(match[2]), End: uint32(match[3])},
			Message:  "App manifest needs read permission for entity '" + entity + "'",
			Severity: protocol.DiagnosticSeverityError,
			Source:   "shopware-lsp",
			Payload: map[string]any{
				"entity":   entity,
				"manifest": filepath.Join(app.Path, "manifest.xml"),
			},
		})
	}
	return problems, nil
}

func scriptHookName(path string) string {
	path = filepath.ToSlash(path)
	const marker = "/Resources/scripts/"
	index := strings.Index(path, marker)
	if index < 0 {
		return ""
	}
	relative := path[index+len(marker):]
	if separator := strings.IndexByte(relative, '/'); separator >= 0 {
		return relative[:separator]
	}
	return strings.TrimSuffix(relative, filepath.Ext(relative))
}

func appScriptMatchInComment(document *lsp.TextDocument, offset int) bool {
	if document == nil || document.SyntaxTree == nil || document.SyntaxTree.Root == nil || offset < 0 {
		return false
	}
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == twigsyntax.TwigComment || current.Kind() == twigsyntax.HtmlComment {
			return true
		}
	}
	return false
}
