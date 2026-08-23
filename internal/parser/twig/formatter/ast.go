// Private formatting IR node types. They are derived from the immutable Twig
// CST and rendered with one call-local renderer.
package formatter

// attribute represents an HTML attribute with key and value. Like the other
// node types it lives in a nodeList as a pointer (*attribute); its dump method
// has a pointer receiver, so a bare attribute value does not satisfy node.
type attribute struct {
	Key   string
	Value string
}

// node is the interface for nodes in the AST.
type node interface {
	dump(renderer *renderer, indent int) string
}

// nodeList is a sequence of formatting nodes. Its dump method
// arranges children with appropriate inter-node whitespace.
type nodeList []node

// twigTrim records the whitespace-control modifiers on a single Twig
// delimiter pair. A zero byte means no modifier; '-' and '~' preserve Twig's
// two whitespace-control forms verbatim.
type twigTrim struct {
	Left  byte
	Right byte
}

// rawNode holds verbatim source text — anything outside structured HTML or
// Twig syntax, plus the bytes of any Twig tag that has no registered handler.
type rawNode struct {
	Text string
	Line int
}

// commentNode represents an HTML <!-- ... --> comment.
type commentNode struct {
	Text string
	Line int
}

// templateExpressionNode represents a `{{ ... }}` Twig expression.
type templateExpressionNode struct {
	Expression string
	Trim       twigTrim
	Line       int
}

// elementNode represents an HTML element.
type elementNode struct {
	Tag         string
	Attributes  nodeList
	Children    nodeList
	SelfClosing bool
	// Unclosed reports that the element opened with `<tag>` but its
	// children parser yielded on an outer Twig terminator (e.g.
	// `{% endblock %}`) before reaching `</tag>`. The closing tag is
	// elsewhere in the source, typically wrapped in another control-flow
	// block. The formatter therefore does NOT emit `</tag>` for unclosed
	// elements — the matching `</tag>` lives as a rawNode further down.
	Unclosed bool
	Line     int
}

// twigBlockNode represents `{% block name %}...{% endblock %}`.
// OpenTrim is the trim flags on the `{% block name %}` delimiters; CloseTrim
// is the trim flags on the `{% endblock %}` delimiters.
type twigBlockNode struct {
	Name      string
	Children  nodeList
	OpenTrim  twigTrim
	CloseTrim twigTrim
	Line      int
}

// twigIfBranch is one conditional branch of a {% if %}...{% endif %} block.
// The first branch in twigIfNode.Branches is the "if" itself; subsequent
// entries are "elseif" branches. The else (no-condition) branch is held
// separately on twigIfNode.ElseChildren. Trim is the trim flags on the
// branch's own header delimiters.
type twigIfBranch struct {
	Condition string
	Body      nodeList
	Trim      twigTrim
}

// twigIfNode represents `{% if %}...{% elseif %}...{% else %}...{% endif %}`.
// Branches[0] is always the "if"; Branches[1..] are "elseif"s.
// ElseChildren is nil/empty when there is no {% else %} clause.
type twigIfNode struct {
	Branches     []twigIfBranch
	ElseChildren nodeList
	// ElseTrim is the trim flags on the `{% else %}` delimiters when the
	// else clause is present.
	ElseTrim twigTrim
	// EndTrim is the trim flags on the `{% endif %}` delimiters.
	EndTrim twigTrim
	Line    int
}

// parentNode represents `{% parent %}` or `{% parent() %}`.
type parentNode struct {
	Trim twigTrim
	Line int
}
