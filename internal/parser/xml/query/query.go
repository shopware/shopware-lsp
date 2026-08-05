package query

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
)

func Nodes(root *syntax.Node, kinds ...syntax.Kind) []*syntax.Node {
	if root == nil {
		return nil
	}
	accepted := make(map[syntax.Kind]struct{}, len(kinds))
	for _, kind := range kinds {
		accepted[kind] = struct{}{}
	}

	var result []*syntax.Node
	var visit func(*syntax.Node)
	visit = func(node *syntax.Node) {
		if _, ok := accepted[node.Kind()]; ok {
			result = append(result, node)
		}
		for index := 0; index < node.ChildCount(); index++ {
			child, ok := node.Child(index).(*syntax.Node)
			if ok {
				visit(child)
			}
		}
	}
	visit(root)
	return result
}

func Elements(root *syntax.Node, names ...string) []*syntax.Node {
	if root == nil {
		return nil
	}
	accepted := make(map[string]struct{}, len(names))
	for _, name := range names {
		accepted[name] = struct{}{}
	}

	var result []*syntax.Node
	var visit func(*syntax.Node)
	visit = func(node *syntax.Node) {
		if node.Kind() == syntax.XmlElement {
			if len(accepted) == 0 {
				result = append(result, node)
			} else if _, ok := accepted[ElementName(node)]; ok {
				result = append(result, node)
			}
		}
		for index := 0; index < node.ChildCount(); index++ {
			child, ok := node.Child(index).(*syntax.Node)
			if ok {
				visit(child)
			}
		}
	}
	visit(root)
	return result
}

func ElementAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(node, syntax.XmlElement)
}

func ParentElement(node *syntax.Node) *syntax.Node {
	element := ElementAt(node)
	if element == nil {
		return nil
	}
	for parent := element.Parent(); parent != nil; parent = parent.Parent() {
		if parent.Kind() == syntax.XmlElement {
			return parent
		}
	}
	return nil
}

func ElementName(node *syntax.Node) string {
	element := normalizeElement(node)
	if element == nil {
		return ""
	}
	tag := startTag(element)
	if tag == nil {
		return ""
	}
	name := directChildNode(tag, syntax.XmlName)
	if name == nil {
		return ""
	}
	token := name.ChildTokenOfKind(syntax.TkName)
	if token == nil {
		return ""
	}
	return token.Text()
}

func ChildElements(node *syntax.Node, names ...string) []*syntax.Node {
	element := normalizeElement(node)
	if element == nil {
		return nil
	}
	content := directChildNode(element, syntax.XmlContent)
	if content == nil {
		return nil
	}

	accepted := make(map[string]struct{}, len(names))
	for _, name := range names {
		accepted[name] = struct{}{}
	}

	var result []*syntax.Node
	for index := 0; index < content.ChildCount(); index++ {
		child, ok := content.Child(index).(*syntax.Node)
		if !ok {
			continue
		}
		if child.Kind() != syntax.XmlElement {
			continue
		}
		if len(accepted) == 0 {
			result = append(result, child)
			continue
		}
		if _, ok := accepted[ElementName(child)]; ok {
			result = append(result, child)
		}
	}
	return result
}

func ChildElement(node *syntax.Node, names ...string) *syntax.Node {
	children := ChildElements(node, names...)
	if len(children) == 0 {
		return nil
	}
	return children[0]
}

func Attributes(node *syntax.Node) []*syntax.Node {
	element := normalizeElement(node)
	if element == nil {
		return nil
	}
	tag := startTag(element)
	if tag == nil {
		return nil
	}

	var result []*syntax.Node
	for index := 0; index < tag.ChildCount(); index++ {
		child, ok := tag.Child(index).(*syntax.Node)
		if ok && child.Kind() == syntax.XmlAttribute {
			result = append(result, child)
		}
	}
	return result
}

func Attribute(node *syntax.Node, name string) *syntax.Node {
	for _, attribute := range Attributes(node) {
		if AttributeName(attribute) == name {
			return attribute
		}
	}
	return nil
}

func AttributeAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(node, syntax.XmlAttribute)
}

func AttributeName(node *syntax.Node) string {
	attribute := ancestorOrSelf(node, syntax.XmlAttribute)
	if attribute == nil {
		return ""
	}
	name := directChildNode(attribute, syntax.XmlName)
	if name == nil {
		return ""
	}
	token := name.ChildTokenOfKind(syntax.TkName)
	if token == nil {
		return ""
	}
	return token.Text()
}

func AttributeValue(node *syntax.Node) string {
	attribute := ancestorOrSelf(node, syntax.XmlAttribute)
	if attribute == nil {
		attribute = Attribute(normalizeElement(node), AttributeName(node))
	}
	if attribute == nil {
		return ""
	}
	value := directChildNode(attribute, syntax.XmlAttributeValue)
	if value == nil {
		return ""
	}
	token := value.FirstToken()
	if token == nil {
		return ""
	}
	return trimAttributeQuotes(token.Text())
}

func AttributeValues(node *syntax.Node) map[string]string {
	result := make(map[string]string)
	for _, attribute := range Attributes(node) {
		name := AttributeName(attribute)
		if name != "" {
			result[name] = AttributeValue(attribute)
		}
	}
	return result
}

func TextAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(node, syntax.XmlText)
}

// TextContent returns the direct character content of an element. Nested child
// elements are intentionally excluded, matching the XML value semantics needed
// by manifests, parameters, and system-config fields.
func TextContent(node *syntax.Node) string {
	element := normalizeElement(node)
	if element == nil {
		return ""
	}
	content := directChildNode(element, syntax.XmlContent)
	if content == nil {
		return ""
	}

	var value strings.Builder
	for index := 0; index < content.ChildCount(); index++ {
		child, ok := content.Child(index).(*syntax.Node)
		if !ok {
			continue
		}
		switch child.Kind() {
		case syntax.XmlText, syntax.XmlEntityReference:
			value.WriteString(child.Text())
		case syntax.XmlCdata:
			text := child.Text()
			text = strings.TrimPrefix(text, "<![CDATA[")
			text = strings.TrimSuffix(text, "]]>")
			value.WriteString(text)
		}
	}
	return value.String()
}

func NodeValue(node *syntax.Node) string {
	if attribute := AttributeAt(node); attribute != nil {
		return AttributeValue(attribute)
	}
	if text := TextAt(node); text != nil {
		return text.Text()
	}
	if element := ElementAt(node); element != nil {
		return TextContent(element)
	}
	return ""
}

func normalizeElement(node *syntax.Node) *syntax.Node {
	if node == nil {
		return nil
	}
	if element := ElementAt(node); element != nil {
		return element
	}
	if node.Kind() == syntax.XmlDocument {
		elements := Elements(node)
		if len(elements) > 0 {
			return elements[0]
		}
	}
	return nil
}

func startTag(element *syntax.Node) *syntax.Node {
	if tag := directChildNode(element, syntax.XmlStartTag); tag != nil {
		return tag
	}
	return directChildNode(element, syntax.XmlEmptyElementTag)
}

func directChildNode(node *syntax.Node, kind syntax.Kind) *syntax.Node {
	if node == nil {
		return nil
	}
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*syntax.Node)
		if ok && child.Kind() == kind {
			return child
		}
	}
	return nil
}

func ancestorOrSelf(node *syntax.Node, kind syntax.Kind) *syntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == kind {
			return current
		}
	}
	return nil
}

func trimAttributeQuotes(text string) string {
	if len(text) > 0 && (text[0] == '"' || text[0] == '\'') {
		quote := text[0]
		text = text[1:]
		if len(text) > 0 && text[len(text)-1] == quote {
			text = text[:len(text)-1]
		}
	}
	return text
}
