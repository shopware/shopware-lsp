package codelens

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/translation"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/shopware/shopware-lsp/internal/validation"
)

type ValidationCodeLensProvider struct {
	phpIndex     *php.PHPIndex
	translations *translation.Index
}

func NewValidationCodeLensProvider(
	phpIndex *php.PHPIndex,
	translationIndexes ...*translation.Index,
) *ValidationCodeLensProvider {
	provider := &ValidationCodeLensProvider{phpIndex: phpIndex}
	if len(translationIndexes) != 0 {
		provider.translations = translationIndexes[0]
	}
	return provider
}

func (p *ValidationCodeLensProvider) GetCodeLenses(
	ctx context.Context,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.CodeLensParams == nil || request.Document == nil ||
		request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil ||
		request.Document.LineIndex == nil ||
		!strings.HasSuffix(
			strings.ToLower(request.TextDocument.URI),
			".php",
		) {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	root := request.Document.SyntaxTree.Root
	phpContext := p.phpIndex.AddDocumentContext(
		ctx,
		path,
		request.Document.Version,
		root,
		root,
	)
	document := php.GetPHPContext(phpContext).Document
	var result []protocol.CodeLens
	for _, class := range document.Symbols {
		if class.Kind != semantic.ClassSymbol &&
			class.Kind != semantic.InterfaceSymbol {
			continue
		}
		counterpart, found := validation.CounterpartClass(
			p.phpIndex,
			class,
		)
		if !found {
			continue
		}
		title := "Open validator"
		if strings.HasSuffix(class.FullyQualified, "Validator") {
			title = "Open constraint"
		}
		result = append(result, relatedLens(
			relatedProtocolRange(
				class.SelectionRange,
				request.Document.LineIndex,
			),
			title,
			[]string{relatedTarget(
				counterpart.Path,
				relatedSourceLine(
					counterpart.Path,
					counterpart.SelectionRange.Start,
				),
			)},
		))
	}
	if p.translations != nil {
		for _, reference := range translation.PHPReferences(root) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if reference.PHPKind !=
				translation.PHPReferenceValidatorMessage ||
				reference.Container == nil ||
				reference.Container.Kind() !=
					phpsyntax.PhpPropertyDeclaration ||
				!translation.ValidatePHPReference(
					phpContext,
					reference,
					p.phpIndex,
					request.Document.Text,
				) {
				continue
			}
			messages, messageErr := p.translations.GetMessages(
				"validators",
				reference.Key,
			)
			if messageErr != nil {
				return nil, messageErr
			}
			var targets []string
			for _, message := range messages {
				targets = append(
					targets,
					relatedTarget(message.File, message.Line+1),
				)
			}
			targets = uniqueRelatedTargets(targets)
			if len(targets) == 0 {
				continue
			}
			title := "Open validator translation"
			if len(targets) > 1 {
				title = fmt.Sprintf(
					"Open %d validator translations",
					len(targets),
				)
			}
			result = append(result, relatedLens(
				relatedProtocolRange(
					phpquery.StringContentRange(reference.Node),
					request.Document.LineIndex,
				),
				title,
				targets,
			))
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Range.Start.Line !=
			result[right].Range.Start.Line {
			return result[left].Range.Start.Line <
				result[right].Range.Start.Line
		}
		if result[left].Range.Start.Character !=
			result[right].Range.Start.Character {
			return result[left].Range.Start.Character <
				result[right].Range.Start.Character
		}
		return result[left].Command.Title < result[right].Command.Title
	})
	return result, nil
}

func (p *ValidationCodeLensProvider) ResolveCodeLens(
	_ context.Context,
	lens *protocol.CodeLens,
) (*protocol.CodeLens, error) {
	return lens, nil
}
