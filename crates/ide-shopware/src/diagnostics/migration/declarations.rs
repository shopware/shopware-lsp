//! Interface-to-abstract-class, added parameter and native type migrations.

use super::scan;
use swlsp_lsp::{Context, Problem};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_CLASS_DECLARATION, PHP_EXTENDS_CLAUSE, PHP_IMPLEMENTS_CLAUSE, PHP_NAME, PHP_PARAMETER,
};
use swlsp_php::NameResolver;
use swlsp_php_semantic::Snapshot;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::eq_ignore_case;

use super::api::{Since, since};
use super::{
    ShopwareMigrationAnalyzer, ShopwareMigrationPayload, migration_problem, node_text,
    php_class_is_subtype, php_own_method_for_migration,
};

const INTERFACE_ABSTRACT_CLASS_CODE: &str = "shopware.migration.declaration.interface_to_abstract";
const ADD_METHOD_PARAMETER_CODE: &str = "shopware.migration.declaration.parameter.add";
const NATIVE_TYPE_MIGRATION_CODE: &str = "shopware.migration.declaration.type";

#[derive(Clone, Copy, Debug)]
pub(crate) struct InterfaceAbstractClassMigration {
    pub since: Since,
    pub interface_name: &'static str,
    pub abstract_class: &'static str,
}

#[derive(Clone, Copy, Debug)]
pub(crate) struct MethodParameterMigration {
    pub since: Since,
    pub class: &'static str,
    pub method: &'static str,
    pub position: i64,
    pub name: &'static str,
    pub type_text: &'static str,
}

#[derive(Clone, Copy, Debug)]
pub(crate) struct MethodTypeMigration {
    pub since: Since,
    pub class: &'static str,
    pub method: &'static str,
    pub position: i64,
    pub type_text: &'static str,
}

const fn interface(
    since: Since,
    interface_name: &'static str,
    abstract_class: &'static str,
) -> InterfaceAbstractClassMigration {
    InterfaceAbstractClassMigration {
        since,
        interface_name,
        abstract_class,
    }
}

const fn typed(
    since: Since,
    class: &'static str,
    method: &'static str,
    position: i64,
    type_text: &'static str,
) -> MethodTypeMigration {
    MethodTypeMigration {
        since,
        class,
        method,
        position,
        type_text,
    }
}

pub(crate) const INTERFACE_ABSTRACT_CLASS_MIGRATIONS: &[InterfaceAbstractClassMigration] = &[
    interface(
        since(5, 0),
        "Shopware\\Core\\Checkout\\Cart\\CartPersisterInterface",
        "Shopware\\Core\\Checkout\\Cart\\AbstractCartPersister",
    ),
    interface(
        since(5, 0),
        "Shopware\\Core\\Content\\Sitemap\\Provider\\UrlProviderInterface",
        "Shopware\\Core\\Content\\Sitemap\\Provider\\AbstractUrlProvider",
    ),
    interface(
        since(5, 0),
        "Shopware\\Core\\System\\Snippet\\Files\\SnippetFileInterface",
        "Shopware\\Core\\System\\Snippet\\Files\\GenericSnippetFile",
    ),
    interface(
        since(8, 0),
        "Shopware\\Core\\Content\\ProductStream\\Service\\ProductStreamBuilderInterface",
        "Shopware\\Core\\Content\\ProductStream\\Service\\AbstractProductStreamBuilder",
    ),
];

pub(crate) const ADDED_METHOD_PARAMETER_MIGRATIONS: &[MethodParameterMigration] = &[
    MethodParameterMigration {
        since: since(5, 0),
        class: "Shopware\\Storefront\\Framework\\Captcha\\AbstractCaptcha",
        method: "supports",
        position: 1,
        name: "captchaConfig",
        type_text: "array",
    },
    MethodParameterMigration {
        since: since(5, 0),
        class: "Shopware\\Storefront\\Framework\\Cache\\ReverseProxy\\AbstractReverseProxyGateway",
        method: "tag",
        position: 2,
        name: "response",
        type_text: "\\Symfony\\Component\\HttpFoundation\\Response",
    },
];

pub(crate) const PARAMETER_TYPE_MIGRATIONS: &[MethodTypeMigration] = &[
    typed(
        since(5, 0),
        "Shopware\\Core\\Framework\\DataAbstractionLayer\\Indexing\\EntityIndexer",
        "iterate",
        0,
        "?array",
    ),
    typed(
        since(5, 0),
        "Shopware\\Storefront\\Theme\\DataAbstractionLayer\\ThemeIndexer",
        "iterate",
        0,
        "?array",
    ),
    typed(
        since(5, 0),
        "Shopware\\Core\\Content\\Flow\\Indexing\\FlowIndexer",
        "iterate",
        0,
        "?array",
    ),
    typed(
        since(5, 0),
        "Shopware\\Core\\Content\\Media\\DataAbstractionLayer\\MediaFolderConfigurationIndexer",
        "iterate",
        0,
        "?array",
    ),
    typed(
        since(5, 0),
        "Shopware\\Core\\Content\\Media\\DataAbstractionLayer\\MediaFolderIndexer",
        "iterate",
        0,
        "?array",
    ),
    typed(
        since(5, 0),
        "Shopware\\Core\\Content\\Media\\DataAbstractionLayer\\MediaIndexer",
        "iterate",
        0,
        "?array",
    ),
    typed(
        since(5, 0),
        "Shopware\\Core\\Content\\LandingPage\\DataAbstractionLayer\\LandingPageIndexer",
        "iterate",
        0,
        "?array",
    ),
    typed(
        since(5, 0),
        "Shopware\\Core\\Content\\ProductStream\\DataAbstractionLayer\\ProductStreamIndexer",
        "iterate",
        0,
        "?array",
    ),
    typed(
        since(5, 0),
        "Shopware\\Core\\Content\\Rule\\DataAbstractionLayer\\RuleIndexer",
        "iterate",
        0,
        "?array",
    ),
    typed(
        since(5, 0),
        "Shopware\\Storefront\\Page\\Product\\Review\\ReviewLoaderResult",
        "setMatrix",
        0,
        "\\Shopware\\Core\\Content\\Product\\SalesChannel\\Review\\RatingMatrix",
    ),
];

pub(crate) const RETURN_TYPE_MIGRATIONS: &[MethodTypeMigration] = &[
    typed(
        since(5, 0),
        "Shopware\\Core\\Framework\\Adapter\\Twig\\TemplateIterator",
        "getIterator",
        -1,
        "\\Traversable",
    ),
    typed(
        since(5, 0),
        "Shopware\\Core\\Content\\Cms\\DataResolver\\CriteriaCollection",
        "getIterator",
        -1,
        "\\Traversable",
    ),
    typed(
        since(5, 0),
        "Shopware\\Core\\Checkout\\Cart\\CartBehavior",
        "hasPermission",
        -1,
        "bool",
    ),
    typed(
        since(5, 0),
        "Shopware\\Storefront\\Page\\Product\\Review\\ReviewLoaderResult",
        "getMatrix",
        -1,
        "\\Shopware\\Core\\Content\\Product\\SalesChannel\\Review\\RatingMatrix",
    ),
    typed(
        since(7, 0),
        "Shopware\\Elasticsearch\\Framework\\AbstractElasticsearchDefinition",
        "buildTermQuery",
        -1,
        "\\OpenSearchDSL\\BuilderInterface",
    ),
];

impl ShopwareMigrationAnalyzer {
    pub(super) fn declaration_migration_problems(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        snapshot: Option<&Snapshot>,
    ) -> Vec<Problem> {
        let resolver = scan::resolver(root);
        let mut result = Vec::new();
        for class in scan::classes(root) {
            if ctx.is_cancelled() {
                return result;
            }
            result.extend(self.interface_abstract_class_problems(&class, &resolver));
            result.extend(self.added_method_parameter_problems(&class, root, snapshot));
            result.extend(self.native_type_problems(&class, root, snapshot, &resolver));
        }
        for parameter in phpquery::nodes(root, &[PHP_PARAMETER]) {
            if phpquery::class_at(&parameter).is_some() {
                continue;
            }
            result.extend(self.interface_type_problem(
                &parameter,
                &resolver,
                "interface-parameter",
            ));
        }
        result
    }

    fn interface_abstract_class_problems(
        &self,
        class: &SyntaxNode,
        resolver: &NameResolver,
    ) -> Vec<Problem> {
        let mut result = Vec::new();
        if let Some(implements) = phpquery::direct_child(class, PHP_IMPLEMENTS_CLAUSE) {
            for name in implements
                .children()
                .filter(|child| child.kind() == PHP_NAME)
            {
                let name_text = node_text(&name);
                let resolved = resolver.resolve(name_text.trim());
                let resolved = resolved.trim_matches('\\');
                for rule in INTERFACE_ABSTRACT_CLASS_MIGRATIONS {
                    if !rule.since.active(self) || !eq_ignore_case(resolved, rule.interface_name) {
                        continue;
                    }
                    let safe = class_can_adopt_parent(class, resolver, rule.abstract_class);
                    result.push(declaration_migration_problem(
                        INTERFACE_ABSTRACT_CLASS_CODE,
                        class,
                        format!(
                            "Shopware {}: extend {} instead of implementing {}",
                            rule.since.label(),
                            rule.abstract_class,
                            rule.interface_name
                        ),
                        "interface-class",
                        name_text.trim().to_owned(),
                        format!("\\{}", rule.abstract_class),
                        0,
                        safe,
                    ));
                }
            }
        }
        for parameter in phpquery::nodes(class, &[PHP_PARAMETER]) {
            if phpquery::class_at(&parameter).as_ref() != Some(class) {
                continue;
            }
            result.extend(self.interface_type_problem(&parameter, resolver, "interface-parameter"));
        }
        for property in phpquery::properties(class) {
            result.extend(self.interface_type_problem(&property, resolver, "interface-property"));
        }
        result
    }

    fn interface_type_problem(
        &self,
        declaration: &SyntaxNode,
        resolver: &NameResolver,
        kind: &str,
    ) -> Option<Problem> {
        let type_text = if kind == "interface-parameter" {
            phpquery::parameter_type(declaration)
        } else {
            phpquery::property_type(declaration)
        };
        if type_text.is_empty() || type_text.contains(['|', '&', '?']) {
            return None;
        }
        let resolved = resolver.resolve(&type_text);
        let resolved = resolved.trim_matches('\\');
        INTERFACE_ABSTRACT_CLASS_MIGRATIONS
            .iter()
            .find(|rule| rule.since.active(self) && eq_ignore_case(resolved, rule.interface_name))
            .map(|rule| {
                declaration_migration_problem(
                    INTERFACE_ABSTRACT_CLASS_CODE,
                    declaration,
                    format!(
                        "Shopware {}: replace {} type with {}",
                        rule.since.label(),
                        rule.interface_name,
                        rule.abstract_class
                    ),
                    kind,
                    type_text.clone(),
                    format!("\\{}", rule.abstract_class),
                    0,
                    true,
                )
            })
    }

    fn added_method_parameter_problems(
        &self,
        class: &SyntaxNode,
        root: &SyntaxNode,
        snapshot: Option<&Snapshot>,
    ) -> Vec<Problem> {
        let mut result = Vec::new();
        for rule in ADDED_METHOD_PARAMETER_MIGRATIONS {
            if !rule.since.active(self) || !php_class_is_subtype(class, root, snapshot, rule.class)
            {
                continue;
            }
            let Some(method) = php_own_method_for_migration(class, rule.method) else {
                continue;
            };
            if method_has_parameter(&method, &format!("${}", rule.name)) {
                continue;
            }
            result.push(declaration_migration_problem(
                ADD_METHOD_PARAMETER_CODE,
                &method,
                format!(
                    "Shopware {}: add ${} to {}()",
                    rule.since.label(),
                    rule.name,
                    rule.method
                ),
                "parameter-add",
                String::new(),
                format!("{} ${}", rule.type_text, rule.name),
                rule.position,
                true,
            ));
        }
        result
    }

    fn native_type_problems(
        &self,
        class: &SyntaxNode,
        root: &SyntaxNode,
        snapshot: Option<&Snapshot>,
        resolver: &NameResolver,
    ) -> Vec<Problem> {
        let mut result = Vec::new();
        for rule in PARAMETER_TYPE_MIGRATIONS {
            if !rule.since.active(self) || !php_class_is_subtype(class, root, snapshot, rule.class)
            {
                continue;
            }
            let method = php_own_method_for_migration(class, rule.method);
            let Some(parameter) = method
                .as_ref()
                .and_then(|method| method_parameter_at(method, rule.position))
            else {
                continue;
            };
            let parameter_type = phpquery::parameter_type(&parameter);
            if native_type_matches(&parameter_type, rule.type_text, resolver) {
                continue;
            }
            result.push(declaration_migration_problem(
                NATIVE_TYPE_MIGRATION_CODE,
                &parameter,
                format!(
                    "Shopware {}: use {} for parameter {}",
                    rule.since.label(),
                    rule.type_text,
                    phpquery::parameter_name(&parameter)
                ),
                "parameter-type",
                parameter_type,
                rule.type_text.to_owned(),
                rule.position,
                true,
            ));
        }
        for rule in RETURN_TYPE_MIGRATIONS {
            if !rule.since.active(self) || !php_class_is_subtype(class, root, snapshot, rule.class)
            {
                continue;
            }
            let Some(method) = php_own_method_for_migration(class, rule.method) else {
                continue;
            };
            let return_type = phpquery::method_return_type(&method);
            if native_type_matches(&return_type, rule.type_text, resolver) {
                continue;
            }
            result.push(declaration_migration_problem(
                NATIVE_TYPE_MIGRATION_CODE,
                &method,
                format!(
                    "Shopware {}: use {} as {}() return type",
                    rule.since.label(),
                    rule.type_text,
                    rule.method
                ),
                "return-type",
                return_type,
                rule.type_text.to_owned(),
                -1,
                true,
            ));
        }
        result
    }
}

#[allow(clippy::too_many_arguments)]
fn declaration_migration_problem(
    code: &str,
    element: &SyntaxNode,
    message: String,
    kind: &str,
    original: String,
    replacement: String,
    position: i64,
    safe: bool,
) -> Problem {
    let mut range = element.range_trimmed_trivia();
    if element.kind() == PHP_CLASS_DECLARATION
        && let Some(name) = phpquery::direct_child(element, PHP_NAME)
    {
        range = name.range_trimmed_trivia();
    }
    migration_problem(
        code,
        range,
        element,
        message,
        &ShopwareMigrationPayload {
            rule: "declaration-migration".into(),
            kind: kind.into(),
            safe,
            original,
            replacement,
            argument_index: position,
            ..ShopwareMigrationPayload::default()
        },
    )
}

fn class_can_adopt_parent(class: &SyntaxNode, resolver: &NameResolver, target: &str) -> bool {
    let Some(extends) = phpquery::direct_child(class, PHP_EXTENDS_CLAUSE) else {
        return true;
    };
    phpquery::direct_child(&extends, PHP_NAME).is_some_and(|name| {
        eq_ignore_case(
            resolver.resolve(node_text(&name).trim()).trim_matches('\\'),
            target,
        )
    })
}

fn method_has_parameter(method: &SyntaxNode, name: &str) -> bool {
    phpquery::parameters(method)
        .any(|parameter| eq_ignore_case(&phpquery::parameter_name(&parameter), name))
}

fn method_parameter_at(method: &SyntaxNode, position: i64) -> Option<SyntaxNode> {
    let position = usize::try_from(position).ok()?;
    phpquery::parameters(method).nth(position)
}

fn native_type_matches(actual: &str, expected: &str, resolver: &NameResolver) -> bool {
    let actual = actual.trim();
    let expected = expected.trim();
    if actual.is_empty() || expected.is_empty() {
        return actual == expected;
    }
    if expected.contains(['|', '&', '?']) || !expected.contains('\\') {
        return eq_ignore_case(&actual.replace(' ', ""), &expected.replace(' ', ""));
    }
    eq_ignore_case(
        resolver.resolve(actual).trim_matches('\\'),
        expected.trim_matches('\\'),
    )
}
