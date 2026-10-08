package completion

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type DoctrineCompletionProvider struct {
	index    *doctrine.Index
	phpIndex *php.PHPIndex
}

func NewDoctrineCompletionProvider(
	index *doctrine.Index,
	phpIndexes ...*php.PHPIndex,
) *DoctrineCompletionProvider {
	var phpIndex *php.PHPIndex
	if len(phpIndexes) != 0 {
		phpIndex = phpIndexes[0]
	}
	return &DoctrineCompletionProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *DoctrineCompletionProvider) GetCompletions(
	ctx context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.index == nil || request == nil ||
		request.Root == nil || request.Document == nil {
		return nil
	}
	path, _ := uriutil.Path(request.TextDocument.URI)
	extension := strings.ToLower(filepath.Ext(path))
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	if registration, found := doctrine.TypeRegistrationReferenceAt(
		path,
		request.Root,
		offset,
	); found && registration.Role == doctrine.TypeRegistrationClass {
		return p.dbalTypeClassCompletions(
			registration,
			extension,
			request.Document.Source,
		)
	}
	if extension != ".php" {
		return nil
	}
	if request.Node == nil {
		return nil
	}
	if items := dbalCompletionItems(p.index.DBALCompletionsAt(
		ctx,
		request.Root,
		request.Node,
	)); len(items) != 0 {
		return items
	}
	return nil
}

func (p *DoctrineCompletionProvider) dbalTypeClassCompletions(
	reference doctrine.TypeRegistrationReference,
	extension,
	source string,
) []protocol.CompletionItem {
	if p.phpIndex == nil {
		return nil
	}
	snapshot := p.phpIndex.SemanticSnapshot()
	var classes []string
	for _, symbol := range p.phpIndex.ClassSymbols() {
		if strings.EqualFold(
			symbol.FullyQualified,
			"Doctrine\\DBAL\\Types\\Type",
		) || !snapshot.IsSubtypeOf(
			symbol.FullyQualified,
			"Doctrine\\DBAL\\Types\\Type",
		) {
			continue
		}
		classes = append(classes, symbol.FullyQualified)
	}
	items := typeClassCompletionItems(
		classes,
		reference.Range,
		extension,
		source,
		"Doctrine DBAL type class",
		reference.ClassConstant,
	)
	if !reference.ObjectCreation {
		return items
	}
	for index := range items {
		className := items[index].Label
		if items[index].FilterText != "" {
			className = items[index].FilterText
		}
		short := className
		if separator := strings.LastIndex(short, `\`); separator >= 0 {
			short = short[separator+1:]
		}
		items[index].Label = short
		items[index].FilterText = className
		prefix := `new `
		if reference.ObjectCreationStarted {
			prefix = ""
		}
		items[index].InsertText = prefix + `\` + className + "()"
	}
	return items
}

func dbalCompletionItems(
	completions []doctrine.DBALCompletion,
) []protocol.CompletionItem {
	result := make([]protocol.CompletionItem, 0, len(completions))
	for _, completion := range completions {
		kind := protocol.FieldCompletion
		switch completion.Kind {
		case doctrine.DBALTableCompletion:
			kind = protocol.StructCompletion
		case doctrine.DBALAliasCompletion:
			kind = protocol.VariableCompletion
		}
		result = append(result, protocol.CompletionItem{
			Label:  completion.Label,
			Kind:   int(kind),
			Detail: completion.Detail,
		})
	}
	return result
}

func typeClassCompletionItems(
	classNames []string,
	reference cst.TextRange,
	extension,
	source,
	detail string,
	forceClassConstant bool,
) []protocol.CompletionItem {
	classConstant := forceClassConstant ||
		(extension == ".php" &&
			classConstantAt(source, reference.End))
	result := make([]protocol.CompletionItem, 0, len(classNames))
	seen := make(map[string]struct{}, len(classNames))
	for _, className := range classNames {
		className = strings.TrimPrefix(className, `\`)
		key := strings.ToLower(className)
		if className == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		item := protocol.CompletionItem{
			Label:  className,
			Kind:   int(protocol.ClassCompletion),
			Detail: detail,
		}
		if classConstant {
			short := className
			if separator := strings.LastIndex(short, `\`); separator >= 0 {
				short = short[separator+1:]
			}
			item.Label = short
			item.FilterText = className
			item.InsertText = `\` + className + "::class"
		}
		result = append(result, item)
	}
	return result
}

func classConstantAt(source string, end uint32) bool {
	if int(end) > len(source) {
		return false
	}
	suffix := source[end:]
	if len(suffix) > len("::class") {
		suffix = suffix[:len("::class")]
	}
	return strings.EqualFold(suffix, "::class")
}

func (p *DoctrineCompletionProvider) GetTriggerCharacters() []string {
	return []string{"'", "\"", ".", ":"}
}
