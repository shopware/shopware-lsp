package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/theme"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigDefinitionProvider struct {
	projectRoot  string
	twigIndexer  *twig.TwigIndexer
	iconProvider *theme.IconProvider
	phpIndex     *php.PHPIndex
}

func NewTwigDefinitionProvider(
	projectRoot string,
	twigIndexer *twig.TwigIndexer,
	extensionIndexer *extension.ExtensionIndexer,
	phpIndex *php.PHPIndex,
) *TwigDefinitionProvider {
	return &TwigDefinitionProvider{
		projectRoot:  projectRoot,
		twigIndexer:  twigIndexer,
		iconProvider: theme.NewIconProvider(projectRoot, extensionIndexer),
		phpIndex:     phpIndex,
	}
}

func (p *TwigDefinitionProvider) GetDefinition(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	switch strings.ToLower(filepath.Ext(params.TextDocument.URI)) {
	case ".php":
		if params.Node == nil {
			return []protocol.Location{}
		}
		return p.phpDefinitions(ctx, params)
	case ".twig":
		return p.twigDefinitions(ctx, params)
	default:
		return []protocol.Location{}
	}
}

func (p *TwigDefinitionProvider) twigDefinitions(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	if locations := p.twigSeeDefinitions(params); locations != nil {
		return locations
	}
	if locations := p.twigGuardDefinitions(params); locations != nil {
		return locations
	}
	if locations := p.twigTypesTagDefinitions(params); locations != nil {
		return locations
	}
	if locations := p.twigTestDefinitions(params); locations != nil {
		return locations
	}
	if locations := p.twigTagDefinitions(params); locations != nil {
		return locations
	}
	if params == nil || params.Node == nil {
		return []protocol.Location{}
	}
	if locations := p.twigVariableDefinitions(params); locations != nil {
		return locations
	}
	if locations := p.twigPHPMemberDefinitions(params); locations != nil {
		return locations
	}

	if twig.IsTwigTemplateString(params.Node) {
		itemValue := twigquery.StringValue(twigquery.LiteralStringAt(params.Node))

		files, _ := p.twigIndexer.GetTwigFilesByRelPath(itemValue)
		currentPath, err := uriutil.Path(params.TextDocument.URI)
		if err != nil {
			return nil
		}

		var locations []protocol.Location
		for _, file := range files {
			if file.Path == currentPath {
				continue
			}

			locations = append(locations, protocol.Location{
				URI: uriutil.FileURI(file.Path),
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      0,
						Character: 0,
					},
					End: protocol.Position{
						Line:      0,
						Character: 0,
					},
				},
			})
		}

		return locations
	}

	filterNode := twigquery.ClosestNodeOfKind(params.Node, twigsyntax.TwigFilter)
	filterName := twigquery.FilterName(filterNode)
	if filterName != "" && params.Token != nil && params.Token.Text() == filterName {
		filters, _ := p.twigIndexer.GetTwigFilter(filterName)

		var locations []protocol.Location
		for _, filter := range filters {
			locations = append(locations, protocol.Location{
				URI: uriutil.FileURI(filter.FilePath),
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      int(filter.Line) - 1,
						Character: 0,
					},
					End: protocol.Position{
						Line:      int(filter.Line) - 1,
						Character: 0,
					},
				},
			})
		}
		return locations
	}

	if functionName := twigquery.FunctionNameAt(params.Node); functionName != "" {
		functions, _ := p.twigIndexer.GetTwigFunction(functionName)
		var locations []protocol.Location
		for _, function := range functions {
			locations = append(locations, protocol.Location{
				URI: uriutil.FileURI(function.FilePath),
				Range: protocol.Range{
					Start: protocol.Position{Line: int(function.Line) - 1},
					End:   protocol.Position{Line: int(function.Line) - 1},
				},
			})
		}
		return locations
	}

	if twigquery.StringInTag(params.Node, "sw_icon") {
		text := twigquery.StringValue(twigquery.LiteralStringAt(params.Node))

		cfg := twigquery.HashStringMap(params.Node)

		pack, ok := cfg["pack"]
		if !ok {
			pack = "default"
		}

		icon := p.iconProvider.GetIcon(pack, text)

		if icon != "" {
			locations := []protocol.Location{
				{
					URI: uriutil.FileURI(icon),
					Range: protocol.Range{
						Start: protocol.Position{
							Line:      0,
							Character: 0,
						},
						End: protocol.Position{
							Line:      0,
							Character: 0,
						},
					},
				},
			}

			return locations
		}

	}

	return []protocol.Location{}
}

func (p *TwigDefinitionProvider) twigTagDefinitions(
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.twigIndexer == nil || request == nil ||
		request.DefinitionParams == nil || request.LineIndex == nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	for _, usage := range twig.TwigTagUsages(request.DocumentContent) {
		if !usage.Range.Contains(offset) {
			continue
		}
		tags, _, err := p.twigIndexer.ResolveTwigTag(usage.Name)
		if err != nil || len(tags) == 0 {
			return nil
		}
		seen := make(map[string]struct{}, len(tags))
		locations := make([]protocol.Location, 0, len(tags))
		for _, tag := range tags {
			key := tag.FilePath + ":" + tag.Range.String()
			if _, exists := seen[key]; exists {
				continue
			}
			location, found := twigVariableLocation(tag.FilePath, tag.Range)
			if !found {
				continue
			}
			seen[key] = struct{}{}
			locations = append(locations, location)
		}
		return locations
	}
	return nil
}

func (p *TwigDefinitionProvider) twigPHPMemberDefinitions(
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if request == nil || request.Node == nil || request.Root == nil ||
		p.phpIndex == nil {
		return nil
	}
	nameNode := twigquery.ClosestNodeOfKind(
		request.Node,
		twigsyntax.TwigLiteralName,
	)
	if nameNode == nil {
		return nil
	}
	templatePath, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil
	}
	resolution, ok := (twig.PHPAccessResolver{
		PHP:  p.phpIndex,
		Twig: p.twigIndexer,
	}).ResolveName(templatePath, request.Root, nameNode)
	if !ok {
		return nil
	}
	seen := make(map[string]struct{})
	var locations []protocol.Location
	for _, member := range resolution.Members {
		symbol := member.Symbol
		key := string(symbol.ID)
		if _, exists := seen[key]; exists {
			continue
		}
		rng := symbol.SelectionRange
		if rng.Len() == 0 {
			rng = symbol.Range
		}
		location, found := twigVariableLocation(symbol.Path, rng)
		if !found {
			continue
		}
		seen[key] = struct{}{}
		locations = append(locations, location)
	}
	return locations
}

func (p *TwigDefinitionProvider) twigVariableDefinitions(
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if request == nil || request.Node == nil {
		return nil
	}
	nameNode := twigquery.ClosestNodeOfKind(
		request.Node,
		twigsyntax.TwigLiteralName,
	)
	if nameNode == nil ||
		twigquery.ClosestNodeOfKind(nameNode, twigsyntax.TwigVar) == nil {
		return nil
	}
	if accessor := twigquery.ClosestNodeOfKind(
		nameNode,
		twigsyntax.TwigAccessor,
	); accessor != nil {
		names := twigquery.Nodes(accessor, twigsyntax.TwigLiteralName)
		if len(names) == 0 || names[0] != nameNode {
			return nil
		}
	}
	name := strings.TrimSpace(nameNode.Text())
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil
	}
	var locations []protocol.Location
	seen := make(map[string]struct{})
	if p.phpIndex != nil {
		variables, variableErr := p.phpIndex.TwigTemplateVariableSources(
			name,
			twig.TemplateNames(path)...,
		)
		if variableErr != nil {
			return nil
		}
		for _, variable := range variables {
			if location, found := twigVariableLocation(
				variable.File,
				variable.Range,
			); found {
				key := variable.File + ":" + variable.Range.String()
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				locations = append(locations, location)
			}
		}
		if len(variables) != 0 {
			return locations
		}
	}
	if p.twigIndexer == nil {
		return nil
	}
	globals, err := p.twigIndexer.GetGlobals(name)
	if err != nil || len(globals) == 0 {
		return nil
	}
	for _, global := range globals {
		key := global.File + ":" + global.Range.String()
		if _, exists := seen[key]; exists {
			continue
		}
		location, found := twigVariableLocation(global.File, global.Range)
		if !found {
			continue
		}
		seen[key] = struct{}{}
		locations = append(locations, location)
	}
	return locations
}

func twigVariableLocation(
	path string,
	rng cst.TextRange,
) (protocol.Location, bool) {
	if path == "" {
		return protocol.Location{}, false
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return protocol.Location{}, false
	}
	lineIndex := cst.NewLineIndex(string(source))
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return protocol.Location{
		URI: uriutil.FileURI(path),
		Range: protocol.Range{
			Start: protocol.Position{
				Line:      int(startLine),
				Character: int(startCharacter),
			},
			End: protocol.Position{
				Line:      int(endLine),
				Character: int(endCharacter),
			},
		},
	}, true
}

func (p *TwigDefinitionProvider) phpDefinitions(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	if _, found := php.AssistantArgumentReference(
		ctx,
		params.Node,
		"Template",
	); found {
		return p.phpTemplateDefinitions(phpquery.StringValue(params.Node))
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line),
		uint32(params.Position.Character),
	)
	reference, found := twig.TemplateReferenceAt(
		twig.PHPTemplateReferences("", params.Root),
		offset,
	)
	if found {
		return p.phpTemplateDefinitions(reference.Template)
	}
	if template, looksLikeTemplate := twig.PHPTemplateLikeString(
		params.Node,
	); looksLikeTemplate {
		// Match the reference plugin's global PHP-string navigation without
		// promoting arbitrary strings to indexed usages or rename targets.
		return p.phpTemplateDefinitions(template)
	}

	return []protocol.Location{}
}

func (p *TwigDefinitionProvider) phpTemplateDefinitions(
	name string,
) []protocol.Location {
	if p == nil || p.twigIndexer == nil {
		return nil
	}
	files, _ := p.twigIndexer.GetTwigFilesByRelPath(name)
	var locations []protocol.Location
	for _, file := range files {
		locations = append(locations, protocol.Location{
			URI: uriutil.FileURI(file.Path),
			Range: protocol.Range{
				Start: protocol.Position{},
				End:   protocol.Position{},
			},
		})
	}
	return locations
}
