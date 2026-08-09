package admin

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

// TwigVueBindingKind describes a name introduced by Vue template syntax.
// These bindings are document-local and deliberately remain outside the
// persistent Administration symbol index.
type TwigVueBindingKind string

const (
	TwigVueBindingFor   TwigVueBindingKind = "v-for"
	TwigVueBindingEvent TwigVueBindingKind = "event"
)

// TwigVueBinding is one lexically scoped Vue template variable. DeclarationRange
// is empty for Vue's implicit $event local. DefinitionPath/DefinitionLine are
// populated when an indexed component event provides an external declaration.
type TwigVueBinding struct {
	Name             string
	Kind             TwigVueBindingKind
	Ordinal          int
	DeclarationRange cst.TextRange
	ScopeRange       cst.TextRange
	ExpressionRange  cst.TextRange
	Iterable         string
	ComponentName    string
	EventName        string
	Type             string
	DefinitionPath   string
	DefinitionLine   int
	Members          []TwigVueMember
	MembersComplete  bool
	TypeContextPath  string
}

// TwigVueMember is a direct property observed on one lexical Vue binding.
// Vue templates are frequently backed by untyped JavaScript, so the lexical
// shape remains useful even when no external TypeScript declaration exists.
// Range is the first observed occurrence and is never treated as a declaration.
type TwigVueMember struct {
	Name            string
	Type            string
	Documentation   string
	Optional        bool
	NestedMembers   []TwigVueMember
	NestedComplete  bool
	Range           cst.TextRange
	DefinitionPath  string
	DefinitionLine  int
	DefinitionRange AdminSourceRange
}

// TwigVueMemberAccess is one safe root.member access in an evaluated Vue
// expression. An empty Member is valid at a completion cursor immediately
// after the dot. Receiver segments may include statically inspectable indexed
// access; the type resolver decides whether the receiver contract makes that
// access sound.
type TwigVueMemberAccess struct {
	Root         string
	RootCalled   bool
	Member       string
	MemberCalled bool
	RootRange    cst.TextRange
	MemberRange  cst.TextRange
	Receiver     []TwigVueMemberSegment
}

// TwigVueMemberSegment is one operation between the lexical root and the
// member under the cursor. For row.product.manufacturer.name, resolving name
// yields product and manufacturer as Receiver segments. Indexed segments
// preserve their source expression so typed arrays, tuples, and Records can be
// resolved without treating arbitrary JavaScript objects as dictionaries.
type TwigVueMemberSegment struct {
	Name            string
	Range           cst.TextRange
	Called          bool
	Indexed         bool
	Optional        bool
	IndexExpression string
	IndexRange      cst.TextRange
}

// TwigVueCall identifies the innermost statically named call containing the
// cursor in an evaluated Administration template expression. Vue directive
// values are stored as lossless HTML tokens rather than Twig function nodes,
// so signature help uses this lexical representation for both directive and
// interpolation syntax.
type TwigVueCall struct {
	Name           string
	NameRange      cst.TextRange
	ActiveArgument int
	Filter         bool
}

// TwigVueCallSite is one complete statically named call in an evaluated
// Administration template expression. Argument ranges exclude surrounding
// whitespace and retain the exact source range of nested expressions.
type TwigVueCallSite struct {
	TwigVueCall
	Range     cst.TextRange
	OpenParen uint32
	Arguments []cst.TextRange
}

// ResolvedTwigVueMember joins a safe property chain with its lexical v-for or
// event binding and the structural type reached by its receiver.
type ResolvedTwigVueMember struct {
	Binding         TwigVueBinding
	Access          TwigVueMemberAccess
	Member          TwigVueMember
	MemberFound     bool
	ReceiverFound   bool
	ReceiverType    string
	ReceiverMembers []TwigVueMember
	MembersComplete bool
}

// ResolvedTwigVueInstanceMember joins a property chain rooted in an indexed
// component prop/data/computed value with the structural type reached by its
// receiver. Lexical v-for, event, and scoped-slot locals are excluded by the
// resolver so component scope never leaks through a shadowing declaration.
type ResolvedTwigVueInstanceMember struct {
	Component       VueComponent
	RootMember      VueComponentMember
	Access          TwigVueMemberAccess
	Member          TwigVueMember
	MemberFound     bool
	ReceiverFound   bool
	ReceiverType    string
	ReceiverMembers []TwigVueMember
	MembersComplete bool
}

func (resolved ResolvedTwigVueInstanceMember) QualifiedName() string {
	return resolved.Access.QualifiedName()
}

func (access TwigVueMemberAccess) MemberPath() []string {
	result := make([]string, 0, len(access.Receiver)+1)
	for _, segment := range access.Receiver {
		if segment.Indexed {
			result = append(result, "["+strings.TrimSpace(segment.IndexExpression)+"]")
			continue
		}
		result = append(result, segment.Name)
	}
	if access.Member != "" {
		result = append(result, access.Member)
	}
	return result
}

// QualifiedName renders the safe chain as it appears semantically, retaining
// call markers while MemberPath remains name-only for reference identity.
func (access TwigVueMemberAccess) QualifiedName() string {
	result := access.Root
	if access.RootCalled {
		result += "()"
	}
	for _, segment := range access.Receiver {
		if segment.Indexed {
			if segment.Optional {
				result += "?."
			}
			result += "[" + strings.TrimSpace(segment.IndexExpression) + "]"
			if segment.Called {
				result += "()"
			}
			continue
		}
		name := segment.Name
		if segment.Called {
			name += "()"
		}
		result += "." + name
	}
	if access.Member != "" {
		member := access.Member
		if access.MemberCalled {
			member += "()"
		}
		result += "." + member
	}
	return result
}

// SamePath compares the semantic member chain, including which named
// segments are invoked. Source ranges are intentionally ignored.
func (access TwigVueMemberAccess) SamePath(other TwigVueMemberAccess) bool {
	if access.Root != other.Root || access.RootCalled != other.RootCalled ||
		access.Member != other.Member ||
		access.MemberCalled != other.MemberCalled ||
		len(access.Receiver) != len(other.Receiver) {
		return false
	}
	for index := range access.Receiver {
		if access.Receiver[index].Name != other.Receiver[index].Name ||
			access.Receiver[index].Called != other.Receiver[index].Called ||
			access.Receiver[index].Indexed != other.Receiver[index].Indexed ||
			strings.TrimSpace(access.Receiver[index].IndexExpression) !=
				strings.TrimSpace(other.Receiver[index].IndexExpression) {
			return false
		}
	}
	return true
}

func (binding TwigVueBinding) IsDeclarationOffset(offset uint32) bool {
	return binding.DeclarationRange.Len() > 0 &&
		offset >= binding.DeclarationRange.Start &&
		offset <= binding.DeclarationRange.End
}

func (binding TwigVueBinding) sameIdentity(other TwigVueBinding) bool {
	return binding.Kind == other.Kind && binding.Name == other.Name &&
		binding.DeclarationRange == other.DeclarationRange &&
		binding.ScopeRange == other.ScopeRange
}

// TwigVueBindings returns all v-for declarations and implicit event locals in
// a template. Callers use the scope ranges to resolve shadowing at a cursor.
