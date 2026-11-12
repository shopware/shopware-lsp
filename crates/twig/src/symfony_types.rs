//! Types the Symfony integration knows better than the PHP declarations:
//! `app.request` and `app.user` in templates a controller renders, and the
//! variables every form view has.

use swlsp_php_semantic::{Attribute, AttributeValueKind, SymbolKind};
use swlsp_php_types::{Kind as TypeKind, ShapeField, Type};
use swlsp_syntax::SyntaxNode;
use swlsp_textutil::eq_ignore_case;

use crate::inference::{is_nullable, without_null};
use crate::php_access::PhpAccessResolver;

const APP_VARIABLE: &str = "Symfony\\Bridge\\Twig\\AppVariable";
const FORM_VIEW: &str = "Symfony\\Component\\Form\\FormView";
const USER_INTERFACE: &str = "Symfony\\Component\\Security\\Core\\User\\UserInterface";

/// The variables BaseType and FormType set on every form view. The others,
/// such as `value` or `choices`, depend on the form type.
const FORM_VARS: &[(&str, &str)] = &[
    ("id", "string"),
    ("name", "string"),
    ("full_name", "string"),
    (
        "label",
        "string|\\Symfony\\Contracts\\Translation\\TranslatableInterface|false|null",
    ),
    (
        "help",
        "string|\\Symfony\\Contracts\\Translation\\TranslatableInterface|null",
    ),
    ("attr", "array<string, scalar|\\Stringable|null>"),
    ("row_attr", "array<string, scalar|\\Stringable|null>"),
    ("label_attr", "array<string, scalar|\\Stringable|null>"),
    ("help_attr", "array<string, scalar|\\Stringable|null>"),
    ("block_prefixes", "list<string>"),
    ("translation_domain", "string|false|null"),
    (
        "errors",
        "\\Symfony\\Component\\Form\\FormErrorIterator<\\Symfony\\Component\\Form\\FormError>",
    ),
    ("required", "bool"),
    ("disabled", "bool"),
    ("compound", "bool"),
    ("valid", "bool"),
];

impl PhpAccessResolver<'_> {
    /// The `app` global when no configuration or extension declares it.
    pub(crate) fn app_variable_type(&self) -> Type {
        if self.snapshot().classes(APP_VARIABLE).is_empty() {
            Type::unknown()
        } else {
            Type::named(APP_VARIABLE, Vec::new())
        }
    }

    /// `value`, the declared type of `receiver.name`, refined by what the
    /// framework guarantees.
    pub(crate) fn framework_member_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        receiver: &Type,
        name: &str,
        value: Type,
    ) -> Type {
        let receiver = without_null(receiver);
        if receiver.kind() != TypeKind::Object {
            return value;
        }
        let class = receiver.name().trim_matches('\\');
        if name == "vars" && self.is_class(class, FORM_VIEW) {
            return form_vars_type();
        }
        if !eq_ignore_case(class, APP_VARIABLE) || !is_nullable(&value) {
            return value;
        }
        match name {
            // A controller renders within a request.
            "request" if !self.family_of(template_path, root).sources.is_empty() => {
                without_null(&value)
            }
            "user" if self.rendered_for_user(template_path, root) => {
                match self.project_user_class() {
                    Some(user) => Type::named(&user, Vec::new()),
                    None => without_null(&value),
                }
            }
            _ => value,
        }
    }

    fn is_class(&self, class: &str, target: &str) -> bool {
        eq_ignore_case(class, target) || self.snapshot().is_subtype_of(class, target)
    }

    /// Whether every render call that reaches the template sits in a
    /// controller method that requires an authenticated user.
    fn rendered_for_user(&self, template_path: &str, root: &SyntaxNode) -> bool {
        let family = self.family_of(template_path, root);
        !family.sources.is_empty()
            && !family.open
            && family
                .sources
                .iter()
                .all(|(file, offset)| self.requires_user(file, *offset))
    }

    /// Whether the method around `offset` in `file`, or its class, carries
    /// `#[IsGranted]` for a role or an authenticated user, or every route
    /// of the method requires a user by `security.access_control`.
    /// Attributes decided by a custom voter do not count: a voter may grant
    /// access without a user.
    fn requires_user(&self, file: &str, offset: u32) -> bool {
        let snapshot = self.snapshot();
        let symbols = snapshot.symbols_in(file);
        let Some(method) = symbols.iter().find(|symbol| {
            symbol.kind == SymbolKind::Method
                && u32::from(symbol.range.start()) <= offset
                && offset < u32::from(symbol.range.end())
        }) else {
            return false;
        };
        let class = snapshot.symbol(method.container.as_str());
        method
            .attributes()
            .iter()
            .chain(class.iter().flat_map(|class| class.attributes()))
            .any(grants_to_user)
            || self
                .twig
                .is_some_and(|twig| twig.routes_require_user(&method.fully_qualified))
    }

    /// The one class of the project (outside `vendor`) that implements the
    /// security user interface.
    fn project_user_class(&self) -> Option<String> {
        let snapshot = self.snapshot();
        let mut found: Option<String> = None;
        for class in snapshot.global_class_views() {
            if class.kind() != SymbolKind::Class || class.path().contains("/vendor/") {
                continue;
            }
            let name = class.fully_qualified();
            if !snapshot.is_subtype_of(name, USER_INTERFACE) {
                continue;
            }
            if found.is_some() {
                return None;
            }
            found = Some(name.to_owned());
        }
        found
    }
}

/// Whether an attribute is `#[IsGranted]` for `ROLE_*` or an
/// `IS_AUTHENTICATED*` attribute that needs a user.
fn grants_to_user(attribute: &Attribute) -> bool {
    let name = attribute.name.trim_matches('\\');
    let name = name.rsplit('\\').next().unwrap_or(name);
    if !eq_ignore_case(name, "IsGranted") {
        return false;
    }
    let Some(argument) = attribute
        .arguments
        .iter()
        .find(|argument| argument.name.is_empty() || &*argument.name == "attribute")
    else {
        return false;
    };
    if argument.value.kind != AttributeValueKind::String {
        return false;
    }
    let value: &str = &argument.value.value;
    crate::security::is_user_attribute(value.trim_matches(['\'', '"']))
}

fn form_vars_type() -> Type {
    let fields = FORM_VARS
        .iter()
        .filter_map(|(name, ty)| Some(ShapeField::new(name, swlsp_php_types::parse(ty).ok()?)))
        .collect();
    Type::array_shape(fields, true)
}
