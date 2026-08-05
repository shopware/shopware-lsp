package refactor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// PHPTwigRenameProvider augments the PHP semantic rename with typed Twig
// member, constant, enum-case, and direct class-name usages.
type PHPTwigRenameProvider struct {
	base     lsp.RenameProvider
	index    *twig.TwigIndexer
	phpIndex *php.PHPIndex
}

func NewPHPTwigRenameProvider(
	base lsp.RenameProvider,
	index *twig.TwigIndexer,
	phpIndex *php.PHPIndex,
) *PHPTwigRenameProvider {
	return &PHPTwigRenameProvider{
		base:     base,
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *PHPTwigRenameProvider) Rename(
	ctx context.Context,
	request *lsp.RenameRequest,
) (*protocol.WorkspaceEdit, error) {
	if p == nil || p.base == nil {
		return nil, nil
	}
	edit, err := p.base.Rename(ctx, request)
	if err != nil || edit == nil || p.index == nil ||
		p.phpIndex == nil || request == nil ||
		request.RenameParams == nil || request.Document == nil ||
		request.Root == nil || request.LineIndex == nil ||
		strings.ToLower(filepath.Ext(request.TextDocument.URI)) != ".php" {
		return edit, err
	}
	path, pathErr := uriutil.Path(request.TextDocument.URI)
	if pathErr != nil {
		return edit, nil
	}
	document, snapshot := p.phpSemanticState(ctx, request, path)
	if document == nil || snapshot == nil {
		return edit, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	symbol, found := php.SymbolAt(document, snapshot, offset)
	if !found {
		return edit, nil
	}
	newName := strings.TrimPrefix(strings.TrimSpace(request.NewName), "$")
	if newName == "" {
		return edit, nil
	}
	if edit.Changes == nil {
		edit.Changes = make(map[string][]protocol.TextEdit)
	}

	cache := newTwigRenameLocationCache()
	seen := make(map[string]struct{})
	for uri, edits := range edit.Changes {
		for _, textEdit := range edits {
			seen[fmt.Sprintf(
				"%s:%d:%d:%d:%d",
				uri,
				textEdit.Range.Start.Line,
				textEdit.Range.Start.Character,
				textEdit.Range.End.Line,
				textEdit.Range.End.Character,
			)] = struct{}{}
		}
	}
	add := func(
		filePath string,
		textRange cst.TextRange,
		replacement func(string) string,
	) {
		location, source, ok := cache.location(filePath, textRange)
		if !ok {
			return
		}
		key := fmt.Sprintf(
			"%s:%d:%d:%d:%d",
			location.URI,
			location.Range.Start.Line,
			location.Range.Start.Character,
			location.Range.End.Line,
			location.Range.End.Character,
		)
		if _, duplicate := seen[key]; duplicate {
			return
		}
		seen[key] = struct{}{}
		edit.Changes[location.URI] = append(
			edit.Changes[location.URI],
			protocol.TextEdit{
				Range:   location.Range,
				NewText: replacement(source),
			},
		)
	}

	if target, supported := twig.PHPUsageTargetForSymbol(
		snapshot,
		symbol,
	); supported {
		references, queryErr := p.index.GetPHPUsageReferences(target)
		if queryErr != nil {
			return nil, queryErr
		}
		for _, reference := range references {
			current := reference
			add(
				current.FilePath,
				current.Range,
				func(source string) string {
					return renamedTwigPHPUsage(
						source,
						symbol,
						newName,
					)
				},
			)
		}
	}

	if constant, supported := twigConstantRenameTarget(
		snapshot,
		symbol,
	); supported {
		references, queryErr := p.index.GetConstantReferences(constant)
		if queryErr != nil {
			return nil, queryErr
		}
		for _, reference := range references {
			current := reference
			add(
				current.FilePath,
				current.Range,
				func(source string) string {
					if symbol.Kind == semantic.GlobalConstantSymbol {
						return replaceTwigQualifiedNameTail(
							source,
							newName,
						)
					}
					return replaceTwigScopedMemberName(
						source,
						newName,
					)
				},
			)
		}
	}
	return edit, nil
}

func (p *PHPTwigRenameProvider) phpSemanticState(
	ctx context.Context,
	request *lsp.RenameRequest,
	path string,
) (*semantic.Document, *semantic.Snapshot) {
	if phpContext := php.GetPHPContext(ctx); phpContext != nil &&
		phpContext.Document != nil && phpContext.Snapshot != nil {
		return phpContext.Document, phpContext.Snapshot
	}
	if document, found := p.phpIndex.SemanticDocument(path); found {
		return document, p.phpIndex.SemanticSnapshot()
	}
	document := p.phpIndex.AnalyzeDocument(
		path,
		request.Document.Version,
		(*phpsyntax.Node)(request.Root),
	)
	return document, p.phpIndex.SemanticSnapshot().WithDocument(document)
}

func twigConstantRenameTarget(
	snapshot *semantic.Snapshot,
	symbol semantic.Symbol,
) (twig.ConstantReference, bool) {
	switch symbol.Kind {
	case semantic.GlobalConstantSymbol:
		return twig.ConstantReference{
			Name: strings.TrimPrefix(symbol.FullyQualified, "\\"),
		}, true
	case semantic.ClassConstantSymbol, semantic.EnumCaseSymbol:
		container, found := snapshot.Symbol(symbol.Container)
		if !found || !container.IsClassLike() {
			return twig.ConstantReference{}, false
		}
		return twig.ConstantReference{
			Class: container.FullyQualified,
			Name:  symbol.Name,
		}, true
	default:
		return twig.ConstantReference{}, false
	}
}

func renamedTwigPHPUsage(
	current string,
	symbol semantic.Symbol,
	newName string,
) string {
	switch symbol.Kind {
	case semantic.MethodSymbol:
		if strings.EqualFold(current, symbol.Name) {
			return newName
		}
		shortcut := twig.TwigAttributeName(symbol.Name)
		if shortcut != "" && strings.EqualFold(current, shortcut) {
			if renamed := twig.TwigAttributeName(newName); renamed != "" {
				return renamed
			}
			return newName
		}
		return current
	case semantic.ClassConstantSymbol, semantic.EnumCaseSymbol:
		return replaceTwigScopedMemberName(current, newName)
	case semantic.PropertySymbol:
		return newName
	default:
		if symbol.IsClassLike() {
			return replaceTwigQualifiedNameTail(current, newName)
		}
		return current
	}
}

func replaceTwigScopedMemberName(current, newName string) string {
	if separator := strings.LastIndex(current, "::"); separator >= 0 {
		return current[:separator+2] + newName
	}
	return replaceTwigQualifiedNameTail(current, newName)
}

func replaceTwigQualifiedNameTail(current, newName string) string {
	arraySuffix := ""
	if strings.HasSuffix(current, "[]") {
		arraySuffix = "[]"
		current = strings.TrimSuffix(current, arraySuffix)
	}
	if separator := strings.LastIndex(current, "\\"); separator >= 0 {
		return current[:separator+1] + newName + arraySuffix
	}
	return newName + arraySuffix
}

type twigRenameLocationCache struct {
	files map[string]twigRenameFile
}

type twigRenameFile struct {
	source    string
	lineIndex *cst.LineIndex
}

func newTwigRenameLocationCache() *twigRenameLocationCache {
	return &twigRenameLocationCache{
		files: make(map[string]twigRenameFile),
	}
}

func (c *twigRenameLocationCache) location(
	path string,
	textRange cst.TextRange,
) (protocol.Location, string, bool) {
	file, exists := c.files[path]
	if !exists {
		content, err := os.ReadFile(path)
		if err != nil {
			return protocol.Location{}, "", false
		}
		file = twigRenameFile{
			source:    string(content),
			lineIndex: cst.NewLineIndex(string(content)),
		}
		c.files[path] = file
	}
	if textRange.End > uint32(len(file.source)) ||
		textRange.End < textRange.Start {
		return protocol.Location{}, "", false
	}
	startLine, startCharacter := file.lineIndex.PositionUTF16(
		textRange.Start,
	)
	endLine, endCharacter := file.lineIndex.PositionUTF16(textRange.End)
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
		},
		file.source[textRange.Start:textRange.End],
		true
}
