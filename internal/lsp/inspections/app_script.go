package inspections

import (
	"context"
	"fmt"
	"html"
	"strings"

	"github.com/shopware/shopware-lsp/internal/appscript"
	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	"github.com/shopware/shopware-lsp/internal/rewrite"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const addAppReadPermissionFixID lsp.FixID = "add-app-read-permission"

type appPermissionPayload struct {
	Entity   string `json:"entity"`
	Manifest string `json:"manifest"`
}

func NewAppScript(
	index *appscript.Index,
	extensions *extension.ExtensionIndexer,
) lsp.Inspection {
	return &boundInspection{
		definition: lsp.InspectionDefinition{
			ID:        "shopware.app_script",
			Languages: []language.ID{language.Twig},
			Problems: []lsp.ProblemDefinition{
				{ID: "app_script.permission-missing", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityError},
				{ID: "app_script.service-unavailable", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityError},
			},
		},
		analyzer: diagnostics.NewAppScriptAnalyzer(index, extensions),
		fixes:    []lsp.QuickFix{appReadPermissionFix{}},
		bind: func(code lsp.DiagnosticID, payload map[string]any) []lsp.BoundFix {
			if code != "app_script.permission-missing" {
				return nil
			}
			value := appPermissionPayload{
				Entity:   mapString(payload, "entity"),
				Manifest: mapString(payload, "manifest"),
			}
			if value.Entity == "" || value.Manifest == "" {
				return nil
			}
			return []lsp.BoundFix{lsp.BindFix(addAppReadPermissionFixID, value)}
		},
	}
}

type appReadPermissionFix struct{}

func (appReadPermissionFix) ID() lsp.FixID { return addAppReadPermissionFixID }

func (appReadPermissionFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[appPermissionPayload](fixContext)
	return lsp.FixPresentation{
		Title:      "Add read permission for '" + payload.Entity + "'",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixLazy,
	}, payload.Entity != "" && payload.Manifest != "", err
}

func (appReadPermissionFix) Build(
	ctx context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[appPermissionPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if payload.Entity == "" || payload.Manifest == "" {
		return rewrite.WorkspacePlan{}, fmt.Errorf("app read permission is incomplete")
	}
	return planAppReadPermissions(ctx, fixContext, payload.Manifest, []string{payload.Entity})
}

func planAppReadPermissions(
	ctx context.Context,
	fixContext lsp.FixContext,
	manifestPath string,
	entities []string,
) (rewrite.WorkspacePlan, error) {
	if _, err := fixContext.Anchor.Resolve(
		fixContext.Document.URI,
		fixContext.Document.Version,
		fixContext.Document.SyntaxLanguage,
		fixContext.Document.SyntaxTree,
	); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	entities = uniqueEntities(entities)
	if len(entities) == 0 || manifestPath == "" {
		return rewrite.WorkspacePlan{}, fmt.Errorf("app read permission is incomplete")
	}
	manifestURI := uriutil.FileURI(manifestPath)
	target, err := fixContext.Documents.ResolveDocument(ctx, manifestURI)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if target.Document == nil || target.Document.SyntaxLanguage != language.XML ||
		target.Document.SyntaxTree == nil || target.Document.SyntaxTree.Root == nil {
		return rewrite.WorkspacePlan{}, fmt.Errorf("app manifest has no XML syntax tree")
	}
	manifests := xmlquery.Elements(target.Document.SyntaxTree.Root, "manifest")
	if len(manifests) == 0 {
		return rewrite.WorkspacePlan{}, fmt.Errorf("app manifest root is missing")
	}
	manifest := manifests[0]
	permissions := xmlquery.ChildElement(manifest, "permissions")
	missing := entities
	if permissions != nil {
		missing = nil
		for _, entity := range entities {
			if !manifestGrantsRead(permissions, entity) {
				missing = append(missing, entity)
			}
		}
	}
	if len(missing) == 0 {
		return rewrite.WorkspacePlan{}, fmt.Errorf(
			"read permission for %q already exists",
			entities[0],
		)
	}

	var offset uint32
	var insertion string
	if permissions != nil {
		children := make([]string, len(missing))
		for index, entity := range missing {
			children[index] = "<read>" + html.EscapeString(entity) + "</read>"
		}
		offset, insertion, err = xmlChildInsertion(
			target.Document.Source,
			permissions.RangeTrimmedTrivia().Start,
			permissions.RangeTrimmedTrivia().End,
			"permissions",
			children...,
		)
	} else {
		offset, insertion, err = xmlChildInsertion(
			target.Document.Source,
			manifest.RangeTrimmedTrivia().Start,
			manifest.RangeTrimmedTrivia().End,
			"manifest",
			"<permissions>\n        "+readPermissionLines(missing)+"\n    </permissions>",
		)
	}
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	builder := rewrite.NewBuilder(target.Document.Source)
	if err := builder.Insert(offset, insertion); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	edits, err := builder.Finish()
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	return rewrite.WorkspacePlan{Documents: []rewrite.DocumentPlan{
		rewrite.NewDocumentPlan(manifestURI, target.Version, target.Document.Source, edits),
	}}, nil
}

func manifestGrantsRead(permissions *xmlsyntax.Node, entity string) bool {
	for _, child := range xmlquery.ChildElements(permissions) {
		switch xmlquery.ElementName(child) {
		case "read", "crud":
			value := strings.TrimSpace(xmlquery.TextContent(child))
			if value == entity || value == "*" {
				return true
			}
		}
	}
	return false
}

func readPermissionLines(entities []string) string {
	lines := make([]string, len(entities))
	for index, entity := range entities {
		lines[index] = "<read>" + html.EscapeString(entity) + "</read>"
	}
	return strings.Join(lines, "\n        ")
}

func uniqueEntities(entities []string) []string {
	seen := make(map[string]struct{}, len(entities))
	result := make([]string, 0, len(entities))
	for _, entity := range entities {
		entity = strings.TrimSpace(entity)
		if entity == "" {
			continue
		}
		if _, exists := seen[entity]; exists {
			continue
		}
		seen[entity] = struct{}{}
		result = append(result, entity)
	}
	return result
}

func xmlChildInsertion(
	source string,
	start,
	end uint32,
	parent string,
	children ...string,
) (uint32, string, error) {
	if start > end || end > uint32(len(source)) || len(children) == 0 {
		return 0, "", fmt.Errorf("%s element range changed", parent)
	}
	fragment := source[start:end]
	relative := strings.LastIndex(fragment, "</"+parent)
	if relative < 0 {
		return 0, "", fmt.Errorf("%s closing tag is missing", parent)
	}
	closing := start + uint32(relative)
	lineStart := closing
	for lineStart > 0 && source[lineStart-1] != '\n' {
		lineStart--
	}
	indent := source[lineStart:closing]
	var builder strings.Builder
	if strings.TrimSpace(indent) == "" {
		for _, child := range children {
			builder.WriteString(indent)
			builder.WriteString("    ")
			builder.WriteString(child)
			builder.WriteByte('\n')
		}
		return lineStart, builder.String(), nil
	}
	for _, child := range children {
		builder.WriteString("\n    ")
		builder.WriteString(child)
	}
	builder.WriteByte('\n')
	builder.WriteString(indent)
	return closing, builder.String(), nil
}
