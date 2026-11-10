//! Per-file usage accumulation.

use crate::*;

/// Collects symbol occurrences of one file, deduplicating identical ranges.
pub(crate) struct AdminUsageCollector<'a> {
    file_path: String,
    pub(crate) line_index: &'a LineIndex,
    /// Sets in insertion order plus a key index; the result order is
    /// irrelevant because sets are persisted by key.
    sets: Vec<AdminUsageSet>,
    positions: HashMap<String, usize>,
    seen: HashSet<String>,
}

impl<'a> AdminUsageCollector<'a> {
    pub(crate) fn new(file_path: &str, line_index: &'a LineIndex) -> Self {
        Self {
            file_path: file_path.to_owned(),
            line_index,
            sets: Vec::new(),
            positions: HashMap::new(),
            seen: HashSet::new(),
        }
    }

    pub(crate) fn add_store_declaration(&mut self, call: &SyntaxNode) {
        if let Some(literal) = jsquery::string_argument(call, 0) {
            self.add_js_string(AdminSymbolKind::Store, "", Some(&literal), true);
            return;
        }
        let id = jsquery::object_argument(call, 0)
            .and_then(|object| jsquery::property(&object, "id"))
            .and_then(|property| jsquery::property_value(&property));
        self.add_js_string(AdminSymbolKind::Store, "", id.as_ref(), true);
    }

    pub(crate) fn add_js_string(
        &mut self,
        kind: AdminSymbolKind,
        owner: &str,
        node: Option<&SyntaxNode>,
        declaration: bool,
    ) {
        let Some(node) = node.filter(|node| node.kind() == jssyntax::JS_STRING) else {
            return;
        };
        let name = jsquery::string_value(node);
        if name.is_empty() {
            return;
        }
        let Some(range) = js_string_content_range(node) else {
            return;
        };
        self.add_range(kind, owner, &name, range, declaration, false);
    }

    pub(crate) fn add_named_js_node(
        &mut self,
        kind: AdminSymbolKind,
        owner: &str,
        name: &str,
        node: Option<&SyntaxNode>,
        declaration: bool,
    ) {
        let Some(node) = node else {
            return;
        };
        if name.is_empty() {
            return;
        }
        let original = if node.kind() == jssyntax::JS_STRING {
            jsquery::string_value(node)
        } else {
            node.text_string().trim().to_owned()
        };
        let style = if kind == AdminSymbolKind::ComponentEvent
            && original != canonical_event_name(&original)
        {
            AdminNameStyle::Camel
        } else {
            AdminNameStyle::Exact
        };
        if node.kind() == jssyntax::JS_STRING {
            if let Some(range) = js_string_content_range(node) {
                self.add_styled_range(kind, owner, name, range, declaration, false, style);
            }
            return;
        }
        self.add_styled_range(
            kind,
            owner,
            name,
            node.range_trimmed_trivia(),
            declaration,
            true,
            style,
        );
    }

    pub(crate) fn add_node(
        &mut self,
        kind: AdminSymbolKind,
        owner: &str,
        name: &str,
        node: Option<&SyntaxNode>,
        declaration: bool,
    ) {
        let Some(node) = node else {
            return;
        };
        if name.is_empty() {
            return;
        }
        self.add_range(
            kind,
            owner,
            name,
            node.range_trimmed_trivia(),
            declaration,
            true,
        );
    }

    pub(crate) fn add_range(
        &mut self,
        kind: AdminSymbolKind,
        owner: &str,
        name: &str,
        range: TextRange,
        declaration: bool,
        identifier: bool,
    ) {
        self.add_styled_range(
            kind,
            owner,
            name,
            range,
            declaration,
            identifier,
            AdminNameStyle::Exact,
        );
    }

    pub(crate) fn add_styled_range(
        &mut self,
        kind: AdminSymbolKind,
        owner: &str,
        name: &str,
        range: TextRange,
        declaration: bool,
        identifier: bool,
        name_style: AdminNameStyle,
    ) {
        self.add_range_with_dynamic_selector(
            kind,
            owner,
            name,
            range,
            declaration,
            identifier,
            name_style,
            "",
            false,
        );
    }

    pub(crate) fn add_dynamic_range(
        &mut self,
        kind: AdminSymbolKind,
        name: &str,
        range: TextRange,
        selector: &str,
        router_view: bool,
    ) {
        self.add_dynamic_styled_range(
            kind,
            name,
            range,
            false,
            false,
            AdminNameStyle::Exact,
            selector,
            router_view,
        );
    }

    pub(crate) fn add_dynamic_styled_range(
        &mut self,
        kind: AdminSymbolKind,
        name: &str,
        range: TextRange,
        declaration: bool,
        identifier: bool,
        name_style: AdminNameStyle,
        selector: &str,
        router_view: bool,
    ) {
        self.add_range_with_dynamic_selector(
            kind,
            ADMIN_DYNAMIC_COMPONENT_USAGE_OWNER,
            name,
            range,
            declaration,
            identifier,
            name_style,
            selector,
            router_view,
        );
    }

    fn set_for(
        &mut self,
        kind: AdminSymbolKind,
        owner: &str,
        name: &str,
        key: String,
    ) -> &mut AdminUsageSet {
        let index = match self.positions.get(&key) {
            Some(&index) => index,
            None => {
                let index = self.sets.len();
                self.sets.push(AdminUsageSet {
                    kind,
                    owner: owner.to_owned(),
                    name: name.to_owned(),
                    file_path: self.file_path.clone(),
                    occurrences: Vec::new(),
                });
                self.positions.insert(key, index);
                index
            }
        };
        &mut self.sets[index]
    }

    pub(crate) fn add_range_with_dynamic_selector(
        &mut self,
        kind: AdminSymbolKind,
        owner: &str,
        name: &str,
        range: TextRange,
        declaration: bool,
        identifier: bool,
        name_style: AdminNameStyle,
        dynamic_selector: &str,
        dynamic_router_view: bool,
    ) {
        if name.is_empty() || range.end() <= range.start() {
            return;
        }
        let key = admin_usage_key(kind, owner, name);
        let dedup_key = format!(
            "{key}\x00{}\x00{}",
            u32::from(range.start()),
            u32::from(range.end())
        );
        if !self.seen.insert(dedup_key) {
            return;
        }
        let (start_line, start_character) = self.line_index.position_utf16(range.start().into());
        let (end_line, end_character) = self.line_index.position_utf16(range.end().into());
        self.set_for(kind, owner, name, key)
            .occurrences
            .push(AdminSourceRange {
                start_line,
                start_character,
                end_line,
                end_character,
                declaration,
                identifier,
                name_style,
                dynamic_component_selector: dynamic_selector.to_owned(),
                dynamic_router_view,
            });
    }

    pub(crate) fn add_source_range(
        &mut self,
        kind: AdminSymbolKind,
        owner: &str,
        name: &str,
        range: &AdminSourceRange,
        name_style: AdminNameStyle,
    ) {
        if name.is_empty()
            || range.end_line < range.start_line
            || (range.end_line == range.start_line && range.end_character <= range.start_character)
        {
            return;
        }
        let key = admin_usage_key(kind, owner, name);
        let dedup_key = format!(
            "{key}\x00{}:{}:{}:{}",
            range.start_line, range.start_character, range.end_line, range.end_character
        );
        if !self.seen.insert(dedup_key) {
            return;
        }
        let mut occurrence = range.clone();
        occurrence.declaration = true;
        occurrence.name_style = name_style;
        self.set_for(kind, owner, name, key)
            .occurrences
            .push(occurrence);
    }

    pub(crate) fn values(self) -> Vec<AdminUsageSet> {
        self.sets
    }
}

/// The content range of a string literal without its quotes.
pub(crate) fn js_string_content_range(node: &SyntaxNode) -> Option<TextRange> {
    let token = node
        .descendants_with_tokens()
        .filter_map(|element| element.into_token())
        .find(|token| matches!(token.kind(), jssyntax::TK_STRING | jssyntax::TK_TEMPLATE))?;
    let range = token.text_range();
    let text = token.text().as_bytes();
    if text.len() >= 2 && matches!(text[0], b'\'' | b'"' | b'`') && text[text.len() - 1] == text[0]
    {
        return Some(TextRange::new(
            range.start() + TextSize::from(1),
            range.end() - TextSize::from(1),
        ));
    }
    Some(range)
}

pub(crate) fn last_js_identifier(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.children()
        .filter(|child| child.kind() == jssyntax::JS_IDENTIFIER)
        .last()
}

pub(crate) fn range_contains(range: TextRange, offset: u32) -> bool {
    offset >= u32::from(range.start()) && offset <= u32::from(range.end())
}
