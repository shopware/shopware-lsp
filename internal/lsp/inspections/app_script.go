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
	if _, err := fixContext.Anchor.Resolve(
		fixContext.Document.URI,
		fixContext.Document.Version,
		fixContext.Document.SyntaxLanguage,
		fixContext.Document.SyntaxTree,
	); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	manifestURI := uriutil.FileURI(payload.Manifest)
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
	if permissions != nil {
		for _, read := range xmlquery.ChildElements(permissions, "read") {
			if strings.TrimSpace(xmlquery.TextContent(read)) == payload.Entity {
				return rewrite.WorkspacePlan{}, fmt.Errorf(
					"read permission for %q already exists",
					payload.Entity,
				)
			}
		}
	}

	var offset uint32
	var insertion string
	entity := html.EscapeString(payload.Entity)
	if permissions != nil {
		offset, insertion, err = xmlChildInsertion(
			target.Document.Source,
			permissions.RangeTrimmedTrivia().Start,
			permissions.RangeTrimmedTrivia().End,
			"permissions",
			"<read>"+entity+"</read>",
		)
	} else {
		offset, insertion, err = xmlChildInsertion(
			target.Document.Source,
			manifest.RangeTrimmedTrivia().Start,
			manifest.RangeTrimmedTrivia().End,
			"manifest",
			"<permissions>\n        <read>"+entity+"</read>\n    </permissions>",
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

func xmlChildInsertion(
	source string,
	start,
	end uint32,
	parent,
	child string,
) (uint32, string, error) {
	if start > end || end > uint32(len(source)) {
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
	if strings.TrimSpace(indent) == "" {
		return lineStart, indent + "    " + child + "\n", nil
	}
	return closing, "\n    " + child + "\n" + indent, nil
}
