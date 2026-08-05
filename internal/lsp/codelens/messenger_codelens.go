package codelens

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/messenger"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type MessengerCodeLensProvider struct {
	index    *messenger.Index
	phpIndex *php.PHPIndex
}

func NewMessengerCodeLensProvider(
	index *messenger.Index,
	phpIndex *php.PHPIndex,
) *MessengerCodeLensProvider {
	return &MessengerCodeLensProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *MessengerCodeLensProvider) GetCodeLenses(
	ctx context.Context,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	if p == nil || p.index == nil || p.phpIndex == nil ||
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
	if !strings.EqualFold(filepath.Ext(path), ".php") {
		return nil, nil
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
	snapshot := php.GetPHPContext(phpContext).Snapshot
	var result []protocol.CodeLens
	for _, symbol := range document.Symbols {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		switch symbol.Kind {
		case semantic.ClassSymbol,
			semantic.InterfaceSymbol,
			semantic.EnumSymbol:
			message, found, getErr := p.index.GetMessage(
				symbol.FullyQualified,
			)
			if getErr != nil {
				return nil, getErr
			}
			if !found {
				continue
			}
			targets := messengerOccurrenceTargets(message.Occurrences)
			if len(targets) == 0 {
				continue
			}
			result = append(result, relatedLens(
				relatedProtocolRange(
					symbol.SelectionRange,
					request.Document.LineIndex,
				),
				fmt.Sprintf(
					"Messenger · %d handler(s) · %d dispatch(es)",
					len(message.Handlers()),
					len(message.Dispatches()),
				),
				targets,
			))
		case semantic.MethodSymbol:
			class, exists := snapshot.Symbol(symbol.Container)
			if !exists {
				continue
			}
			messages, getErr := p.index.MessagesForHandler(
				class.FullyQualified,
				symbol.Name,
			)
			if getErr != nil {
				return nil, getErr
			}
			if len(messages) == 0 {
				continue
			}
			var targets []string
			for _, message := range messages {
				if declaration, found := p.phpIndex.FindClass(
					message.Name,
				); found {
					rng := declaration.SelectionRange
					if rng.Len() == 0 {
						rng = declaration.Range
					}
					targets = append(targets, relatedTarget(
						declaration.Path,
						relatedSourceLine(
							declaration.Path,
							rng.Start,
						),
					))
				}
				targets = append(
					targets,
					messengerOccurrenceTargets(
						message.Dispatches(),
					)...,
				)
			}
			targets = uniqueRelatedTargets(targets)
			if len(targets) == 0 {
				continue
			}
			title := "Handles Messenger message"
			if len(messages) > 1 {
				title = fmt.Sprintf(
					"Handles %d Messenger messages",
					len(messages),
				)
			}
			result = append(result, relatedLens(
				relatedProtocolRange(
					symbol.SelectionRange,
					request.Document.LineIndex,
				),
				title,
				targets,
			))
		}
	}
	sortRelatedCodeLenses(result)
	return result, nil
}

func messengerOccurrenceTargets(
	occurrences []messenger.Occurrence,
) []string {
	var targets []string
	for _, occurrence := range occurrences {
		rng := occurrence.Range
		if occurrence.Kind == messenger.HandlerOccurrence &&
			occurrence.HandlerRange.Len() != 0 {
			rng = occurrence.HandlerRange
		} else if occurrence.MessageRange.Len() != 0 {
			rng = occurrence.MessageRange
		}
		targets = append(targets, relatedTarget(
			occurrence.File,
			relatedSourceLine(occurrence.File, rng.Start),
		))
	}
	return uniqueRelatedTargets(targets)
}

func (p *MessengerCodeLensProvider) ResolveCodeLens(
	_ context.Context,
	lens *protocol.CodeLens,
) (*protocol.CodeLens, error) {
	return lens, nil
}
