package diagnostics

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/suggestion"
)

func (p *AdminAnalyzer) twigPrivilegeDiagnostics(
	document *lsp.TextDocument,
	analysis *adminTwigDiagnosticDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		analysis == nil {
		return nil, nil
	}
	var references []admin.AdminTwigRegistryReference
	for _, reference := range analysis.registryReferences {
		if reference.Kind == admin.AdminSymbolPrivilege && reference.Name != "" {
			references = append(references, reference)
		}
	}
	if len(references) == 0 {
		return nil, nil
	}
	privileges, err := p.adminIndexer.GetAllPrivileges()
	if err != nil || len(privileges) == 0 {
		return nil, err
	}
	known := make(map[string]struct{}, len(privileges))
	names := make([]string, 0, len(privileges))
	hasProjectPrivileges := false
	for _, privilege := range privileges {
		if privilege.Name == "" {
			continue
		}
		if !privilege.IsBuiltin() {
			hasProjectPrivileges = true
		}
		if _, exists := known[privilege.Name]; exists {
			continue
		}
		known[privilege.Name] = struct{}{}
		names = append(names, privilege.Name)
	}
	// Preserve the analyzer's fail-open behavior for an empty or not-yet-built
	// project index. The built-in administrator key remains available to
	// completion and hover without making it look like a complete ACL registry.
	if !hasProjectPrivileges {
		return nil, nil
	}
	var diagnostics []lsp.Problem
	for _, reference := range references {
		if _, exists := known[reference.Name]; exists {
			continue
		}
		diagnostics = append(diagnostics, lsp.Problem{
			Range: reference.Range,
			Message: fmt.Sprintf(
				"Administration privilege '%s' is not registered",
				reference.Name,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.privilege.not-found",
			Payload: map[string]any{
				"privilegeName": reference.Name,
				"suggestions": suggestion.Similar(
					reference.Name,
					names,
				),
			},
		})
	}
	return diagnostics, nil
}

// checkBlockReferences checks block overrides against the source registration's
// parent contract. Both Component.extend and Component.override templates use
// the same resolution path.
func (p *AdminAnalyzer) checkBlockReferences(
	rootNode *twigsyntax.Node,
	lineIndex *twigsyntax.LineIndex,
	analysis *adminTwigDiagnosticDocument,
	diagnostics *[]lsp.Problem,
) {
	if analysis == nil || analysis.liveOwner == nil ||
		analysis.liveOwner.Kind != admin.ComponentExtend &&
			analysis.liveOwner.Kind != admin.ComponentOverride &&
			analysis.liveOwner.ExtendsComponent == "" {
		return
	}

	parent, err := p.adminIndexer.GetParentComponentForTemplate(
		analysis.templatePath,
	)
	if err != nil || parent == nil {
		return
	}
	blocks := make(map[string]admin.TwigBlock, len(parent.Blocks))
	names := make([]string, 0, len(parent.Blocks))
	for _, block := range parent.Blocks {
		if block.Name == "" {
			continue
		}
		blocks[block.Name] = block
		names = append(names, block.Name)
	}
	p.findBlockTags(
		rootNode, lineIndex, parent.Name, blocks, names, diagnostics,
	)
}

// findBlockTags finds all {% block %} tags and checks if they exist in valid blocks
func (p *AdminAnalyzer) findBlockTags(
	node *twigsyntax.Node,
	_ *twigsyntax.LineIndex,
	parentName string,
	validBlocks map[string]admin.TwigBlock,
	blockNames []string,
	diagnostics *[]lsp.Problem,
) {
	if node == nil {
		return
	}

	for _, blockNode := range twigquery.Nodes(node, twigsyntax.TwigBlock) {
		blockName := twigquery.BlockName(blockNode)
		block, cast := twigast.CastTwigBlock(blockNode)
		if blockName == "" || !cast || block.Name() == nil {
			continue
		}
		parentBlock, exists := validBlocks[blockName]
		if !exists {
			suggestions := adminNearbySuggestions(blockName, blockNames)
			if len(suggestions) == 0 {
				// Component.extend/override templates may introduce their own
				// extensibility blocks; absence from the parent is not itself an
				// error. Only a close parent-block spelling gives enough evidence
				// to report a likely typo.
				continue
			}
			*diagnostics = append(*diagnostics, lsp.Problem{
				Range: block.Name().Range(),
				Message: fmt.Sprintf(
					"Block '%s' does not exist in parent component '%s'",
					blockName, parentName,
				),
				Source:   "shopware-lsp",
				Severity: protocol.DiagnosticSeverityWarning,
				ID:       "admin.component.block-not-found",
				Payload: map[string]any{
					"blockName": blockName, "suggestions": suggestions,
				},
			})
			continue
		}
		if parentBlock.Deprecated != "" {
			*diagnostics = append(*diagnostics, lsp.Problem{
				Range: block.Name().Range(),
				Message: fmt.Sprintf(
					"Administration Twig block '%s' is deprecated: %s",
					blockName, parentBlock.Deprecated,
				),
				Source:   "shopware-lsp",
				Severity: protocol.DiagnosticSeverityHint,
				ID:       "admin.component.deprecated-block",
				Tags: []protocol.DiagnosticTag{
					protocol.DiagnosticTagDeprecated,
				},
			})
		}
	}
}

// findHTMLStartTags recursively finds all html_start_tag nodes and checks for missing required props
func (p *AdminAnalyzer) findHTMLStartTags(
	ctx context.Context,
	root *twigsyntax.Node,
	content []byte,
	analysis *adminTwigDiagnosticDocument,
	diagnostics *[]lsp.Problem,
) error {
	if root == nil || analysis == nil {
		return nil
	}

	for _, startTag := range twigquery.Nodes(root, twigsyntax.HtmlStartingTag) {
		if ctx.Err() != nil {
			return nil
		}
		if err := p.checkComponentSlotNames(
			startTag, analysis.templatePath, analysis.liveOwner, diagnostics,
		); err != nil {
			return err
		}
		if err := p.checkComponentProps(
			root, content, startTag, analysis, diagnostics,
		); err != nil {
			return err
		}
	}
	return nil
}

// checkComponentProps checks if a component tag has all required props
// <sw-button<caret>> - checks that all required props are present
