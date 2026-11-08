//! Compact TypeScript type index (port of Go `type_index.go`).

use std::cell::RefCell;
use std::sync::LazyLock;

use regex::Regex;

use crate::*;

pub(crate) static ADMIN_TYPE_DECLARATION_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"(?-u:\b)(interface|type)[\t\n\f\r ]+([A-Za-z_$][A-Za-z0-9_$]*)")
        .expect("valid regex")
});
static ADMIN_TYPE_IMPORT_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r#"(?ms)^[\t\n\f\r ]*import[\t\n\f\r ]+(.+?)[\t\n\f\r ]+from[\t\n\f\r ]+['"]([^'"]+)['"][\t\n\f\r ]*;?"#)
        .expect("valid regex")
});
static ADMIN_TYPE_CALL_EVENT_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r#"\([\t\n\f\r ]*[A-Za-z_$][A-Za-z0-9_$]*[\t\n\f\r ]*:[\t\n\f\r ]*['"]([^'"]+)['"]"#)
        .expect("valid regex")
});

/// The compact TypeScript type context retained for one source file.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct AdminTypeFile {
    pub file_path: String,
    pub imports: Vec<AdminTypeImport>,
    pub declarations: Vec<AdminTypeDeclaration>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct AdminTypeImport {
    pub local_name: String,
    pub imported_name: String,
    pub source: String,
}

/// A structural interface or type alias.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct AdminTypeDeclaration {
    pub name: String,
    pub file_path: String,
    pub line: u32,
    pub definition_range: AdminSourceRange,
    pub parameters: Vec<String>,
    pub members: Vec<TwigVueMember>,
    pub extends: Vec<String>,
    pub alias: String,
    pub interface: bool,
    pub open: bool,
    pub default: bool,
    pub call_events: Vec<VueComponentEvent>,
}

/// The resolved structural view of a TypeScript expression. `complete` is
/// separate from `members`: partial shapes help completion but must not
/// drive typo diagnostics.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct VueTypeShape {
    pub type_: String,
    pub members: Vec<TwigVueMember>,
    pub complete: bool,
}

fn admin_type_position(line_index: &LineIndex, offset: usize) -> u32 {
    line_index.position(offset as u32).0 + 1
}

/// Parses the structural declarations and imports of one source file.
pub(crate) fn parse_admin_type_file(
    file_path: &str,
    source: &str,
    line_index: Option<&LineIndex>,
) -> AdminTypeFile {
    let mut result = AdminTypeFile {
        file_path: file_path.to_owned(),
        imports: parse_admin_type_imports(source),
        declarations: Vec::new(),
    };
    let owned_index;
    let line_index = match line_index {
        Some(line_index) => line_index,
        None => {
            owned_index = LineIndex::new(source);
            &owned_index
        }
    };
    let bytes = source.as_bytes();
    for captures in ADMIN_TYPE_DECLARATION_PATTERN.captures_iter(source) {
        let (Some(whole), Some(kind), Some(name)) =
            (captures.get(0), captures.get(1), captures.get(2))
        else {
            continue;
        };
        if !admin_type_code_position(source, whole.start()) {
            continue;
        }
        let mut cursor = name.end();
        let (parameters, after_parameters) = parse_admin_type_parameters(source, cursor);
        cursor = after_parameters;
        let mut declaration = AdminTypeDeclaration {
            name: name.as_str().to_owned(),
            file_path: file_path.to_owned(),
            parameters,
            interface: kind.as_str() == "interface",
            ..Default::default()
        };
        declaration.line = admin_type_position(line_index, name.start());
        if declaration.interface {
            let Some(open) = index_admin_type_token(source, cursor, b'{') else {
                continue;
            };
            let header = source[cursor..open].trim();
            if let Some(extends) = header.strip_prefix("extends") {
                declaration.extends = split_admin_type_references(extends.trim(), b',');
            }
            declaration.members = admin_type_members(source, open, file_path, Some(line_index), 0);
            declaration.open = admin_object_type_has_index_signature(source, open);
            if let Some(close) = balanced_brace_end(source, open)
                && close > open
            {
                declaration.call_events =
                    admin_type_call_events(source, open, close + 1, file_path, Some(line_index));
            }
            result.declarations.push(declaration);
            continue;
        }

        let Some(equals) = index_admin_type_token(source, cursor, b'=') else {
            // Avoid treating `import { type Foo }` as a declaration.
            continue;
        };
        let end = admin_type_alias_end(source, equals + 1);
        let mut alias_start = equals + 1;
        while alias_start < end && go_byte_is_space(bytes[alias_start]) {
            alias_start += 1;
        }
        declaration.alias = source
            .get(alias_start..end)
            .unwrap_or_default()
            .trim_end()
            .to_owned();
        declaration.call_events =
            admin_type_call_events(source, equals + 1, end, file_path, Some(line_index));
        let alias = declaration.alias.clone();
        for open in admin_top_level_object_starts(&alias) {
            let members =
                admin_type_members(&alias, open, file_path, Some(line_index), alias_start);
            declaration.members = merge_twig_vue_members(&declaration.members, &members);
        }
        result.declarations.push(declaration);
    }
    result
        .declarations
        .extend(parse_shopware_utility_value_declarations(
            file_path, source, line_index,
        ));
    result
}

pub(crate) fn admin_type_call_events(
    source: &str,
    start: usize,
    end: usize,
    file_path: &str,
    line_index: Option<&LineIndex>,
) -> Vec<VueComponentEvent> {
    let mut result = Vec::new();
    if end <= start || end > source.len() {
        return result;
    }
    let Some(window) = source.get(start..end) else {
        return result;
    };
    for captures in ADMIN_TYPE_CALL_EVENT_PATTERN.captures_iter(window) {
        let (Some(whole), Some(name)) = (captures.get(0), captures.get(1)) else {
            continue;
        };
        let open = start + whole.start();
        if !admin_type_code_position(source, open) {
            continue;
        }
        let (name_start, name_end) = (start + name.start(), start + name.end());
        let Some(close) = matching_slot_delimiter(source, open, b'(', b')') else {
            continue;
        };
        if close < open || close >= end {
            continue;
        }
        let line = line_index.map_or(0, |line_index| admin_type_position(line_index, name_start));
        let name_range = match line_index {
            Some(line_index) => {
                source_range_at(line_index, name_start as u32, name_end as u32, false)
            }
            None => AdminSourceRange::default(),
        };
        let event = VueComponentEvent {
            name: name.as_str().to_owned(),
            type_: source[open..close + 1].trim().to_owned(),
            documentation: admin_type_field_documentation(source, open),
            file_path: file_path.to_owned(),
            line,
            name_range,
        };
        append_component_event(&mut result, event);
    }
    result
}

pub(crate) fn admin_type_members(
    source: &str,
    open: usize,
    file_path: &str,
    line_index: Option<&LineIndex>,
    base_offset: usize,
) -> Vec<TwigVueMember> {
    let fields = parse_type_declaration_fields(source, open);
    let mut result = Vec::with_capacity(fields.len());
    for field in &fields {
        if field.name.is_empty() {
            continue;
        }
        let mut line = 0;
        if let Some(line_index) = line_index
            && field.offset <= source.len()
        {
            line = admin_type_position(line_index, base_offset + field.offset);
        }
        let mut member = TwigVueMember {
            name: field.name.clone(),
            type_: field.value.trim().to_owned(),
            documentation: admin_type_field_documentation(source, field.offset),
            optional: field.optional,
            definition_path: file_path.to_owned(),
            definition_line: line,
            definition_range: match line_index {
                Some(line_index) => {
                    type_declaration_field_name_range(field, line_index, base_offset)
                }
                None => AdminSourceRange::default(),
            },
            ..Default::default()
        };
        if let Some(value_offset) = field.value_offset
            && member.type_.starts_with('{')
            && balanced_brace_end(&member.type_, 0) == Some(member.type_.len() - 1)
        {
            member.nested_members =
                admin_type_members(source, value_offset, file_path, line_index, base_offset);
            member.nested_complete = !admin_object_type_has_index_signature(source, value_offset);
        }
        result.push(member);
    }
    result.sort_by(|left, right| left.name.cmp(&right.name));
    result
}

pub(crate) fn admin_type_field_documentation(source: &str, offset: usize) -> String {
    let bytes = source.as_bytes();
    if offset == 0 || offset > bytes.len() {
        return String::new();
    }
    let mut end = offset;
    while end > 0 && go_byte_is_space(bytes[end - 1]) {
        end -= 1;
    }
    if end < 2 || &bytes[end - 2..end] != b"*/" {
        return String::new();
    }
    let Some(start) = source[..end - 2].rfind("/**") else {
        return String::new();
    };
    parse_javascript_documentation(&source[start..end])
}

pub(crate) fn parse_admin_type_imports(source: &str) -> Vec<AdminTypeImport> {
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    for captures in ADMIN_TYPE_IMPORT_PATTERN.captures_iter(source) {
        let (Some(whole), Some(specifier), Some(path)) =
            (captures.get(0), captures.get(1), captures.get(2))
        else {
            continue;
        };
        if !admin_type_code_position(source, whole.start()) {
            continue;
        }
        let path = path.as_str().trim();
        if path.is_empty() {
            continue;
        }
        let specifier = specifier.as_str().trim();
        let mut specifier = specifier.strip_prefix("type ").unwrap_or(specifier).trim();
        if let Some(open) = specifier.find('{') {
            if let Some(close) = matching_slot_delimiter(specifier, open, b'{', b'}')
                && close > open
            {
                for entry in split_slot_top_level(&specifier[open + 1..close], b',') {
                    let entry = entry.trim();
                    let entry = entry.strip_prefix("type ").unwrap_or(entry).trim();
                    let parts: Vec<&str> = entry.split_whitespace().collect();
                    if parts.is_empty() {
                        continue;
                    }
                    let imported = parts[0];
                    let mut local = parts[0];
                    if parts.len() >= 3 && parts[parts.len() - 2] == "as" {
                        local = parts[parts.len() - 1];
                    }
                    append_admin_type_import(&mut result, &mut seen, local, imported, path);
                }
            }
            specifier = specifier[..open].trim();
            specifier = specifier.strip_suffix(',').unwrap_or(specifier);
        }
        if specifier.starts_with('*') {
            let parts: Vec<&str> = specifier.split_whitespace().collect();
            if parts.len() >= 3 && parts[parts.len() - 2] == "as" {
                append_admin_type_import(&mut result, &mut seen, parts[parts.len() - 1], "*", path);
            }
            continue;
        }
        let default_name = specifier.trim();
        if !default_name.is_empty() {
            append_admin_type_import(&mut result, &mut seen, default_name, "default", path);
        }
    }
    result
}

pub(crate) fn append_admin_type_import(
    result: &mut Vec<AdminTypeImport>,
    seen: &mut HashSet<String>,
    local: &str,
    imported: &str,
    source: &str,
) {
    if local.is_empty() || source.is_empty() {
        return;
    }
    if !seen.insert(format!("{local}\x00{source}")) {
        return;
    }
    result.push(AdminTypeImport {
        local_name: local.to_owned(),
        imported_name: imported.to_owned(),
        source: source.to_owned(),
    });
}

/// Go `parseAdminTypeParameters`: `(parameters, cursor after them)`.
pub(crate) fn parse_admin_type_parameters(source: &str, cursor: usize) -> (Vec<String>, usize) {
    let bytes = source.as_bytes();
    let mut cursor = cursor;
    while cursor < bytes.len() && go_byte_is_space(bytes[cursor]) {
        cursor += 1;
    }
    if cursor >= bytes.len() || bytes[cursor] != b'<' {
        return (Vec::new(), cursor);
    }
    let Some(close) = matching_slot_delimiter(source, cursor, b'<', b'>') else {
        return (Vec::new(), cursor);
    };
    let mut result = Vec::new();
    for parameter in split_slot_top_level(&source[cursor + 1..close], b',') {
        if let Some(name) = parameter.split_whitespace().next()
            && is_admin_type_identifier(name)
        {
            result.push(name.to_owned());
        }
    }
    (result, close + 1)
}

pub(crate) fn index_admin_type_token(source: &str, cursor: usize, token: u8) -> Option<usize> {
    let bytes = source.as_bytes();
    let mut state = DeclarationScanState::default();
    let mut index = cursor;
    while index < bytes.len() {
        state.consume(source.as_bytes(), &mut index);
        if !state.in_literal_or_comment() {
            if bytes[index] == token {
                return Some(index);
            }
            if bytes[index] == b';' || bytes[index] == b'\n' && token == b'=' {
                return None;
            }
        }
        index += 1;
    }
    None
}

pub(crate) fn admin_type_alias_end(source: &str, start: usize) -> usize {
    let bytes = source.as_bytes();
    let mut state = DeclarationScanState::default();
    let mut angles = 0usize;
    let mut index = start;
    while index < bytes.len() {
        state.consume(source.as_bytes(), &mut index);
        if !state.in_literal_or_comment() {
            match bytes[index] {
                b'{' => state.braces += 1,
                b'}' => {
                    if state.braces > 0 {
                        state.braces -= 1;
                    }
                }
                b'(' => state.parens += 1,
                b')' => {
                    if state.parens > 0 {
                        state.parens -= 1;
                    }
                }
                b'[' => state.brackets += 1,
                b']' => {
                    if state.brackets > 0 {
                        state.brackets -= 1;
                    }
                }
                b'<' => angles += 1,
                b'>' => angles = angles.saturating_sub(1),
                b';' if state.braces == 0
                    && state.parens == 0
                    && state.brackets == 0
                    && angles == 0 =>
                {
                    return index;
                }
                _ => {}
            }
        }
        index += 1;
    }
    bytes.len()
}

pub(crate) fn admin_top_level_object_starts(value: &str) -> Vec<usize> {
    let bytes = value.as_bytes();
    let mut result = Vec::new();
    let mut state = DeclarationScanState::default();
    let mut angles = 0usize;
    let mut index = 0;
    while index < bytes.len() {
        state.consume(value.as_bytes(), &mut index);
        if !state.in_literal_or_comment() {
            match bytes[index] {
                b'<' => angles += 1,
                b'>' => angles = angles.saturating_sub(1),
                b'{' => {
                    if state.braces == 0 && state.parens == 0 && state.brackets == 0 && angles == 0
                    {
                        result.push(index);
                        if let Some(close) = balanced_brace_end(value, index)
                            && close > index
                        {
                            index = close;
                        }
                    }
                }
                b'(' => state.parens += 1,
                b')' => {
                    if state.parens > 0 {
                        state.parens -= 1;
                    }
                }
                b'[' => state.brackets += 1,
                b']' if state.brackets > 0 => state.brackets -= 1,
                _ => {}
            }
        }
        index += 1;
    }
    result
}

pub(crate) fn split_admin_type_references(value: &str, separator: u8) -> Vec<String> {
    split_admin_type_top_level(value, separator)
        .into_iter()
        .map(|entry| entry.trim().to_owned())
        .filter(|entry| !entry.is_empty())
        .collect()
}

pub(crate) fn admin_type_code_position(source: &str, position: usize) -> bool {
    let mut state = DeclarationScanState::default();
    let mut index = 0;
    while index < position && index < source.len() {
        state.consume(source.as_bytes(), &mut index);
        index += 1;
    }
    !state.in_literal_or_comment()
}

pub(crate) fn is_admin_type_identifier(value: &str) -> bool {
    let bytes = value.as_bytes();
    if bytes.is_empty() || !is_slot_identifier_start(bytes[0]) {
        return false;
    }
    bytes[1..]
        .iter()
        .all(|&byte| is_slot_identifier_continue(byte))
}

pub(crate) fn admin_type_import_candidates(file_path: &str, import_path: &str) -> Vec<String> {
    if import_path.is_empty()
        || !import_path.starts_with("./")
            && !import_path.starts_with("../")
            && !import_path.starts_with("src/")
    {
        return Vec::new();
    }
    let base = if import_path.starts_with("src/") {
        let normalized = filepath::to_slash(file_path);
        let marker = "/Resources/app/administration/";
        let Some(position) = normalized.find(marker) else {
            return Vec::new();
        };
        filepath::join(&[&normalized[..position + marker.len()], import_path])
    } else {
        filepath::join(&[&filepath::dir(file_path), import_path])
    };
    let base = filepath::clean(&base);
    if base.ends_with(".ts")
        || base.ends_with(".d.ts")
        || base.ends_with(".js")
        || base.ends_with(".vue")
    {
        return vec![base];
    }
    vec![
        format!("{base}.ts"),
        format!("{base}.d.ts"),
        format!("{base}.js"),
        format!("{base}.vue"),
        filepath::join(&[&base, "index.ts"]),
        filepath::join(&[&base, "index.d.ts"]),
        filepath::join(&[&base, "index.js"]),
        filepath::join(&[&base, "index.vue"]),
    ]
}

/// Seen-set shared by one recursive type resolution.
pub(crate) type TypeSeen = RefCell<HashSet<String>>;

/// Removes a key from the seen set when dropped (Go `defer delete`).
pub(crate) struct SeenGuard<'a> {
    seen: &'a TypeSeen,
    key: String,
}

impl<'a> SeenGuard<'a> {
    /// `None` when `key` is already being resolved.
    pub(crate) fn enter(seen: &'a TypeSeen, key: String) -> Option<Self> {
        if !seen.borrow_mut().insert(key.clone()) {
            return None;
        }
        Some(Self { seen, key })
    }
}

impl Drop for SeenGuard<'_> {
    fn drop(&mut self) {
        self.seen.borrow_mut().remove(&self.key);
    }
}

impl AdminComponentIndexer {
    /// Resolves a structural TypeScript expression in the lexical import
    /// context of `context_path`.
    pub fn resolve_vue_type(
        &self,
        type_expression: &str,
        context_path: &str,
        live_files: &[AdminTypeFile],
    ) -> IndexResult<VueTypeShape> {
        let overlays = self.live_type_file_overlays(live_files);
        let seen = TypeSeen::default();
        self.resolve_vue_type_in(
            type_expression.trim(),
            context_path,
            &seen,
            &HashMap::new(),
            &overlays,
        )
    }

    /// Resolves callable `defineEmits` signatures from local or imported
    /// declarations.
    pub fn resolve_vue_events(
        &self,
        type_expression: &str,
        context_path: &str,
        live_files: &[AdminTypeFile],
    ) -> IndexResult<Vec<VueComponentEvent>> {
        let overlays = self.live_type_file_overlays(live_files);
        let seen = TypeSeen::default();
        self.resolve_vue_events_impl(
            type_expression.trim(),
            context_path,
            &seen,
            &HashMap::new(),
            &overlays,
        )
    }
}

#[allow(dead_code)]
pub(crate) fn admin_type_file_overlays(files: &[AdminTypeFile]) -> HashMap<String, AdminTypeFile> {
    let mut result = HashMap::with_capacity(files.len());
    for file in files {
        if file.file_path.is_empty() {
            continue;
        }
        result.insert(filepath::clean(&file.file_path), file.clone());
    }
    result
}

impl AdminComponentIndexer {
    pub(crate) fn resolve_vue_events_impl(
        &self,
        type_expression: &str,
        context_path: &str,
        seen: &TypeSeen,
        substitutions: &HashMap<String, String>,
        live_files: &HashMap<String, AdminTypeFile>,
    ) -> IndexResult<Vec<VueComponentEvent>> {
        let substituted = substitute_admin_type(type_expression, substitutions);
        let value = trim_admin_type_parentheses(substituted.trim());
        if value.is_empty() {
            return Ok(Vec::new());
        }
        for wrapper in ["Readonly", "Partial", "Required", "NonNullable"] {
            if let Some(inner) = admin_type_generic_inner(value, wrapper) {
                return self.resolve_vue_events_impl(
                    &inner,
                    context_path,
                    seen,
                    substitutions,
                    live_files,
                );
            }
        }
        for separator in *b"|&" {
            let branches = split_admin_type_top_level(value, separator);
            if branches.len() > 1 {
                let mut result = Vec::new();
                for branch in &branches {
                    for event in self.resolve_vue_events_impl(
                        branch,
                        context_path,
                        seen,
                        substitutions,
                        live_files,
                    )? {
                        append_component_event(&mut result, event);
                    }
                }
                return Ok(result);
            }
        }
        let (name, arguments) = parse_admin_named_type(value);
        if name.is_empty() {
            return Ok(Vec::new());
        }
        let key = format!(
            "{}\x00events\x00{}\x00{}",
            filepath::clean(context_path),
            name,
            arguments.join("\x00")
        );
        let Some(_guard) = SeenGuard::enter(seen, key) else {
            return Ok(Vec::new());
        };
        let Some((declaration, declaration_context)) =
            self.resolve_admin_type_declaration(context_path, &name, live_files)?
        else {
            return Ok(Vec::new());
        };
        let mut local_substitutions = substitutions.clone();
        for (parameter_index, parameter) in declaration.parameters.iter().enumerate() {
            if let Some(argument) = arguments.get(parameter_index) {
                local_substitutions.insert(
                    parameter.clone(),
                    substitute_admin_type(argument, substitutions),
                );
            }
        }
        let mut result = declaration.call_events.clone();
        for event in &mut result {
            event.type_ = substitute_admin_type(&event.type_, &local_substitutions);
        }
        for parent in &declaration.extends {
            for event in self.resolve_vue_events_impl(
                parent,
                &declaration_context,
                seen,
                &local_substitutions,
                live_files,
            )? {
                append_component_event(&mut result, event);
            }
        }
        if !declaration.alias.is_empty() {
            for event in self.resolve_vue_events_impl(
                &declaration.alias,
                &declaration_context,
                seen,
                &local_substitutions,
                live_files,
            )? {
                append_component_event(&mut result, event);
            }
        }
        Ok(result)
    }

    /// Go `resolveVueType` with a caller-owned seen set.
    pub(crate) fn resolve_vue_type_impl(
        &self,
        type_expression: &str,
        context_path: &str,
        seen: &mut HashSet<String>,
        substitutions: &HashMap<String, String>,
        live_files: &HashMap<String, AdminTypeFile>,
    ) -> IndexResult<VueTypeShape> {
        let shared = TypeSeen::new(std::mem::take(seen));
        let result = self.resolve_vue_type_in(
            type_expression,
            context_path,
            &shared,
            substitutions,
            live_files,
        );
        *seen = shared.into_inner();
        result
    }

    pub(crate) fn resolve_vue_type_in(
        &self,
        type_expression: &str,
        context_path: &str,
        seen: &TypeSeen,
        substitutions: &HashMap<String, String>,
        live_files: &HashMap<String, AdminTypeFile>,
    ) -> IndexResult<VueTypeShape> {
        let resolver = VueTypeResolver {
            index: self,
            context_path: context_path.to_owned(),
            seen,
            substitutions: substitutions.clone(),
            live_files,
        };
        resolver.resolve(type_expression)
    }
}

fn member(name: &str, type_: &str) -> TwigVueMember {
    TwigVueMember {
        name: name.to_owned(),
        type_: type_.to_owned(),
        ..Default::default()
    }
}

fn first_argument_or(arguments: &[String], index: usize, fallback: &str) -> String {
    match arguments.get(index) {
        Some(argument) if !argument.trim().is_empty() => argument.trim().to_owned(),
        _ => fallback.to_owned(),
    }
}

pub(crate) fn admin_javascript_builtin_shape(
    type_expression: &str,
    name: &str,
    arguments: &[String],
) -> Option<VueTypeShape> {
    let t = type_expression;
    let members = match name {
        "string" | "String" => vec![
            member("length", "number"),
            member("charAt", "(index: number) => string"),
            member("endsWith", "(search: string) => boolean"),
            member("includes", "(search: string) => boolean"),
            member(
                "replace",
                "(search: string | RegExp, replacement: string) => string",
            ),
            member("slice", "(start?: number, end?: number) => string"),
            member("split", "(separator: string | RegExp) => string[]"),
            member("startsWith", "(search: string) => boolean"),
            member("substring", "(start: number, end?: number) => string"),
            member("toLowerCase", "() => string"),
            member("toUpperCase", "() => string"),
            member("trim", "() => string"),
        ],
        "number" | "Number" => vec![
            member("toFixed", "(digits?: number) => string"),
            member("toLocaleString", "() => string"),
            member("toString", "(radix?: number) => string"),
            member("valueOf", "() => number"),
        ],
        "boolean" | "Boolean" => vec![
            member("toString", "() => string"),
            member("valueOf", "() => boolean"),
        ],
        "Function" => vec![
            member("apply", "(thisArg: unknown, args?: unknown[]) => unknown"),
            member("bind", "(thisArg: unknown, ...args: unknown[]) => Function"),
            member("call", "(thisArg: unknown, ...args: unknown[]) => unknown"),
            member("length", "number"),
            member("name", "string"),
            member("toString", "() => string"),
        ],
        "Array" | "ReadonlyArray" => {
            let element = if arguments.len() == 1 && !arguments[0].trim().is_empty() {
                arguments[0].trim().to_owned()
            } else {
                "unknown".to_owned()
            };
            vec![
                member("at", &format!("(index: number) => {element} | undefined")),
                member("filter", &format!("(predicate: Function) => {t}")),
                member(
                    "find",
                    &format!("(predicate: Function) => {element} | undefined"),
                ),
                member("forEach", "(callback: Function) => void"),
                member("includes", &format!("(value: {element}) => boolean")),
                member("join", "(separator?: string) => string"),
                member("length", "number"),
                member("map", "(callback: Function) => Array<unknown>"),
                member("slice", &format!("(start?: number, end?: number) => {t}")),
            ]
        }
        // Record keys are dynamic: no safe named completion members.
        "Record" => Vec::new(),
        "Map" | "ReadonlyMap" => {
            let key = first_argument_or(arguments, 0, "unknown");
            let value = first_argument_or(arguments, 1, "unknown");
            vec![
                member("clear", "() => void"),
                member("delete", &format!("(key: {key}) => boolean")),
                member("get", &format!("(key: {key}) => {value} | undefined")),
                member("has", &format!("(key: {key}) => boolean")),
                member("set", &format!("(key: {key}, value: {value}) => {t}")),
                member("size", "number"),
            ]
        }
        "Emitter" => {
            let events = first_argument_or(arguments, 0, "Record<string | symbol, unknown>");
            vec![
                member("all", &format!("Map<keyof {events}, Function[]>")),
                member(
                    "emit",
                    &format!("(type: keyof {events}, event?: unknown) => void"),
                ),
                member(
                    "off",
                    &format!("(type: keyof {events}, handler?: Function) => void"),
                ),
                member(
                    "on",
                    &format!("(type: keyof {events}, handler: Function) => void"),
                ),
            ]
        }
        "Promise" | "PromiseLike" => {
            let result = if arguments.len() == 1 && !arguments[0].trim().is_empty() {
                arguments[0].trim().to_owned()
            } else {
                "unknown".to_owned()
            };
            vec![
                member(
                    "then",
                    &format!("(onFulfilled: (value: {result}) => unknown) => Promise<unknown>"),
                ),
                member(
                    "catch",
                    &format!("(onRejected: Function) => Promise<{result}>"),
                ),
                member(
                    "finally",
                    &format!("(onFinally: Function) => Promise<{result}>"),
                ),
            ]
        }
        _ => return None,
    };
    // Built-ins have a broader version-dependent surface: never complete.
    Some(VueTypeShape {
        type_: type_expression.to_owned(),
        members,
        complete: false,
    })
}

fn short_type_name(name: &str) -> &str {
    name.rsplit_once('.').map_or(name, |(_, short)| short)
}

pub(crate) fn admin_named_string_generic(
    name: &str,
    arguments: &[String],
    wanted: &str,
) -> Option<String> {
    if arguments.len() != 1 || short_type_name(name) != wanted {
        return None;
    }
    let value = admin_type_string_literal(&arguments[0]);
    (!value.is_empty()).then_some(value)
}

pub(crate) fn admin_repository_shape(type_expression: &str, entity_name: &str) -> VueTypeShape {
    let entity = format!("Entity<'{entity_name}'>");
    let collection = format!("EntityCollection<'{entity_name}'>");
    VueTypeShape {
        type_: type_expression.to_owned(),
        members: vec![
            member(
                "assign",
                "(id: string, context?: apiContext) => Promise<unknown>",
            ),
            member(
                "clone",
                "(entityId: string, behavior: object, context?: apiContext) => Promise<unknown>",
            ),
            member(
                "create",
                &format!("(context?: apiContext, id?: string | null) => {entity}"),
            ),
            member(
                "createVersion",
                "(entityId: string, context?: apiContext, versionId?: string | null, versionName?: string | null) => Promise<apiContext>",
            ),
            member(
                "delete",
                "(id: string, context?: apiContext) => Promise<unknown>",
            ),
            member(
                "deleteVersion",
                "(entityId: string, versionId: string, context?: apiContext) => Promise<unknown>",
            ),
            member("discard", &format!("(entity: {entity}) => void")),
            member("entityName", &format!("'{entity_name}'")),
            member(
                "get",
                &format!(
                    "(id: string, context?: apiContext, criteria?: Criteria | null) => Promise<{entity} | null>"
                ),
            ),
            member("hasChanges", &format!("(entity: {entity}) => boolean")),
            member(
                "iterateIds",
                "(criteria: Criteria, callback: Function, context?: apiContext) => Promise<unknown>",
            ),
            member(
                "mergeVersion",
                "(versionId: string, context?: apiContext) => Promise<unknown>",
            ),
            member("route", "string"),
            member("schema", "EntityDefinition"),
            member(
                "save",
                &format!("(entity: {entity}, context?: apiContext) => Promise<void | unknown>"),
            ),
            member(
                "saveAll",
                &format!("(entities: {collection}, context?: apiContext) => Promise<unknown>"),
            ),
            member(
                "search",
                &format!("(criteria: Criteria, context?: apiContext) => Promise<{collection}>"),
            ),
            member(
                "searchIds",
                "(criteria: Criteria, context?: apiContext) => Promise<IdSearchResult>",
            ),
            member(
                "sync",
                &format!("(entities: {collection}, context?: apiContext) => Promise<unknown>"),
            ),
            member(
                "syncDeleted",
                "(ids: string[], context?: apiContext) => Promise<void>",
            ),
        ],
        // Repository decorators can add runtime methods.
        complete: false,
    }
}

impl AdminComponentIndexer {
    pub(crate) fn resolve_admin_store_type(
        &self,
        type_expression: &str,
        store_name: &str,
    ) -> IndexResult<VueTypeShape> {
        let mut result = VueTypeShape {
            type_: type_expression.to_owned(),
            ..Default::default()
        };
        for store in self.get_store(store_name)? {
            for store_member in &store.members {
                let addition = [TwigVueMember {
                    name: store_member.name.clone(),
                    type_: store_member.type_.clone(),
                    definition_path: store_member.file_path.clone(),
                    definition_line: store_member.line,
                    ..Default::default()
                }];
                result.members = merge_twig_vue_members(&result.members, &addition);
            }
        }
        // Pinia adds plugin/runtime members and permits store augmentation.
        result.complete = false;
        Ok(result)
    }
}

/// Go `adminEntityGeneric`: `(entityName, collection)`.
pub(crate) fn admin_entity_generic(name: &str, arguments: &[String]) -> Option<(String, bool)> {
    if arguments.len() != 1 {
        return None;
    }
    let collection = match short_type_name(name) {
        "Entity" => false,
        "EntityCollection" => true,
        _ => return None,
    };
    let entity_name = admin_type_string_literal(&arguments[0]);
    if entity_name.is_empty() {
        return None;
    }
    Some((entity_name, collection))
}

pub(crate) fn admin_type_string_literal(value: &str) -> String {
    let value = value.trim();
    let bytes = value.as_bytes();
    if bytes.len() < 2
        || bytes[0] != bytes[bytes.len() - 1]
        || !matches!(bytes[0], b'\'' | b'"' | b'`')
    {
        return String::new();
    }
    value[1..value.len() - 1].trim().to_owned()
}

impl AdminComponentIndexer {
    /// Maps `Entity<'technical_name'>` to the generated EntitySchema
    /// interface, merged across augmenting files.
    pub(crate) fn resolve_admin_entity_type(
        &self,
        type_expression: &str,
        entity_name: &str,
        context_path: &str,
        seen: &TypeSeen,
        live_files: &HashMap<String, AdminTypeFile>,
    ) -> IndexResult<VueTypeShape> {
        let mut result = VueTypeShape {
            type_: type_expression.to_owned(),
            ..Default::default()
        };
        if entity_name.is_empty() {
            return Ok(result);
        }
        let Some(_guard) = SeenGuard::enter(seen, format!("entity\x00{entity_name}")) else {
            return Ok(result);
        };
        let files = self.all_admin_type_files(live_files)?;
        let mut found = false;
        let mut complete = true;
        for file in &files {
            for declaration in &file.declarations {
                if declaration.name != entity_name || !declaration.interface {
                    continue;
                }
                found = true;
                result.members = merge_twig_vue_members(&result.members, &declaration.members);
                for parent in &declaration.extends {
                    let shape = self.resolve_vue_type_in(
                        parent,
                        &file.file_path,
                        seen,
                        &HashMap::new(),
                        live_files,
                    )?;
                    result.members = merge_twig_vue_members(&shape.members, &result.members);
                    complete = complete && shape.complete;
                }
            }
        }
        if !found {
            return Ok(result);
        }
        result.members = merge_twig_vue_members(
            &result.members,
            &admin_entity_runtime_members(type_expression, context_path),
        );
        result.complete = complete;
        Ok(result)
    }
}

pub(crate) fn admin_entity_runtime_members(
    type_expression: &str,
    context_path: &str,
) -> Vec<TwigVueMember> {
    let t = type_expression;
    [
        ("_draft", t.to_owned()),
        ("_entityName", "string".to_owned()),
        ("_isDirty", "boolean".to_owned()),
        ("_isNew", "boolean".to_owned()),
        ("_origin", t.to_owned()),
        ("__identifier__", "() => string".to_owned()),
        ("getDraft", format!("() => {t}")),
        ("getEntityName", "() => string".to_owned()),
        ("getIsDirty", "() => boolean".to_owned()),
        ("getOrigin", format!("() => {t}")),
        ("isNew", "() => boolean".to_owned()),
        ("markAsNew", "() => void".to_owned()),
    ]
    .into_iter()
    .map(|(name, type_)| TwigVueMember {
        name: name.to_owned(),
        type_,
        definition_path: context_path.to_owned(),
        ..Default::default()
    })
    .collect()
}

pub(crate) fn admin_entity_collection_shape(
    type_expression: &str,
    entity_name: &str,
) -> VueTypeShape {
    let entity = format!("Entity<'{entity_name}'>");
    let array_type = format!("Array<{entity}>");
    let array_shape =
        admin_javascript_builtin_shape(&array_type, "Array", std::slice::from_ref(&entity))
            .unwrap_or_default();
    let additions = vec![
        member("__identifier__", "() => string"),
        member("add", &format!("(entity: {entity}) => void")),
        member(
            "addAt",
            &format!("(entity: {entity}, index: number) => void"),
        ),
        member("aggregations", "Record<string, unknown> | null"),
        member("context", "Record<string, unknown>"),
        member("criteria", "Criteria | null"),
        member("entity", &format!("'{entity_name}'")),
        member("first", &format!("() => {entity} | null")),
        member("get", &format!("(id: string) => {entity} | null")),
        member("getAt", &format!("(index: number) => {entity} | null")),
        member("getIds", "() => string[]"),
        member("has", "(id: string) => boolean"),
        member("last", &format!("() => {entity} | null")),
        member("length", "number"),
        member(
            "moveItem",
            &format!("(from: number, to: number) => {entity} | null"),
        ),
        member("remove", "(id: string) => boolean"),
        member("source", "string"),
        member("total", "number | null"),
    ];
    VueTypeShape {
        type_: type_expression.to_owned(),
        members: merge_twig_vue_members(&array_shape.members, &additions),
        // EntityCollection extends Array, whose full surface is not listed.
        complete: false,
    }
}

impl AdminComponentIndexer {
    /// Go `(declaration, declarationContext, found, error)`.
    pub(crate) fn resolve_admin_type_declaration(
        &self,
        context_path: &str,
        name: &str,
        live_files: &HashMap<String, AdminTypeFile>,
    ) -> IndexResult<Option<(AdminTypeDeclaration, String)>> {
        if let Some(type_file) = self.admin_type_file_for_resolution(context_path, live_files)? {
            if let Some(declaration) = admin_type_declaration_named(&type_file, name) {
                return Ok(Some((declaration.clone(), context_path.to_owned())));
            }
            for type_import in &type_file.imports {
                if type_import.local_name != name {
                    continue;
                }
                for candidate in admin_type_import_candidates(context_path, &type_import.source) {
                    let Some(imported_file) =
                        self.admin_type_file_for_resolution(&candidate, live_files)?
                    else {
                        continue;
                    };
                    let mut imported_name = type_import.imported_name.as_str();
                    if imported_name == "default" {
                        if let Some(declaration) = admin_type_default_declaration(&imported_file) {
                            return Ok(Some((declaration.clone(), candidate)));
                        }
                        imported_name = name;
                    }
                    if let Some(declaration) =
                        admin_type_declaration_named(&imported_file, imported_name)
                    {
                        return Ok(Some((declaration.clone(), candidate)));
                    }
                }
            }
        }

        // Ambient/global declarations: resolve only a unique declaration.
        let files = self.all_admin_type_files(live_files)?;
        let mut matched: Option<(AdminTypeDeclaration, String)> = None;
        for candidate in &files {
            if let Some(declaration) = admin_type_declaration_named(candidate, name) {
                if matched.is_some() {
                    return Ok(None);
                }
                matched = Some((declaration.clone(), candidate.file_path.clone()));
            }
        }
        Ok(matched)
    }

    #[cfg(test)]
    pub(crate) fn admin_type_file(&self, file_path: &str) -> IndexResult<Option<AdminTypeFile>> {
        Ok(self
            .admin_type_file_ref(file_path)?
            .map(|file| file.clone()))
    }

    /// The persisted type file of `file_path` without copying it.
    pub(crate) fn admin_type_file_ref(
        &self,
        file_path: &str,
    ) -> IndexResult<Option<AdminTypeFileRef<'static>>> {
        if file_path.is_empty() {
            return Ok(None);
        }
        let values = self
            .type_index
            .get_values_by_path_view(&filepath::clean(file_path))?;
        Ok((!values.is_empty()).then_some(AdminTypeFileRef::Stored(values)))
    }

    pub(crate) fn admin_type_file_for_resolution<'a>(
        &self,
        file_path: &str,
        live_files: &'a HashMap<String, AdminTypeFile>,
    ) -> IndexResult<Option<AdminTypeFileRef<'a>>> {
        let cleaned = filepath::clean(file_path);
        if let Some(file) = live_files.get(&cleaned) {
            return Ok(Some(AdminTypeFileRef::Live(file)));
        }
        self.admin_type_file_ref(&cleaned)
    }

    /// Every persisted type file shadowed by open-document overlays, without
    /// copying the persisted catalog.
    pub(crate) fn all_admin_type_files<'a>(
        &self,
        live_files: &'a HashMap<String, AdminTypeFile>,
    ) -> IndexResult<AdminTypeFiles<'a>> {
        let stored = self.type_index.get_all_values_view()?;
        if live_files.is_empty() {
            return Ok(AdminTypeFiles {
                stored,
                shadowed: Vec::new(),
                overlays: Vec::new(),
            });
        }
        let mut shadowed = vec![false; stored.len()];
        let mut seen = HashSet::with_capacity(stored.len());
        for (index, file) in stored.iter().enumerate() {
            let path = filepath::clean(&file.file_path);
            if live_files.contains_key(&path) {
                shadowed[index] = true;
                continue;
            }
            seen.insert(path);
        }
        // Go iterates the overlay map in random order; sort for determinism.
        let mut overlays: Vec<(&String, &'a AdminTypeFile)> = live_files
            .iter()
            .filter(|(path, _)| !seen.contains(*path))
            .collect();
        overlays.sort_by(|left, right| left.0.cmp(right.0));
        Ok(AdminTypeFiles {
            stored,
            shadowed,
            overlays: overlays.into_iter().map(|(_, file)| file).collect(),
        })
    }
}

/// One type file borrowed from an overlay or shared from the repository
/// cache.
pub(crate) enum AdminTypeFileRef<'a> {
    Live(&'a AdminTypeFile),
    /// A non-empty repository view; the first value is the file.
    Stored(std::sync::Arc<[AdminTypeFile]>),
}

impl std::ops::Deref for AdminTypeFileRef<'_> {
    type Target = AdminTypeFile;

    fn deref(&self) -> &AdminTypeFile {
        match self {
            Self::Live(file) => file,
            Self::Stored(values) => &values[0],
        }
    }
}

/// The persisted type catalog followed by unshadowed overlays.
pub(crate) struct AdminTypeFiles<'a> {
    stored: std::sync::Arc<[AdminTypeFile]>,
    /// Per stored file: replaced by an overlay. Empty means none.
    shadowed: Vec<bool>,
    overlays: Vec<&'a AdminTypeFile>,
}

impl AdminTypeFiles<'_> {
    pub(crate) fn iter(&self) -> impl Iterator<Item = &AdminTypeFile> {
        self.stored
            .iter()
            .enumerate()
            .filter(|(index, _)| !self.shadowed.get(*index).copied().unwrap_or(false))
            .map(|(_, file)| file)
            .chain(self.overlays.iter().copied())
    }
}

impl<'s> IntoIterator for &'s AdminTypeFiles<'_> {
    type Item = &'s AdminTypeFile;
    type IntoIter = Box<dyn Iterator<Item = &'s AdminTypeFile> + 's>;

    fn into_iter(self) -> Self::IntoIter {
        Box::new(self.iter())
    }
}

pub(crate) fn admin_type_declaration_named<'a>(
    file: &'a AdminTypeFile,
    name: &str,
) -> Option<&'a AdminTypeDeclaration> {
    file.declarations
        .iter()
        .find(|declaration| declaration.name == name)
}

pub(crate) fn admin_type_default_declaration(
    file: &AdminTypeFile,
) -> Option<&AdminTypeDeclaration> {
    file.declarations
        .iter()
        .find(|declaration| declaration.default)
}

/// Go `parseAdminNamedType`: `(name, arguments)`; empty name when `value`
/// is not a (generic) named type.
pub(crate) fn parse_admin_named_type(value: &str) -> (String, Vec<String>) {
    let value = value.trim();
    if value.is_empty() {
        return (String::new(), Vec::new());
    }
    let bytes = value.as_bytes();
    let mut end = 0;
    while end < bytes.len() && (is_slot_identifier_continue(bytes[end]) || bytes[end] == b'.') {
        end += 1;
    }
    if end == bytes.len() && is_admin_type_qualified_identifier(value) {
        return (value.to_owned(), Vec::new());
    }
    if end == 0 || end >= bytes.len() || bytes[end] != b'<' {
        return (String::new(), Vec::new());
    }
    if matching_slot_delimiter(value, end, b'<', b'>') != Some(bytes.len() - 1) {
        return (String::new(), Vec::new());
    }
    (
        value[..end].to_owned(),
        split_admin_type_top_level(&value[end + 1..bytes.len() - 1], b','),
    )
}

pub(crate) fn is_admin_type_qualified_identifier(value: &str) -> bool {
    !value.is_empty() && value.split('.').all(is_admin_type_identifier)
}

pub(crate) fn admin_type_generic_inner(value: &str, wrapper: &str) -> Option<String> {
    if !value.starts_with(wrapper) || !value[wrapper.len()..].starts_with('<') {
        return None;
    }
    let open = wrapper.len();
    if matching_slot_delimiter(value, open, b'<', b'>') != Some(value.len() - 1) {
        return None;
    }
    Some(value[open + 1..value.len() - 1].trim().to_owned())
}

pub(crate) fn trim_admin_type_parentheses(value: &str) -> &str {
    let mut value = value;
    while value.len() >= 2
        && value.as_bytes()[0] == b'('
        && matching_slot_delimiter(value, 0, b'(', b')') == Some(value.len() - 1)
    {
        value = value[1..value.len() - 1].trim();
    }
    value
}

/// Splits `value` at top-level `separator` bytes, ignoring literals,
/// comments and nested brackets. Always returns at least one entry.
pub(crate) fn split_admin_type_top_level(value: &str, separator: u8) -> Vec<String> {
    let bytes = value.as_bytes();
    let mut result = Vec::new();
    let mut start = 0;
    let mut state = DeclarationScanState::default();
    let mut angles = 0usize;
    let mut index = 0;
    while index < bytes.len() {
        state.consume(value.as_bytes(), &mut index);
        if !state.in_literal_or_comment() {
            match bytes[index] {
                b'{' => state.braces += 1,
                b'}' => {
                    if state.braces > 0 {
                        state.braces -= 1;
                    }
                }
                b'(' => state.parens += 1,
                b')' => {
                    if state.parens > 0 {
                        state.parens -= 1;
                    }
                }
                b'[' => state.brackets += 1,
                b']' => {
                    if state.brackets > 0 {
                        state.brackets -= 1;
                    }
                }
                b'<' => angles += 1,
                b'>' => angles = angles.saturating_sub(1),
                _ => {}
            }
            if bytes[index] == separator
                && state.braces == 0
                && state.parens == 0
                && state.brackets == 0
                && angles == 0
            {
                result.push(go_byte_slice(value, start, index).trim().to_owned());
                start = index + 1;
            }
        }
        index += 1;
    }
    result.push(go_byte_slice(value, start, bytes.len()).trim().to_owned());
    result
}

/// A byte-range slice that tolerates non-boundary offsets (only possible for
/// non-ASCII separators, which callers never use).
fn go_byte_slice(value: &str, start: usize, end: usize) -> String {
    match value.get(start..end) {
        Some(slice) => slice.to_owned(),
        None => String::from_utf8_lossy(&value.as_bytes()[start..end]).into_owned(),
    }
}

pub(crate) fn substitute_admin_type(
    value: &str,
    substitutions: &HashMap<String, String>,
) -> String {
    if substitutions.is_empty() || value.is_empty() {
        return value.to_owned();
    }
    let bytes = value.as_bytes();
    let mut builder = String::with_capacity(value.len());
    let mut index = 0;
    let mut literal_start = 0;
    while index < bytes.len() {
        if !is_slot_identifier_start(bytes[index]) {
            index += 1;
            continue;
        }
        builder.push_str(&value[literal_start..index]);
        let mut end = index + 1;
        while end < bytes.len() && is_slot_identifier_continue(bytes[end]) {
            end += 1;
        }
        let name = &value[index..end];
        match substitutions.get(name) {
            Some(replacement) if !replacement.is_empty() => builder.push_str(replacement),
            _ => builder.push_str(name),
        }
        index = end;
        literal_start = end;
    }
    builder.push_str(&value[literal_start..]);
    builder
}

pub(crate) fn substitute_vue_type_members(
    members: &[TwigVueMember],
    substitutions: &HashMap<String, String>,
) -> Vec<TwigVueMember> {
    members
        .iter()
        .map(|member| {
            let mut member = member.clone();
            member.type_ = substitute_admin_type(&member.type_, substitutions);
            member.nested_members =
                substitute_vue_type_members(&member.nested_members, substitutions);
            member
        })
        .collect()
}

pub(crate) fn union_vue_type_members(shapes: &[VueTypeShape]) -> Vec<TwigVueMember> {
    let mut result: Vec<TwigVueMember> = Vec::new();
    let mut positions: HashMap<String, usize> = HashMap::new();
    let mut presence: HashMap<String, usize> = HashMap::new();
    for shape in shapes {
        let mut seen = HashSet::with_capacity(shape.members.len());
        for member in &shape.members {
            if member.name.is_empty() || !seen.insert(member.name.as_str()) {
                continue;
            }
            *presence.entry(member.name.clone()).or_default() += 1;
            if let Some(&position) = positions.get(&member.name) {
                let existing = &mut result[position];
                if existing.documentation.is_empty() {
                    existing.documentation = member.documentation.clone();
                }
                existing.type_ = merge_vue_types(&[&existing.type_, &member.type_]);
                existing.optional = existing.optional || member.optional;
                continue;
            }
            positions.insert(member.name.clone(), result.len());
            result.push(member.clone());
        }
    }
    for member in &mut result {
        if presence.get(&member.name).copied().unwrap_or_default() < shapes.len() {
            member.type_ = merge_vue_types(&[&member.type_, "undefined"]);
            member.optional = true;
        }
    }
    result.sort_by(|left, right| left.name.cmp(&right.name));
    result
}

/// Unresolved, nullish and top types never provide a closed object shape.
pub(crate) fn admin_primitive_type(_value: &str) -> bool {
    false
}

pub(crate) fn admin_object_type_has_index_signature(value: &str, open: usize) -> bool {
    let bytes = value.as_bytes();
    if open >= bytes.len() || bytes[open] != b'{' {
        return false;
    }
    let Some(close) = balanced_brace_end(value, open) else {
        return false;
    };
    if close <= open {
        return false;
    }
    let mut state = SlotScanState::default();
    for index in open + 1..close {
        if state.top_level()
            && bytes[index] == b'['
            && let Some(bracket_close) = matching_slot_delimiter(value, index, b'[', b']')
            && bracket_close > index
            && bracket_close < close
        {
            let mut cursor = bracket_close + 1;
            while cursor < close && is_slot_space(bytes[cursor]) {
                cursor += 1;
            }
            if cursor < close && bytes[cursor] == b':' {
                return true;
            }
        }
        state.consume(bytes[index]);
    }
    false
}
