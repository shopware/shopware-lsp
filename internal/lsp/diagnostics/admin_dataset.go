package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin/dataset"
	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	adminDatasetPermissionMissing = "admin.dataset.permission-missing"
	adminDatasetUnscoped          = "admin.dataset.unscoped-subscription"
	adminDatasetUnresolved        = "admin.dataset.unresolved"
)

// AdminDatasetAnalyzer checks Meteor Admin SDK dataset reads in Shopware apps.
// Literal selectors are walked through the indexed DAL schema and compared
// with the app manifest's read and crud privileges.
type AdminDatasetAnalyzer struct {
	datasets   *dataset.Index
	dal        *dal.Index
	extensions *extension.ExtensionIndexer
}

func NewAdminDatasetAnalyzer(
	datasets *dataset.Index,
	definitions *dal.Index,
	extensions *extension.ExtensionIndexer,
) *AdminDatasetAnalyzer {
	return &AdminDatasetAnalyzer{
		datasets:   datasets,
		dal:        definitions,
		extensions: extensions,
	}
}

func (analyzer *AdminDatasetAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if analyzer == nil || analyzer.extensions == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	path, err := uriutil.Path(document.URI)
	if err != nil || !isAppAdministrationSource(path) {
		return nil, err
	}
	app, err := analyzer.extensions.FindAppForFile(path)
	if err != nil || app == nil {
		return nil, err
	}
	calls := dataset.Calls(document.SyntaxTree.Root)
	if len(calls) == 0 {
		return nil, nil
	}
	granted, wildcard := readGrants(app.Permissions)
	manifest := filepath.Join(app.Path, "manifest.xml")
	var definitions []dal.Definition
	loaded := false
	var problems []lsp.Problem
	for _, call := range calls {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		problem, needsSchema, report := classifyDatasetCall(call)
		if report {
			problems = append(problems, problem)
			continue
		}
		if !needsSchema {
			continue
		}
		if !loaded {
			if analyzer.dal != nil {
				definitions, err = analyzer.dal.Definitions()
				if err != nil {
					return nil, err
				}
			}
			loaded = true
		}
		propertyPath := ""
		if analyzer.datasets != nil && call.ID != "" {
			propertyPath, err = analyzer.datasets.PropertyPath(call.ID)
			if err != nil {
				return nil, err
			}
		}
		resolved, ok := resolveDatasetProblem(
			call, definitions, propertyPath, granted, wildcard, manifest,
		)
		if ok {
			problems = append(problems, resolved)
		}
	}
	return problems, nil
}

func classifyDatasetCall(call dataset.Call) (lsp.Problem, bool, bool) {
	if call.ID == "" {
		return unresolvedDatasetProblem(call, "dynamic-id", fmt.Sprintf(
			"data.%s uses a dynamic dataset id and cannot be checked statically",
			call.Method,
		)), false, true
	}
	switch call.SelectorsMode {
	case dataset.SelectorsDynamic:
		return unresolvedDatasetProblem(call, "dynamic-selectors", fmt.Sprintf(
			"data.%s(%q) uses dynamic selectors and cannot be checked statically",
			call.Method, call.ID,
		)), false, true
	case dataset.SelectorsAbsent:
		if call.Method != "subscribe" {
			return lsp.Problem{}, false, false
		}
		return lsp.Problem{
			ID:      adminDatasetUnscoped,
			Range:   call.IDRange,
			Element: call.Node,
			Message: fmt.Sprintf(
				"data.subscribe(%q) selects the whole dataset, so required read privileges change when core or plugins add associations",
				call.ID,
			),
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "shopware-lsp",
			Payload: map[string]any{
				"dataset": call.ID,
				"call":    "data.subscribe",
			},
		}, false, true
	default:
		return lsp.Problem{}, true, false
	}
}

func resolveDatasetProblem(
	call dataset.Call,
	definitions []dal.Definition,
	propertyPath string,
	granted map[string]bool,
	wildcard bool,
	manifest string,
) (lsp.Problem, bool) {
	root, found := dataset.ResolveRoot(definitions, call.ID, propertyPath)
	if !found {
		return unresolvedDatasetProblem(call, "unknown-dataset", fmt.Sprintf(
			"Dataset %q cannot be resolved to a Shopware entity",
			call.ID,
		)), true
	}
	entities, resolved := dataset.RequiredEntities(definitions, root, call.Selectors)
	if len(entities) == 0 {
		if resolved {
			return lsp.Problem{}, false
		}
		return unresolvedDatasetProblem(call, "unresolved-selector", fmt.Sprintf(
			"Selector path cannot be resolved through the entity schema for dataset %q",
			call.ID,
		)), true
	}
	missing := missingReads(entities, granted, wildcard)
	if len(missing) == 0 {
		return lsp.Problem{}, false
	}
	return lsp.Problem{
		ID:      adminDatasetPermissionMissing,
		Range:   call.IDRange,
		Element: call.Node,
		Message: fmt.Sprintf(
			"App manifest is missing %s for dataset %q used by data.%s",
			formatReadPrivileges(missing), call.ID, call.Method,
		),
		Severity: protocol.DiagnosticSeverityError,
		Source:   "shopware-lsp",
		Payload: map[string]any{
			"dataset":  call.ID,
			"entities": missing,
			"manifest": manifest,
			"call":     "data." + call.Method,
		},
	}, true
}

func unresolvedDatasetProblem(call dataset.Call, reason, message string) lsp.Problem {
	return lsp.Problem{
		ID:       adminDatasetUnresolved,
		Range:    call.IDRange,
		Element:  call.Node,
		Message:  message,
		Severity: protocol.DiagnosticSeverityHint,
		Source:   "shopware-lsp",
		Payload: map[string]any{
			"dataset": call.ID,
			"call":    "data." + call.Method,
			"reason":  reason,
		},
	}
}

func readGrants(permissions []extension.AppPermission) (map[string]bool, bool) {
	granted := make(map[string]bool, len(permissions))
	wildcard := false
	for _, permission := range permissions {
		if permission.Operation != "read" && permission.Operation != "crud" {
			continue
		}
		if permission.Entity == "*" {
			wildcard = true
		}
		granted[permission.Entity] = true
	}
	return granted, wildcard
}

func missingReads(required []string, granted map[string]bool, wildcard bool) []string {
	if wildcard {
		return nil
	}
	var missing []string
	for _, entity := range required {
		if !granted[entity] {
			missing = append(missing, entity)
		}
	}
	return missing
}

func formatReadPrivileges(entities []string) string {
	parts := make([]string, len(entities))
	for index, entity := range entities {
		parts[index] = "read:" + entity
	}
	return strings.Join(parts, ", ")
}

func isAppAdministrationSource(path string) bool {
	normalized := strings.ToLower(filepath.ToSlash(path))
	return strings.Contains(normalized, "/resources/app/administration/")
}
