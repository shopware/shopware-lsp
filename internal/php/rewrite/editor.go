// Package phprewrite provides lossless, composable PHP source rewrites on top
// of the immutable PHP CST.
package phprewrite

import (
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	phpresolver "github.com/shopware/shopware-lsp/internal/php/resolver"
	sourcerewrite "github.com/shopware/shopware-lsp/internal/rewrite"
)

// Editor collects related PHP edits against one immutable source snapshot.
// Callers may combine imports, list edits, class edits, and PHPDoc edits before
// validating the complete set with Finish.
type Editor struct {
	source  string
	root    *phpsyntax.Node
	builder *sourcerewrite.Builder

	imports            map[string]string
	importAliases      map[string]string
	plannedImports     map[string]string
	plannedAliases     map[string]string
	plannedImportCount int
	hasUseDeclarations bool
}

func NewEditor(source string, root *phpsyntax.Node) *Editor {
	editor := &Editor{
		source:         source,
		root:           root,
		builder:        sourcerewrite.NewBuilder(source),
		imports:        make(map[string]string),
		importAliases:  make(map[string]string),
		plannedImports: make(map[string]string),
		plannedAliases: make(map[string]string),
	}
	if root == nil {
		return editor
	}
	declarations := phpquery.UseDeclarations(root)
	editor.hasUseDeclarations = len(declarations) != 0
	for _, declaration := range declarations {
		for _, imported := range phpresolver.ParseUseDeclaration(declaration.Text()) {
			if imported.Kind != phpresolver.ClassImport {
				continue
			}
			editor.imports[strings.ToLower(imported.Alias)] = strings.Trim(imported.Target, "\\")
			editor.importAliases[strings.ToLower(strings.Trim(imported.Target, "\\"))] = imported.Alias
		}
	}
	return editor
}

func (e *Editor) Finish() ([]sourcerewrite.Edit, error) {
	if e == nil || e.builder == nil {
		return nil, fmt.Errorf("finish PHP rewrite: editor is nil")
	}
	return e.builder.Finish()
}

func (e *Editor) ReplaceRange(rng cst.TextRange, text string) error {
	if e == nil || e.builder == nil {
		return fmt.Errorf("replace PHP source range: editor is nil")
	}
	return e.builder.ReplaceRange(rng, text)
}

func (e *Editor) Insert(offset uint32, text string) error {
	if e == nil || e.builder == nil {
		return fmt.Errorf("insert PHP source: editor is nil")
	}
	return e.builder.Insert(offset, text)
}

func (e *Editor) Delete(element cst.Element) error {
	if e == nil || e.builder == nil {
		return fmt.Errorf("delete PHP element: editor is nil")
	}
	return e.builder.Delete(element)
}

// ClassReference returns the shortest unambiguous reference to className and
// schedules a use declaration when one is useful. Conflicting aliases fall
// back to a fully-qualified reference rather than changing existing imports.
func (e *Editor) ClassReference(className string) (string, error) {
	if e == nil || e.builder == nil {
		return "", fmt.Errorf("add PHP class reference: editor is nil")
	}
	className = strings.Trim(strings.TrimSpace(className), "\\")
	if className == "" {
		return "", fmt.Errorf("add PHP class reference: class name is empty")
	}
	shortName, classNamespace := splitClassName(className)
	aliasKey := strings.ToLower(shortName)
	if alias, exists := e.importAliases[strings.ToLower(className)]; exists {
		return alias, nil
	}
	if alias, exists := e.plannedAliases[strings.ToLower(className)]; exists {
		return alias, nil
	}
	if target, exists := e.imports[aliasKey]; exists {
		if strings.EqualFold(target, className) {
			return shortName, nil
		}
		return "\\" + className, nil
	}
	if target, exists := e.plannedImports[aliasKey]; exists {
		if strings.EqualFold(target, className) {
			return shortName, nil
		}
		return "\\" + className, nil
	}

	currentNamespace := ""
	if e.root != nil {
		currentNamespace = strings.Trim(phpquery.Namespace(e.root), "\\")
	}
	if strings.EqualFold(currentNamespace, classNamespace) {
		return shortName, nil
	}
	if e.localClassNameConflicts(shortName, className, currentNamespace) {
		return "\\" + className, nil
	}
	if e.root == nil {
		return "\\" + className, nil
	}

	offset := phpImportInsertionOffset(e.root)
	prefix := "\nuse "
	if !e.hasUseDeclarations && e.plannedImportCount == 0 {
		prefix = "\n\nuse "
	}
	if err := e.builder.Insert(offset, prefix+className+";"); err != nil {
		return "", err
	}
	e.plannedImports[aliasKey] = className
	e.plannedAliases[strings.ToLower(className)] = shortName
	e.plannedImportCount++
	return shortName, nil
}

func (e *Editor) localClassNameConflicts(shortName, className, namespace string) bool {
	if e.root == nil {
		return false
	}
	for _, class := range phpquery.Classes(e.root) {
		if !strings.EqualFold(phpquery.ClassName(class), shortName) {
			continue
		}
		localName := shortName
		if namespace != "" {
			localName = namespace + "\\" + shortName
		}
		if !strings.EqualFold(localName, className) {
			return true
		}
	}
	return false
}

func splitClassName(className string) (shortName, namespace string) {
	separator := strings.LastIndex(className, "\\")
	if separator < 0 {
		return className, ""
	}
	return className[separator+1:], className[:separator]
}

func phpImportInsertionOffset(root *phpsyntax.Node) uint32 {
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
	if openTag := root.FirstToken(); openTag != nil && openTag.Kind() == phpsyntax.TkOpenTag {
		return openTag.Range().End
	}
	return 0
}

func directNode(parent *phpsyntax.Node, kind phpsyntax.Kind) *phpsyntax.Node {
	if parent == nil {
		return nil
	}
	if child, ok := parent.ChildOfKind(kind).(*phpsyntax.Node); ok {
		return child
	}
	return nil
}

func lineStart(source string, offset uint32) uint32 {
	if offset > uint32(len(source)) {
		offset = uint32(len(source))
	}
	if index := strings.LastIndexByte(source[:offset], '\n'); index >= 0 {
		return uint32(index + 1)
	}
	return 0
}

func whitespacePrefix(source string, start, end uint32) (string, bool) {
	if start > end || end > uint32(len(source)) {
		return "", false
	}
	value := source[start:end]
	for _, character := range value {
		if character != ' ' && character != '\t' && character != '\r' {
			return "", false
		}
	}
	return value, true
}
