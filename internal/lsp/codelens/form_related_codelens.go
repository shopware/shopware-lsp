package codelens

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// FormRelatedCodeLensProvider exposes the reference plugin's controller-to-form
// related navigation as portable LSP code lenses.
type FormRelatedCodeLensProvider struct {
	forms *form.Index
	php   *php.PHPIndex
}

func NewFormRelatedCodeLensProvider(
	forms *form.Index,
	phpIndex *php.PHPIndex,
) *FormRelatedCodeLensProvider {
	return &FormRelatedCodeLensProvider{forms: forms, php: phpIndex}
}

func (p *FormRelatedCodeLensProvider) GetCodeLenses(
	ctx context.Context,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	if p == nil || p.forms == nil || p.php == nil ||
		request == nil || request.CodeLensParams == nil ||
		request.Document == nil ||
		request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil ||
		request.Document.LineIndex == nil {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".twig":
		return p.twigFormCodeLenses(ctx, path, request)
	case ".php":
	default:
		return nil, nil
	}
	root := request.Document.SyntaxTree.Root
	phpContext := p.php.AddDocumentContext(
		ctx,
		path,
		request.Document.Version,
		root,
		root,
	)
	semanticContext := php.GetPHPContext(phpContext)
	document := semanticContext.Document
	var classes []semantic.Symbol
	methods := make([]semantic.Symbol, 0)
	for _, symbol := range document.Symbols {
		if symbol.Kind == semantic.ClassSymbol {
			classes = append(classes, symbol)
		} else if symbol.Kind == semantic.MethodSymbol &&
			symbol.Visibility == semantic.Public {
			methods = append(methods, symbol)
		}
	}

	relations, err := p.forms.GetDataClassRelations()
	if err != nil {
		return nil, err
	}
	currentRelations := form.TypesInDocument(path, root)
	overlayClasses := make(map[string]struct{}, len(currentRelations))
	for _, current := range currentRelations {
		overlayClasses[normalizedFormClassName(current.Class)] = struct{}{}
	}
	filtered := make(
		[]form.DataClassRelation,
		0,
		len(relations)+len(currentRelations),
	)
	for _, relation := range relations {
		if filepath.Clean(relation.File) == filepath.Clean(path) {
			continue
		}
		if _, overlaid := overlayClasses[normalizedFormClassName(relation.Class)]; overlaid {
			continue
		}
		filtered = append(filtered, relation)
	}
	for _, current := range currentRelations {
		if current.DataClass != "" {
			filtered = append(filtered, form.DataClassRelation{
				Class:     current.Class,
				DataClass: current.DataClass,
				File:      current.File,
				NameRange: current.NameRange,
			})
		}
	}

	var result []protocol.CodeLens
	result = append(
		result,
		formDataClassCodeLenses(
			classes,
			filtered,
			semanticContext.Snapshot,
			request.Document.LineIndex,
		)...,
	)

	references := form.FactoryTypeReferences(phpContext, root)
	targetsByMethod := make(map[semantic.SymbolID][]string)
	for _, reference := range references {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		target, found, targetErr := p.formTarget(reference.Name)
		if targetErr != nil {
			return nil, targetErr
		}
		if !found {
			continue
		}
		for _, method := range methods {
			if !method.BodyRange.Contains(reference.Range.Start) {
				continue
			}
			targetsByMethod[method.ID] = append(
				targetsByMethod[method.ID],
				target,
			)
			break
		}
	}

	for _, method := range methods {
		targets := uniqueRelatedTargets(targetsByMethod[method.ID])
		if len(targets) == 0 {
			continue
		}
		title := "Open related form type"
		if len(targets) > 1 {
			title = fmt.Sprintf(
				"Open %d related form types",
				len(targets),
			)
		}
		result = append(result, relatedLens(
			relatedProtocolRange(
				method.SelectionRange,
				request.Document.LineIndex,
			),
			title,
			targets,
		))
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Range.Start.Line != result[right].Range.Start.Line {
			return result[left].Range.Start.Line <
				result[right].Range.Start.Line
		}
		return result[left].Command.Title < result[right].Command.Title
	})
	return result, nil
}

func (p *FormRelatedCodeLensProvider) twigFormCodeLenses(
	ctx context.Context,
	path string,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	variables, err := form.TwigFormVariables(p.php, path)
	if err != nil {
		return nil, err
	}
	references := form.TwigFormFunctionReferences(
		request.Document.SyntaxTree.Root,
		variables,
	)
	var result []protocol.CodeLens
	for _, reference := range references {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var targets []string
		for _, formType := range reference.FormTypes {
			target, found, targetErr := p.formTarget(formType)
			if targetErr != nil {
				return nil, targetErr
			}
			if found {
				targets = append(targets, target)
			}
		}
		targets = uniqueRelatedTargets(targets)
		if len(targets) == 0 {
			continue
		}
		title := "Open related form type"
		if len(targets) > 1 {
			title = fmt.Sprintf(
				"Open %d related form types",
				len(targets),
			)
		}
		result = append(result, relatedLens(
			relatedProtocolRange(
				reference.Range,
				request.Document.LineIndex,
			),
			title,
			targets,
		))
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Range.Start.Line != result[right].Range.Start.Line {
			return result[left].Range.Start.Line <
				result[right].Range.Start.Line
		}
		return result[left].Range.Start.Character <
			result[right].Range.Start.Character
	})
	return result, nil
}

func formDataClassCodeLenses(
	classes []semantic.Symbol,
	relations []form.DataClassRelation,
	snapshot *semantic.Snapshot,
	lineIndex *cst.LineIndex,
) []protocol.CodeLens {
	if snapshot == nil || lineIndex == nil ||
		len(classes) == 0 || len(relations) == 0 {
		return nil
	}
	byForm := make(map[string][]form.DataClassRelation)
	byDataClass := make(map[string][]form.DataClassRelation)
	for _, relation := range relations {
		formName := normalizedFormClassName(relation.Class)
		dataClass := normalizedFormClassName(relation.DataClass)
		if formName == "" || dataClass == "" {
			continue
		}
		byForm[formName] = append(byForm[formName], relation)
		byDataClass[dataClass] = append(
			byDataClass[dataClass],
			relation,
		)
	}

	var result []protocol.CodeLens
	for _, class := range classes {
		name := normalizedFormClassName(class.FullyQualified)
		rng := class.SelectionRange
		if rng.Len() == 0 {
			rng = class.Range
		}
		if formRelations := byForm[name]; len(formRelations) != 0 &&
			isFormOrExtensionClass(snapshot, class.FullyQualified) {
			var targets []string
			for _, relation := range formRelations {
				targets = append(
					targets,
					phpClassTargets(
						snapshot,
						relation.DataClass,
						form.DataClassRelation{},
					)...,
				)
			}
			targets = uniqueRelatedTargets(targets)
			if len(targets) != 0 {
				title := "Open form data class"
				if len(targets) > 1 {
					title = fmt.Sprintf(
						"Open %d data class declarations",
						len(targets),
					)
				}
				result = append(result, relatedLens(
					relatedProtocolRange(rng, lineIndex),
					title,
					targets,
				))
			}
		}

		var targets []string
		for _, relation := range byDataClass[name] {
			targets = append(
				targets,
				phpClassTargets(snapshot, relation.Class, relation)...,
			)
		}
		targets = uniqueRelatedTargets(targets)
		if len(targets) == 0 {
			continue
		}
		title := "Open related form type"
		if len(targets) > 1 {
			title = fmt.Sprintf("Open %d related form types", len(targets))
		}
		result = append(result, relatedLens(
			relatedProtocolRange(rng, lineIndex),
			title,
			targets,
		))
	}
	return result
}

func isFormOrExtensionClass(
	snapshot *semantic.Snapshot,
	className string,
) bool {
	return snapshot.IsSubtypeOf(
		className,
		"Symfony\\Component\\Form\\FormTypeInterface",
	) || snapshot.IsSubtypeOf(
		className,
		"Symfony\\Component\\Form\\FormTypeExtensionInterface",
	)
}

func normalizedFormClassName(value string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(value), `\`))
}

func phpClassTargets(
	snapshot *semantic.Snapshot,
	className string,
	fallback form.DataClassRelation,
) []string {
	var preferred []string
	var internal []string
	for _, symbol := range snapshot.Classes(className) {
		if symbol.Path == "" {
			continue
		}
		rng := symbol.SelectionRange
		if rng.Len() == 0 {
			rng = symbol.Range
		}
		target := relatedTarget(
			symbol.Path,
			relatedSourceLine(symbol.Path, rng.Start),
		)
		if symbol.Flags.Has(semantic.InternalFlag) {
			internal = append(internal, target)
		} else {
			preferred = append(preferred, target)
		}
	}
	if len(preferred) != 0 {
		return uniqueRelatedTargets(preferred)
	}
	if len(internal) != 0 {
		return uniqueRelatedTargets(internal)
	}
	if fallback.File == "" {
		return nil
	}
	return []string{relatedTarget(
		fallback.File,
		relatedSourceLine(fallback.File, fallback.NameRange.Start),
	)}
}

func (p *FormRelatedCodeLensProvider) formTarget(
	name string,
) (string, bool, error) {
	current, found, err := p.forms.GetType(name)
	if err != nil || !found {
		return "", false, err
	}
	if symbol, exists := p.php.FindClass(current.Class); exists {
		rng := symbol.SelectionRange
		if rng.Len() == 0 {
			rng = symbol.Range
		}
		return relatedTarget(
			symbol.Path,
			relatedSourceLine(symbol.Path, rng.Start),
		), true, nil
	}
	if current.File == "" {
		return "", false, nil
	}
	return relatedTarget(
		current.File,
		relatedSourceLine(current.File, current.NameRange.Start),
	), true, nil
}

func (p *FormRelatedCodeLensProvider) ResolveCodeLens(
	_ context.Context,
	lens *protocol.CodeLens,
) (*protocol.CodeLens, error) {
	return lens, nil
}
