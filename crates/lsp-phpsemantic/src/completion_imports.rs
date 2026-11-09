//! Global class completion qualification and import planning.

use std::collections::HashMap;

use swlsp_lsp::CompletionRequest;
use swlsp_lsp_protocol::TextEdit;
use swlsp_parser_php::query;
use swlsp_parser_php::syntax::{PHP_NAMESPACE, TK_OPEN_TAG};
use swlsp_php_project::Model;
use swlsp_php_resolver::{ImportKind, parse_use_declaration};
use swlsp_php_semantic::{Document, Flags, Symbol};
use swlsp_syntax::{SyntaxNode, TextRange, TextSize};
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::helpers::range_from_text;

#[derive(Debug, Default)]
pub(crate) struct ClassCompletionPlan {
    pub(crate) qualifier: String,
    pub(crate) imported: bool,
    pub(crate) edit: Option<TextEdit>,
}

pub(crate) struct GlobalCompletionContext<'a> {
    request: Option<&'a CompletionRequest>,
    project: Option<&'a Model>,
    namespace: String,
    prefix: String,
    pub(crate) replacement: TextRange,
    explicitly_scoped: bool,
    imports_by_target: HashMap<String, String>,
    imports_by_alias: HashMap<String, String>,
    local_classes: HashMap<String, String>,
    import_offset: u32,
    has_imports: bool,
}

impl<'a> GlobalCompletionContext<'a> {
    pub(crate) fn new(
        request: Option<&'a CompletionRequest>,
        document: Option<&Document>,
        project: Option<&'a Model>,
        offset: u32,
    ) -> Self {
        let mut context = GlobalCompletionContext {
            request,
            project,
            namespace: String::new(),
            prefix: String::new(),
            replacement: TextRange::default(),
            explicitly_scoped: false,
            imports_by_target: HashMap::new(),
            imports_by_alias: HashMap::new(),
            local_classes: HashMap::new(),
            import_offset: 0,
            has_imports: false,
        };
        if let Some(document) = document {
            context.namespace = document.namespace.to_string();
            if let Some(scope) = document.scope_at(offset) {
                context.namespace = scope.namespace.to_string();
            }
            for symbol in &document.symbols {
                if !symbol.is_class_like() || !symbol.container.is_empty() {
                    continue;
                }
                context.local_classes.insert(
                    lowercase(&symbol.name),
                    symbol
                        .fully_qualified
                        .strip_prefix('\\')
                        .unwrap_or(&symbol.fully_qualified)
                        .to_owned(),
                );
            }
        }
        let Some(request) = request else {
            return context;
        };
        (
            context.prefix,
            context.replacement,
            context.explicitly_scoped,
        ) = completion_prefix(request.source().as_bytes(), offset);
        let Some(root) = &request.root else {
            return context;
        };
        let declarations = query::use_declarations(root);
        context.has_imports = !declarations.is_empty();
        for declaration in &declarations {
            let range = declaration.text_range();
            if u32::from(range.start()) <= offset && u32::from(range.end()) > context.import_offset
            {
                context.import_offset = range.end().into();
            }
            for imported in parse_use_declaration(&declaration.text().to_string()) {
                if imported.kind != ImportKind::Class {
                    continue;
                }
                let target = imported.target.trim_matches('\\').to_owned();
                context
                    .imports_by_target
                    .insert(lowercase(&target), imported.alias.clone());
                context
                    .imports_by_alias
                    .insert(lowercase(&imported.alias), target);
            }
        }
        if context.import_offset == 0 {
            context.import_offset = global_import_insertion_offset(root, offset);
        }
        context
    }

    pub(crate) fn class_plan(&self, symbol: &Symbol) -> ClassCompletionPlan {
        let fqn = symbol.fully_qualified.trim_matches('\\');
        let short = if symbol.name.is_empty() {
            short_name(fqn).to_owned()
        } else {
            symbol.name.to_string()
        };
        let qualified = || ClassCompletionPlan {
            qualifier: format!("\\{fqn}"),
            ..ClassCompletionPlan::default()
        };
        if self.explicitly_scoped {
            return qualified();
        }
        if let Some(alias) = self.imports_by_target.get(&lowercase(fqn))
            && !alias.is_empty()
        {
            return ClassCompletionPlan {
                qualifier: alias.clone(),
                imported: true,
                edit: None,
            };
        }
        let namespace = namespace_name(fqn);
        if eq_ignore_case(namespace, &self.namespace) || namespace.is_empty() {
            return ClassCompletionPlan {
                qualifier: short,
                ..ClassCompletionPlan::default()
            };
        }
        if let Some(target) = self.imports_by_alias.get(&lowercase(&short))
            && !eq_ignore_case(target, fqn)
        {
            return qualified();
        }
        if let Some(target) = self.local_classes.get(&lowercase(&short))
            && !eq_ignore_case(target, fqn)
        {
            return qualified();
        }
        let Some(request) = self.request else {
            return qualified();
        };
        let Some(line_index) = request.line_index() else {
            return qualified();
        };
        let range = range_from_text(
            line_index,
            TextRange::empty(TextSize::from(self.import_offset)),
        );
        let new_text = if self.has_imports {
            format!("\nuse {fqn};")
        } else {
            format!("\n\nuse {fqn};")
        };
        ClassCompletionPlan {
            qualifier: short,
            imported: false,
            edit: Some(TextEdit::new(range, new_text)),
        }
    }

    pub(crate) fn sort_text(&self, symbol: &Symbol, imported: bool) -> String {
        let name = if symbol.name.is_empty() {
            short_name(&symbol.fully_qualified)
        } else {
            &symbol.name
        };
        let matched = completion_match(&self.prefix, name, &symbol.fully_qualified);
        let symbol_namespace = namespace_name(&symbol.fully_qualified);
        let relation = if eq_ignore_case(symbol_namespace, &self.namespace) {
            0
        } else if imported {
            1
        } else if same_completion_package(
            self.project,
            &symbol.fully_qualified,
            &format!("{}\\_CurrentDocument", self.namespace),
        ) {
            2
        } else {
            3
        };
        let deprecated = u8::from(symbol.flags.has(Flags::DEPRECATED));
        format!(
            "10-{matched}-{relation}-{deprecated}-{}",
            lowercase(&symbol.fully_qualified)
        )
    }
}

pub(crate) fn completion_prefix(source: &[u8], offset: u32) -> (String, TextRange, bool) {
    let position = (offset as usize).min(source.len());
    let mut start = position;
    while start > 0 && completion_name_byte(source[start - 1]) {
        start -= 1;
    }
    let raw = String::from_utf8_lossy(&source[start..position]).into_owned();
    let explicit = raw.contains('\\');
    let mut prefix = raw.strip_prefix('\\').unwrap_or(&raw);
    if let Some(separator) = prefix.rfind('\\') {
        prefix = &prefix[separator + 1..];
    }
    (
        prefix.to_owned(),
        TextRange::new(TextSize::from(start as u32), TextSize::from(offset)),
        explicit,
    )
}

fn completion_name_byte(value: u8) -> bool {
    value == b'\\' || value == b'_' || value >= 0x80 || value.is_ascii_alphanumeric()
}

fn global_import_insertion_offset(root: &SyntaxNode, offset: u32) -> u32 {
    let mut namespace_end = 0u32;
    for namespace in query::nodes(root, &[PHP_NAMESPACE]) {
        let namespace_start: u32 = namespace.text_range().start().into();
        if namespace_start > offset {
            continue;
        }
        let text = namespace.text().to_string();
        if let Some(end) = text.find([';', '{']) {
            let candidate = namespace_start + end as u32 + 1;
            namespace_end = namespace_end.max(candidate);
        }
    }
    if namespace_end != 0 {
        return namespace_end;
    }
    match root.first_token() {
        Some(open_tag) if open_tag.kind() == TK_OPEN_TAG => open_tag.text_range().end().into(),
        _ => 0,
    }
}

fn completion_match(prefix: &str, name: &str, fully_qualified: &str) -> u8 {
    if prefix.is_empty() {
        return 3;
    }
    let prefix = lowercase(prefix);
    let name = lowercase(name);
    let fully_qualified = lowercase(
        fully_qualified
            .strip_prefix('\\')
            .unwrap_or(fully_qualified),
    );
    if prefix == name || prefix == fully_qualified {
        0
    } else if name.starts_with(&prefix) {
        1
    } else if name.contains(&prefix) || fully_qualified.contains(&prefix) {
        2
    } else {
        3
    }
}

pub(crate) fn namespace_name(name: &str) -> &str {
    let name = name.trim_matches('\\');
    name.rfind('\\').map_or("", |separator| &name[..separator])
}

pub(crate) fn short_name(name: &str) -> &str {
    let name = name.trim_matches('\\');
    name.rfind('\\')
        .map_or(name, |separator| &name[separator + 1..])
}

fn same_namespace_package(left: &str, right: &str) -> bool {
    let left = left.split('\\').next().unwrap_or("");
    let right = right.split('\\').next().unwrap_or("");
    !left.is_empty() && !right.is_empty() && eq_ignore_case(left, right)
}

pub(crate) fn same_completion_package(model: Option<&Model>, left: &str, right: &str) -> bool {
    let left_package = completion_package(model, left);
    let right_package = completion_package(model, right);
    if !left_package.is_empty() && !right_package.is_empty() {
        return eq_ignore_case(&left_package, &right_package);
    }
    same_namespace_package(namespace_name(left), namespace_name(right))
}

fn completion_package(model: Option<&Model>, fully_qualified: &str) -> String {
    let Some(model) = model else {
        return String::new();
    };
    let name = fully_qualified.trim_matches('\\');
    let mut best_length = 0;
    let mut best_package = String::new();
    for prefix in model.psr4.keys() {
        let prefix = prefix.trim_matches('\\');
        if prefix.len() > best_length && namespace_has_prefix(name, prefix) {
            best_length = prefix.len();
            best_package = "@root".to_owned();
        }
    }
    for dependency in &model.dependencies {
        for prefix in dependency.psr4.keys() {
            let prefix = prefix.trim_matches('\\');
            if prefix.len() > best_length && namespace_has_prefix(name, prefix) {
                best_length = prefix.len();
                best_package = dependency.name.clone();
            }
        }
    }
    best_package
}

fn namespace_has_prefix(name: &str, prefix: &str) -> bool {
    if prefix.is_empty() || name.len() < prefix.len() {
        return false;
    }
    let (Some(head), Some(rest)) = (name.get(..prefix.len()), name.get(prefix.len()..)) else {
        return false;
    };
    eq_ignore_case(head, prefix) && (rest.is_empty() || rest.starts_with('\\'))
}
