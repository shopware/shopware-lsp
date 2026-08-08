package inspections

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/admin/twigmigration"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/rewrite"
	"github.com/shopware/shopware-lsp/internal/shopware"
)

const (
	addAdminPropFixID              lsp.FixID = "add-admin-component-prop"
	bindAdminPropFixID             lsp.FixID = "bind-admin-component-prop"
	replaceAdminComponentTagFixID  lsp.FixID = "replace-admin-component-tag"
	migrateAdminI18nTCFixID        lsp.FixID = "migrate-admin-vue-i18n-tc"
	migrateAdminTwigComponentFixID lsp.FixID = "migrate-admin-twig-component"
)

type adminPropPayload struct {
	Component string `json:"component"`
	Prop      string `json:"prop"`
}

type adminStaticPropPayload struct {
	Attribute string `json:"attribute"`
}

type adminComponentTagPayload struct {
	Component   string `json:"component"`
	Replacement string `json:"replacement"`
}

func NewAdmin(
	index *admin.AdminComponentIndexer,
	versions ...shopware.ResolvedVersion,
) lsp.Inspection {
	version := shopware.ResolvedVersion{}
	if len(versions) != 0 {
		version = versions[0]
	}
	adminAnalyzer := diagnostics.NewAdminAnalyzer(index)
	if version.AtLeast(6, 7, 0) {
		names := make([]string, 0, len(twigmigration.Rules()))
		for _, rule := range twigmigration.Rules() {
			names = append(names, rule.SourceTag)
		}
		adminAnalyzer.SuppressComponentDiagnostics(names...)
	}
	inspection := &boundInspection{
		definition: lsp.InspectionDefinition{
			ID: "shopware.admin",
			Languages: []language.ID{
				language.JavaScript,
				language.Twig,
				language.Vue,
			},
			Problems: []lsp.ProblemDefinition{
				{ID: "admin.application-container.unknown-member", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.shopware-context.unknown-member", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.shopware-utils.unknown-member", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.component.deprecated", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityHint},
				{ID: "admin.component.deprecated-block", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityHint},
				{ID: "admin.component.deprecated-member", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityHint},
				{ID: "admin.component.deprecated-prop", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityHint},
				{ID: "admin.component.block-not-found", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.component.bound-prop-type", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.component.invalid-prop-value", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.component.model-not-assignable", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.component.model-type", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.component.not-found", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.component.missing-required-prop", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.component.static-prop-type", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.component.unknown-slot-prop", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.component.unknown-prop", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.component.unknown-event", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.component.unknown-model", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.component.unknown-slot", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.component.unknown-vue-member", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.component.unknown-instance-member", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.component.unknown-template-member", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.component.parent-not-found", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.component.registry-not-found", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.cms-element.not-found", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.cms-block.not-found", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.directive.not-found", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.filter.not-found", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.privilege.not-found", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.module.not-found", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.module-route.not-found", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.slot-syntax-deprecated", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "admin.vue-i18n.tc-deprecated", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
			},
		},
		analyzer: adminAnalyzer,
		analyzers: []ProblemAnalyzer{
			diagnostics.NewAdminTwigMigrationAnalyzer(version),
			diagnostics.NewAdminSlotMigrationAnalyzer(),
			diagnostics.NewAdminI18nDeprecationAnalyzer(),
		},
		fixes: []lsp.QuickFix{
			adminPropFix{index: index}, adminStaticPropFix{}, adminSlotMigrationFix{},
			adminI18nTCFix{}, adminComponentTagFix{},
			adminTwigComponentMigrationFix{}, suggestionFix{},
		},
		bind: func(code lsp.DiagnosticID, payload map[string]any) []lsp.BoundFix {
			if strings.HasPrefix(string(code), "admin.twig.migration.") {
				safe, _ := payload["safe"].(bool)
				value := diagnostics.AdminTwigMigrationPayload{
					Rule: mapString(payload, "rule"), SourceTag: mapString(payload, "sourceTag"),
					TargetTag: mapString(payload, "targetTag"), Safe: safe,
				}
				if !value.Safe || value.Rule == "" || value.SourceTag == "" || value.TargetTag == "" {
					return nil
				}
				return []lsp.BoundFix{lsp.BindFix(migrateAdminTwigComponentFixID, value)}
			}
			if string(code) == "admin.component.not-found" {
				return adminComponentTagBoundFixes(payload)
			}
			if string(code) == "admin.application-container.unknown-member" ||
				string(code) == "admin.shopware-context.unknown-member" ||
				string(code) == "admin.shopware-utils.unknown-member" ||
				string(code) == "admin.component.registry-not-found" ||
				string(code) == "admin.component.block-not-found" ||
				string(code) == "admin.cms-element.not-found" ||
				string(code) == "admin.cms-block.not-found" ||
				string(code) == "admin.directive.not-found" ||
				string(code) == "admin.filter.not-found" ||
				string(code) == "admin.component.invalid-prop-value" ||
				string(code) == "admin.component.unknown-prop" ||
				string(code) == "admin.component.unknown-event" ||
				string(code) == "admin.component.unknown-model" ||
				string(code) == "admin.component.unknown-slot" ||
				string(code) == "admin.component.unknown-slot-prop" ||
				string(code) == "admin.component.unknown-vue-member" ||
				string(code) == "admin.component.unknown-instance-member" ||
				string(code) == "admin.component.unknown-template-member" ||
				string(code) == "admin.privilege.not-found" ||
				string(code) == "admin.module.not-found" ||
				string(code) == "admin.module-route.not-found" {
				return suggestionBoundFixes(payload)
			}
			if string(code) == "admin.slot-syntax-deprecated" {
				replacement := mapString(payload, "replacement")
				if replacement == "" {
					return nil
				}
				return []lsp.BoundFix{lsp.BindFix(
					migrateAdminSlotFixID,
					adminSlotReplacement{Replacement: replacement},
				)}
			}
			if string(code) == "admin.vue-i18n.tc-deprecated" {
				replacement := mapString(payload, "replacement")
				if replacement != "$t" {
					return nil
				}
				return []lsp.BoundFix{lsp.BindFix(
					migrateAdminI18nTCFixID,
					adminI18nTCReplacement{Replacement: replacement},
				)}
			}
			if string(code) == "admin.component.static-prop-type" {
				attribute := mapString(payload, "attributeName")
				if attribute == "" {
					return nil
				}
				return []lsp.BoundFix{lsp.BindFix(
					bindAdminPropFixID,
					adminStaticPropPayload{Attribute: attribute},
				)}
			}
			if string(code) != "admin.component.missing-required-prop" {
				return nil
			}
			value := adminPropPayload{
				Component: mapString(payload, "componentName"),
				Prop:      mapString(payload, "propName"),
			}
			if value.Component == "" || value.Prop == "" {
				return nil
			}
			return []lsp.BoundFix{lsp.BindFix(addAdminPropFixID, value)}
		},
	}
	for _, rule := range twigmigration.Rules() {
		inspection.definition.Problems = append(inspection.definition.Problems, lsp.ProblemDefinition{
			ID: lsp.DiagnosticID("admin.twig.migration." + rule.ID), Source: "shopware-lsp",
			DefaultSeverity: protocol.DiagnosticSeverityWarning,
		})
	}
	return inspection
}

type adminComponentTagFix struct{}

func (adminComponentTagFix) ID() lsp.FixID {
	return replaceAdminComponentTagFixID
}

func (adminComponentTagFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[adminComponentTagPayload](fixContext)
	return lsp.FixPresentation{
		Title: fmt.Sprintf(
			"Replace component '%s' with '%s'",
			payload.Component,
			payload.Replacement,
		),
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Component != "" && payload.Replacement != "", err
}

func (adminComponentTagFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[adminComponentTagPayload](fixContext)
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
	if fixContext.Document.SyntaxTree == nil ||
		fixContext.Document.SyntaxTree.Root == nil {
		return rewrite.WorkspacePlan{}, fmt.Errorf("component template is no longer available")
	}
	targetRange := protocolTextRange(
		fixContext.Document.LineIndex,
		fixContext.Diagnostic.Range,
	)
	builder := rewrite.NewBuilder(fixContext.Document.Source)
	replaced := false
	for _, startTag := range twigquery.Nodes(
		fixContext.Document.SyntaxTree.Root,
		twigsyntax.HtmlStartingTag,
	) {
		selector, dynamic := admin.TwigDynamicComponentSelector(startTag)
		if !dynamic {
			continue
		}
		for _, candidate := range selector.Candidates {
			if candidate.Name != payload.Component || candidate.Range != targetRange {
				continue
			}
			if err := builder.ReplaceRange(
				candidate.Range, payload.Replacement,
			); err != nil {
				return rewrite.WorkspacePlan{}, err
			}
			replaced = true
			break
		}
		if replaced {
			break
		}
	}
	for _, node := range twigquery.Nodes(
		fixContext.Document.SyntaxTree.Root,
		twigsyntax.HtmlTag,
	) {
		if replaced {
			break
		}
		tag, ok := twigast.CastHtmlTag(node)
		if !ok {
			continue
		}
		starting, ok := tag.StartingTag()
		if !ok || starting.Name() == nil ||
			starting.Name().Text() != payload.Component ||
			starting.Name().Range() != targetRange {
			continue
		}
		if err := builder.ReplaceRange(
			starting.Name().Range(), payload.Replacement,
		); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
		replaced = true
		if ending, ok := tag.EndingTag(); ok && ending.Name() != nil &&
			ending.Name().Text() == payload.Component {
			if err := builder.ReplaceRange(
				ending.Name().Range(), payload.Replacement,
			); err != nil {
				return rewrite.WorkspacePlan{}, err
			}
		}
		break
	}
	if !replaced {
		return rewrite.WorkspacePlan{}, fmt.Errorf("component tag is no longer available")
	}
	edits, err := builder.Finish()
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	version := fixContext.Document.Version
	return rewrite.WorkspacePlan{Documents: []rewrite.DocumentPlan{
		rewrite.NewDocumentPlan(
			fixContext.Document.URI,
			&version,
			fixContext.Document.Source,
			edits,
		),
	}}, nil
}

func adminComponentTagBoundFixes(payload map[string]any) []lsp.BoundFix {
	component := mapString(payload, "componentName")
	if component == "" {
		return nil
	}
	var suggestions []string
	switch values := payload["suggestions"].(type) {
	case []string:
		suggestions = values
	case []any:
		for _, value := range values {
			if name, ok := value.(string); ok {
				suggestions = append(suggestions, name)
			}
		}
	}
	result := make([]lsp.BoundFix, 0, len(suggestions))
	seen := make(map[string]struct{}, len(suggestions))
	for _, replacement := range suggestions {
		replacement = strings.TrimSpace(replacement)
		if replacement == "" || replacement == component {
			continue
		}
		if _, exists := seen[replacement]; exists {
			continue
		}
		seen[replacement] = struct{}{}
		result = append(result, lsp.BindFix(
			replaceAdminComponentTagFixID,
			adminComponentTagPayload{
				Component: component, Replacement: replacement,
			},
		))
	}
	return result
}

type adminStaticPropFix struct{}

func (adminStaticPropFix) ID() lsp.FixID { return bindAdminPropFixID }

func (adminStaticPropFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[adminStaticPropPayload](fixContext)
	return lsp.FixPresentation{
		Title:      fmt.Sprintf("Bind '%s' as a Vue expression", payload.Attribute),
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Attribute != "", err
}

func (adminStaticPropFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[adminStaticPropPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	element, err := fixContext.Anchor.Resolve(
		fixContext.Document.URI,
		fixContext.Document.Version,
		fixContext.Document.SyntaxLanguage,
		fixContext.Document.SyntaxTree,
	)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	node, ok := element.(*twigsyntax.Node)
	if !ok {
		node = element.Parent()
	}
	attribute := twigquery.HTMLAttributeAt(node)
	if attribute == nil || twigquery.HTMLAttributeName(attribute) != payload.Attribute {
		return rewrite.WorkspacePlan{}, fmt.Errorf("component prop attribute is no longer available")
	}
	builder := rewrite.NewBuilder(fixContext.Document.Source)
	if err := builder.Insert(attribute.RangeTrimmedTrivia().Start, ":"); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	edits, err := builder.Finish()
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	version := fixContext.Document.Version
	return rewrite.WorkspacePlan{Documents: []rewrite.DocumentPlan{
		rewrite.NewDocumentPlan(
			fixContext.Document.URI,
			&version,
			fixContext.Document.Source,
			edits,
		),
	}}, nil
}

type adminPropFix struct {
	index *admin.AdminComponentIndexer
}

func (adminPropFix) ID() lsp.FixID { return addAdminPropFixID }

func (adminPropFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[adminPropPayload](fixContext)
	return lsp.FixPresentation{
		Title:      fmt.Sprintf("Add missing prop '%s'", payload.Prop),
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Component != "" && payload.Prop != "", err
}

func (f adminPropFix) BuildCommand(
	_ context.Context,
	fixContext lsp.FixContext,
) (*protocol.CommandAction, error) {
	payload, err := lsp.DecodeBoundFixPayload[adminPropPayload](fixContext)
	if err != nil {
		return nil, err
	}
	element, err := fixContext.Anchor.Resolve(
		fixContext.Document.URI,
		fixContext.Document.Version,
		fixContext.Document.SyntaxLanguage,
		fixContext.Document.SyntaxTree,
	)
	if err != nil {
		return nil, err
	}
	node, ok := element.(*twigsyntax.Node)
	if !ok {
		node = element.Parent()
	}
	startTag := twigquery.StartingHTMLTagAt(node)
	if startTag == nil {
		return nil, fmt.Errorf("component start tag is no longer available")
	}
	text := startTag.Text()
	offset := startTag.RangeTrimmedTrivia().End
	if strings.HasSuffix(text, "/>") {
		offset -= 2
	} else if strings.HasSuffix(text, ">") {
		offset--
	}
	line, character := fixContext.Document.LineIndex.PositionUTF16(offset)
	attribute, value := adminPropFormat(f.index, payload.Component, payload.Prop)
	snippet := fmt.Sprintf(" %s=\"%s$0\"", attribute, value)
	return &protocol.CommandAction{
		Title:   "Add missing prop",
		Command: "shopware.editor.insertSnippetAtPosition",
		Arguments: []any{
			fixContext.Document.URI,
			int(line),
			int(character),
			snippet,
		},
	}, nil
}

func adminPropFormat(
	index *admin.AdminComponentIndexer,
	component,
	propName string,
) (string, string) {
	attribute := admin.CamelToKebab(propName)
	if index == nil {
		return attribute, ""
	}
	components, err := index.GetComponentWithDefinition(component)
	if err != nil || len(components) == 0 {
		return attribute, ""
	}
	for _, prop := range components[0].Props {
		if prop.Name != propName {
			continue
		}
		switch strings.ToLower(prop.Type) {
		case "boolean":
			return ":" + attribute, defaultValue(prop.Default, "false")
		case "number":
			return ":" + attribute, defaultValue(prop.Default, "0")
		case "array":
			return ":" + attribute, defaultValue(prop.Default, "[]")
		case "object":
			return ":" + attribute, defaultValue(prop.Default, "{}")
		case "function":
			return ":" + attribute, "() => {}"
		default:
			return attribute, ""
		}
	}
	return attribute, ""
}

func defaultValue(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
