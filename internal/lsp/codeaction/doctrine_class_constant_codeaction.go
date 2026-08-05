package codeaction

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type DoctrineClassConstantCodeActionProvider struct {
	doctrineIndex *doctrine.Index
	phpIndex      *php.PHPIndex
}

func NewDoctrineClassConstantCodeActionProvider(
	doctrineIndex *doctrine.Index,
	phpIndex *php.PHPIndex,
) *DoctrineClassConstantCodeActionProvider {
	return &DoctrineClassConstantCodeActionProvider{
		doctrineIndex: doctrineIndex,
		phpIndex:      phpIndex,
	}
}

func (p *DoctrineClassConstantCodeActionProvider) GetCodeActionKinds() []protocol.CodeActionKind {
	return []protocol.CodeActionKind{protocol.CodeActionRefactorRewrite}
}

func (p *DoctrineClassConstantCodeActionProvider) GetCodeActions(
	ctx context.Context,
	request *lsp.CodeActionRequest,
) []protocol.CodeAction {
	if ctx.Err() != nil || p == nil || p.doctrineIndex == nil ||
		p.phpIndex == nil || request == nil ||
		request.CodeActionParams == nil || request.Document == nil ||
		request.Root == nil || request.Node == nil ||
		request.Document.SyntaxLanguage != language.PHP {
		return nil
	}
	literal := phpquery.StringAt(request.Node)
	if literal == nil || phpquery.StringValue(literal) == "" {
		return nil
	}
	path, err := uriutil.Path(request.Document.URI)
	if err != nil {
		return nil
	}
	semanticContext := p.phpIndex.AddDocumentContext(
		ctx,
		path,
		request.Document.Version,
		request.Node,
		request.Root,
	)
	reference, found := p.doctrineIndex.ReferenceAt(
		semanticContext,
		request.Root,
		literal,
	)
	if !found || reference.Role != doctrine.EntityReference ||
		reference.Kind != doctrine.StringReference {
		return nil
	}
	className := p.resolveEntityClass(
		phpquery.StringValue(literal),
		php.NewNameResolver(request.Root),
	)
	if className == "" {
		return nil
	}
	qualifier, importEdit := phpClassQualifier(request, className)
	if qualifier == "" {
		return nil
	}
	rng := literal.RangeTrimmedTrivia()
	edits := []protocol.TextEdit{{
		Range:   offsetRange(request, rng.Start, rng.End),
		NewText: qualifier + "::class",
	}}
	if importEdit != nil {
		edits = append(edits, *importEdit)
	}
	return []protocol.CodeAction{{
		Title: "Doctrine: use class constant",
		Kind:  protocol.CodeActionRefactorRewrite,
		Edit: &protocol.WorkspaceEdit{
			Changes: map[string][]protocol.TextEdit{
				request.TextDocument.URI: edits,
			},
		},
	}}
}

func (p *DoctrineClassConstantCodeActionProvider) resolveEntityClass(
	value string,
	resolver *php.NameResolver,
) string {
	value = strings.Trim(strings.TrimSpace(value), `\`)
	if value == "" {
		return ""
	}
	models, err := p.doctrineIndex.Models()
	if err != nil {
		return ""
	}
	if !strings.Contains(value, ":") {
		direct := strings.Trim(resolver.Resolve(value), `\`)
		if _, found := p.phpIndex.FindClass(direct); found {
			if len(models) == 0 {
				return direct
			}
			for _, model := range models {
				if strings.EqualFold(
					strings.Trim(model.Class, `\`),
					direct,
				) {
					return direct
				}
			}
		}
	}

	shortName := value
	if separator := strings.LastIndex(shortName, ":"); separator >= 0 {
		shortName = shortName[separator+1:]
	}
	shortName = strings.ReplaceAll(shortName, "/", `\`)
	if separator := strings.LastIndex(shortName, `\`); separator >= 0 {
		shortName = shortName[separator+1:]
	}
	var match string
	for _, model := range models {
		className := strings.Trim(model.Class, `\`)
		modelShortName := className
		if separator := strings.LastIndex(modelShortName, `\`); separator >= 0 {
			modelShortName = modelShortName[separator+1:]
		}
		if !strings.EqualFold(modelShortName, shortName) {
			continue
		}
		if match != "" && !strings.EqualFold(match, className) {
			return ""
		}
		match = className
	}
	if match == "" {
		return ""
	}
	if _, found := p.phpIndex.FindClass(match); !found {
		return ""
	}
	return match
}
