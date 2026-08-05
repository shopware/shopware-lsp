package codeaction

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	phpresolver "github.com/shopware/shopware-lsp/internal/php/resolver"
)

func offsetRange(
	request *lsp.CodeActionRequest,
	start,
	end uint32,
) protocol.Range {
	if request == nil || request.LineIndex == nil {
		return protocol.Range{}
	}
	startLine, startCharacter := request.LineIndex.PositionUTF16(start)
	endLine, endCharacter := request.LineIndex.PositionUTF16(end)
	return protocol.Range{
		Start: protocol.Position{
			Line:      int(startLine),
			Character: int(startCharacter),
		},
		End: protocol.Position{
			Line:      int(endLine),
			Character: int(endCharacter),
		},
	}
}

func phpClassQualifier(
	request *lsp.CodeActionRequest,
	className string,
) (string, *protocol.TextEdit) {
	className = strings.Trim(strings.TrimSpace(className), `\`)
	if className == "" {
		return "", nil
	}
	separator := strings.LastIndex(className, `\`)
	shortName := className
	classNamespace := ""
	if separator >= 0 {
		shortName = className[separator+1:]
		classNamespace = className[:separator]
	}
	if request == nil || request.Root == nil {
		return `\` + className, nil
	}

	conflict := false
	for _, declaration := range phpquery.UseDeclarations(request.Root) {
		for _, imported := range phpresolver.ParseUseDeclaration(declaration.Text()) {
			if imported.Kind != phpresolver.ClassImport {
				continue
			}
			if strings.EqualFold(strings.Trim(imported.Target, `\`), className) {
				return imported.Alias, nil
			}
			if strings.EqualFold(imported.Alias, shortName) {
				conflict = true
			}
		}
	}
	for _, class := range phpquery.Classes(request.Root) {
		if strings.EqualFold(phpquery.ClassName(class), shortName) &&
			!strings.EqualFold(
				phpClassFullyQualifiedName(request.Root, class),
				className,
			) {
			conflict = true
		}
	}
	if conflict {
		return `\` + className, nil
	}
	if strings.EqualFold(
		strings.Trim(phpquery.Namespace(request.Root), `\`),
		classNamespace,
	) {
		return shortName, nil
	}

	offset := phpImportInsertionOffset(request.Root)
	newText := "\nuse " + className + ";"
	if len(phpquery.UseDeclarations(request.Root)) == 0 {
		newText = "\n\nuse " + className + ";"
	}
	return shortName, &protocol.TextEdit{
		Range:   offsetRange(request, offset, offset),
		NewText: newText,
	}
}

func phpImportInsertionOffset(root *phpsyntax.Node) uint32 {
	if root == nil {
		return 0
	}
	var result uint32
	for _, declaration := range phpquery.UseDeclarations(root) {
		if declaration.Range().End > result {
			result = declaration.Range().End
		}
	}
	if result != 0 {
		return result
	}
	if namespaces := phpquery.Nodes(root, phpsyntax.PhpNamespace); len(namespaces) != 0 {
		namespace := namespaces[0]
		if end := strings.IndexAny(namespace.Text(), ";{"); end >= 0 {
			return namespace.Range().Start + uint32(end+1)
		}
	}
	if openTag := root.FirstToken(); openTag != nil &&
		openTag.Kind() == phpsyntax.TkOpenTag {
		return openTag.Range().End
	}
	return 0
}
