//! Namespace/import name resolution (port of `resolver/names.go`).

use std::collections::{HashMap, HashSet};
use std::sync::{Arc, LazyLock};

use swlsp_php_semantic::{ImportTable, Str};
use swlsp_php_types::{CallableParameter, Kind, ShapeField, Type, go_to_lower};

static EMPTY_IMPORTS: LazyLock<Arc<ImportTable>> = LazyLock::new(Default::default);
static EMPTY_ALIASES: LazyLock<Arc<HashMap<Str, Type>>> = LazyLock::new(Default::default);

/// The namespace/import environment at one source position.
///
/// Go passes this struct by value and shares its maps. Here the import table
/// and PHPDoc aliases are reference counted, so cloning a context is cheap;
/// mutation (`add_import`, `set_phpdoc_aliases`) copies on write.
#[derive(Clone, Debug)]
pub struct NameContext {
    /// Namespace without leading/trailing backslashes.
    pub namespace: Str,
    /// Class and function keys are lowercase aliases; constant keys are
    /// case-sensitive.
    pub imports: Arc<ImportTable>,
    /// Local PHPDoc type alias name to alias reference type.
    pub phpdoc_aliases: Arc<HashMap<Str, Type>>,
}

impl Default for NameContext {
    fn default() -> Self {
        NameContext {
            namespace: Str::EMPTY,
            imports: EMPTY_IMPORTS.clone(),
            phpdoc_aliases: EMPTY_ALIASES.clone(),
        }
    }
}

impl NameContext {
    pub fn new(namespace: &str) -> Self {
        NameContext {
            namespace: Str::from(namespace.trim_matches('\\')),
            ..NameContext::default()
        }
    }

    /// A context with a copy of `imports` (typically a scope's table). The
    /// namespace is used verbatim, as Go's struct literal does.
    pub fn with_imports(namespace: impl Into<Str>, imports: &ImportTable) -> Self {
        NameContext {
            namespace: namespace.into(),
            imports: Arc::new(imports.clone()),
            phpdoc_aliases: EMPTY_ALIASES.clone(),
        }
    }

    /// Mutable access to the import table (copy on write).
    pub fn imports_mut(&mut self) -> &mut ImportTable {
        Arc::make_mut(&mut self.imports)
    }

    pub fn set_phpdoc_aliases(&mut self, aliases: HashMap<Str, Type>) {
        self.phpdoc_aliases = Arc::new(aliases);
    }

    pub fn resolve_class(&self, name: &str) -> String {
        let name = name.trim();
        if name.is_empty() {
            return String::new();
        }
        if let Some(rest) = name.strip_prefix('\\') {
            return rest.to_owned();
        }
        if has_prefix_fold(name, "namespace\\") {
            return qualify(&self.namespace, &name["namespace\\".len()..]);
        }
        let (head, tail) = split_head(name);
        if !self.imports.classes.is_empty()
            && let Some(imported) = lookup_folded_import(&self.imports.classes, head)
        {
            let mut result = String::with_capacity(imported.len() + tail.len());
            result.push_str(imported);
            result.push_str(tail);
            return result;
        }
        qualify(&self.namespace, name)
    }

    pub fn resolve_function(&self, name: &str) -> Vec<String> {
        self.resolve_callable_or_constant(name, &self.imports.functions, true)
            .into_vec()
    }

    pub fn resolve_constant(&self, name: &str) -> Vec<String> {
        self.resolve_callable_or_constant(name, &self.imports.constants, false)
            .into_vec()
    }

    /// Visits resolved function candidates in PHP lookup order. Returning
    /// `false` stops; the result reports whether every candidate was visited.
    pub fn visit_function_names(&self, name: &str, visit: impl FnMut(&str) -> bool) -> bool {
        self.resolve_callable_or_constant(name, &self.imports.functions, true)
            .visit(visit)
    }

    /// Visits resolved constant candidates in PHP lookup order.
    pub fn visit_constant_names(&self, name: &str, visit: impl FnMut(&str) -> bool) -> bool {
        self.resolve_callable_or_constant(name, &self.imports.constants, false)
            .visit(visit)
    }

    fn resolve_callable_or_constant(
        &self,
        name: &str,
        imports: &HashMap<Str, Str>,
        fold_case: bool,
    ) -> ResolvedNames {
        let name = name.trim();
        if name.is_empty() {
            return ResolvedNames::None;
        }
        if let Some(rest) = name.strip_prefix('\\') {
            return ResolvedNames::One(rest.to_owned());
        }
        if has_prefix_fold(name, "namespace\\") {
            return ResolvedNames::One(qualify(&self.namespace, &name["namespace\\".len()..]));
        }
        let (head, tail) = split_head(name);
        if !imports.is_empty() {
            let imported = if fold_case {
                lookup_folded_import(imports, head)
            } else {
                imports.get(head).map(|value| value.as_str())
            };
            if let Some(imported) = imported {
                return ResolvedNames::One(format!("{imported}{tail}"));
            }
        }
        if name.contains('\\') {
            return ResolvedNames::One(qualify(&self.namespace, name));
        }
        if self.namespace.is_empty() {
            return ResolvedNames::One(name.to_owned());
        }
        // Unqualified functions and constants fall back to the global
        // namespace.
        ResolvedNames::Two(qualify(&self.namespace, name), name.to_owned())
    }

    /// Resolves every named object nested in a semantic type.
    pub fn resolve_type(&self, value: &Type) -> Type {
        self.resolve_type_inner(value, None)
    }

    /// Resolves names while preserving template identifiers from the
    /// surrounding class and function PHPDoc scopes.
    pub fn resolve_phpdoc_type<S: AsRef<str>>(&self, value: &Type, template_names: &[S]) -> Type {
        let templates: HashSet<&str> = template_names.iter().map(AsRef::as_ref).collect();
        self.resolve_type_inner(value, Some(&templates))
    }

    fn resolve_type_inner(&self, value: &Type, templates: Option<&HashSet<&str>>) -> Type {
        let args: Vec<Type> = value
            .arguments()
            .iter()
            .map(|argument| self.resolve_type_inner(argument, templates))
            .collect();
        let arg = |index: usize| args.get(index).cloned().unwrap_or_default();
        match value.kind() {
            Kind::Object => {
                let name = value.name();
                if name.is_empty() {
                    return Type::object();
                }
                let qualified = name.contains('\\');
                if !qualified && templates.is_some_and(|templates| templates.contains(name)) {
                    return Type::template(name);
                }
                if !qualified && let Some(alias) = self.phpdoc_aliases.get(name) {
                    return alias.clone();
                }
                Type::named(&self.resolve_class(name), args)
            }
            Kind::SelfType => Type::self_type(args),
            Kind::Static => Type::static_type(args),
            Kind::Parent => Type::parent_type(args),
            Kind::Array => Type::array(arg(0), arg(1)),
            Kind::NonEmptyArray => Type::non_empty_array(arg(0), arg(1)),
            Kind::List => Type::list(arg(0)),
            Kind::NonEmptyList => Type::non_empty_list(arg(0)),
            Kind::Iterable => Type::iterable(arg(0), arg(1)),
            Kind::ClassString => Type::class_string(arg(0)),
            Kind::Union => Type::union(args),
            Kind::Intersection => Type::intersection(args),
            Kind::Conditional => Type::conditional(arg(0), arg(1), arg(2), arg(3)),
            Kind::Callable => {
                let parameters = value
                    .parameters()
                    .iter()
                    .map(|parameter| CallableParameter {
                        ty: self.resolve_type_inner(&parameter.ty, templates),
                        ..parameter.clone()
                    })
                    .collect();
                Type::callable(
                    parameters,
                    self.resolve_type_inner(value.result(), templates),
                )
            }
            Kind::ArrayShape | Kind::ObjectShape => {
                let fields: Vec<ShapeField> = value
                    .fields()
                    .iter()
                    .map(|field| ShapeField {
                        ty: self.resolve_type_inner(&field.ty, templates),
                        ..field.clone()
                    })
                    .collect();
                if value.kind() == Kind::ArrayShape {
                    Type::array_shape(fields, value.is_open_shape())
                } else {
                    Type::object_shape(fields, value.is_open_shape())
                }
            }
            _ => value.clone(),
        }
    }
}

enum ResolvedNames {
    None,
    One(String),
    Two(String, String),
}

impl ResolvedNames {
    fn into_vec(self) -> Vec<String> {
        match self {
            ResolvedNames::None => Vec::new(),
            ResolvedNames::One(first) => vec![first],
            ResolvedNames::Two(first, second) => vec![first, second],
        }
    }

    fn visit(self, mut visit: impl FnMut(&str) -> bool) -> bool {
        match self {
            ResolvedNames::None => true,
            ResolvedNames::One(first) => visit(&first),
            ResolvedNames::Two(first, second) => visit(&first) && visit(&second),
        }
    }
}

pub(crate) fn has_prefix_fold(source: &str, prefix: &str) -> bool {
    source.len() >= prefix.len()
        && source.as_bytes()[..prefix.len()].eq_ignore_ascii_case(prefix.as_bytes())
}

/// Probes a lowercase-keyed import map without allocating for ordinary ASCII
/// identifiers.
fn lookup_folded_import<'m>(imports: &'m HashMap<Str, Str>, name: &str) -> Option<&'m str> {
    const MAX_INLINE: usize = 128;
    if name.len() <= MAX_INLINE && name.is_ascii() {
        let mut folded = [0u8; MAX_INLINE];
        for (target, byte) in folded.iter_mut().zip(name.bytes()) {
            *target = byte.to_ascii_lowercase();
        }
        // ASCII input stays valid UTF-8 after ASCII lowercasing.
        let key = std::str::from_utf8(&folded[..name.len()]).unwrap_or_default();
        return imports.get(key).map(|value| value.as_str());
    }
    imports
        .get(go_to_lower(name).as_str())
        .map(|value| value.as_str())
}

pub(crate) fn qualify(namespace: &str, name: &str) -> String {
    let name = name.trim_matches('\\');
    let namespace = namespace.trim_matches('\\');
    if namespace.is_empty() {
        return name.to_owned();
    }
    if name.is_empty() {
        return namespace.to_owned();
    }
    let mut result = String::with_capacity(namespace.len() + 1 + name.len());
    result.push_str(namespace);
    result.push('\\');
    result.push_str(name);
    result
}

fn split_head(name: &str) -> (&str, &str) {
    match name.find('\\') {
        Some(separator) => (&name[..separator], &name[separator..]),
        None => (name, ""),
    }
}
