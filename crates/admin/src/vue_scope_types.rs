//! Document-local Vue template scope types.

use crate::*;

string_enum! {
    /// A name introduced by Vue template syntax.
    pub enum TwigVueBindingKind {
        For => "v-for",
        Event => "event",
    }
}

/// One lexically scoped Vue template variable. `declaration_range` is empty
/// for Vue's implicit `$event` local.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TwigVueBinding {
    pub name: String,
    pub kind: TwigVueBindingKind,
    pub ordinal: usize,
    pub declaration_range: TextRange,
    pub scope_range: TextRange,
    pub expression_range: TextRange,
    pub iterable: String,
    pub component_name: String,
    pub event_name: String,
    pub type_: String,
    pub definition_path: String,
    pub definition_line: u32,
    pub members: Vec<TwigVueMember>,
    pub members_complete: bool,
    pub type_context_path: String,
}

/// A direct property observed on one lexical binding or declared by a type.
/// `range` is the first observed occurrence, never a declaration.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct TwigVueMember {
    pub name: String,
    #[serde(rename = "type")]
    pub type_: String,
    pub documentation: String,
    pub optional: bool,
    pub nested_members: Vec<TwigVueMember>,
    pub nested_complete: bool,
    pub range: TextRange,
    pub definition_path: String,
    pub definition_line: u32,
    pub definition_range: AdminSourceRange,
}

/// One safe `root.member` access in an evaluated Vue expression.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TwigVueMemberAccess {
    pub root: String,
    pub root_called: bool,
    pub member: String,
    pub member_called: bool,
    pub root_range: TextRange,
    pub member_range: TextRange,
    pub receiver: Vec<TwigVueMemberSegment>,
}

/// One operation between the lexical root and the member under the cursor.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TwigVueMemberSegment {
    pub name: String,
    pub range: TextRange,
    pub called: bool,
    pub indexed: bool,
    pub optional: bool,
    pub index_expression: String,
    pub index_range: TextRange,
}

/// The innermost statically named call containing the cursor.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TwigVueCall {
    pub name: String,
    pub name_range: TextRange,
    pub active_argument: usize,
    pub filter: bool,
}

/// One complete statically named call and its source range.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TwigVueCallSite {
    pub call: TwigVueCall,
    pub range: TextRange,
    pub open_paren: u32,
    pub arguments: Vec<TextRange>,
}

/// A safe property chain joined with its lexical binding and receiver type.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ResolvedTwigVueMember {
    pub binding: TwigVueBinding,
    pub access: TwigVueMemberAccess,
    pub member: TwigVueMember,
    pub member_found: bool,
    pub receiver_found: bool,
    pub receiver_type: String,
    pub receiver_members: Vec<TwigVueMember>,
    pub members_complete: bool,
}

/// A property chain rooted in an indexed component member.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct ResolvedTwigVueInstanceMember {
    pub component: VueComponent,
    pub root_member: VueComponentMember,
    pub access: TwigVueMemberAccess,
    pub member: TwigVueMember,
    pub member_found: bool,
    pub receiver_found: bool,
    pub receiver_type: String,
    pub receiver_members: Vec<TwigVueMember>,
    pub members_complete: bool,
}

impl ResolvedTwigVueInstanceMember {
    pub fn qualified_name(&self) -> String {
        self.access.qualified_name()
    }
}

impl TwigVueMemberAccess {
    pub fn member_path(&self) -> Vec<String> {
        let mut result = Vec::with_capacity(self.receiver.len() + 1);
        for segment in &self.receiver {
            if segment.indexed {
                result.push(format!("[{}]", segment.index_expression.trim()));
                continue;
            }
            result.push(segment.name.clone());
        }
        if !self.member.is_empty() {
            result.push(self.member.clone());
        }
        result
    }

    /// The chain as it appears semantically, retaining call markers.
    pub fn qualified_name(&self) -> String {
        let mut result = self.root.clone();
        if self.root_called {
            result.push_str("()");
        }
        for segment in &self.receiver {
            if segment.indexed {
                if segment.optional {
                    result.push_str("?.");
                }
                result.push('[');
                result.push_str(segment.index_expression.trim());
                result.push(']');
                if segment.called {
                    result.push_str("()");
                }
                continue;
            }
            result.push('.');
            result.push_str(&segment.name);
            if segment.called {
                result.push_str("()");
            }
        }
        if !self.member.is_empty() {
            result.push('.');
            result.push_str(&self.member);
            if self.member_called {
                result.push_str("()");
            }
        }
        result
    }

    /// Compares the semantic member chain; source ranges are ignored.
    pub fn same_path(&self, other: &TwigVueMemberAccess) -> bool {
        if self.root != other.root
            || self.root_called != other.root_called
            || self.member != other.member
            || self.member_called != other.member_called
            || self.receiver.len() != other.receiver.len()
        {
            return false;
        }
        self.receiver
            .iter()
            .zip(&other.receiver)
            .all(|(left, right)| {
                left.name == right.name
                    && left.called == right.called
                    && left.indexed == right.indexed
                    && left.index_expression.trim() == right.index_expression.trim()
            })
    }
}

impl TwigVueBinding {
    pub fn is_declaration_offset(&self, offset: u32) -> bool {
        !self.declaration_range.is_empty()
            && offset >= u32::from(self.declaration_range.start())
            && offset <= u32::from(self.declaration_range.end())
    }

    pub(crate) fn same_identity(&self, other: &TwigVueBinding) -> bool {
        self.kind == other.kind
            && self.name == other.name
            && self.declaration_range == other.declaration_range
            && self.scope_range == other.scope_range
    }
}
