//! Class, member and exception-factory renames.

use super::scan;
use std::collections::HashMap;
use std::sync::LazyLock;

use swlsp_lsp::{Context, Problem};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_ARGUMENT_LIST, PHP_MEMBER_ACCESS, PHP_SCOPED_ACCESS, PHP_SCOPED_CALL, PHP_USE_DECLARATION,
    TK_SCOPE_RESOLUTION,
};
use swlsp_php_resolver::{ImportKind, parse_use_declaration};
use swlsp_php_semantic::{Document, NameKind, Reference, Snapshot, SymbolKind};
use swlsp_syntax::{Kind, SyntaxNode, SyntaxNodeExt, TextRange, TextSize};
use swlsp_textutil::{eq_ignore_case, lowercase};

use super::bc_changes::announced_class_move;
use super::{
    ShopwareMigrationAnalyzer, ShopwareMigrationEdit, ShopwareMigrationPayload, migration_problem,
    node_text,
};
use crate::diagnostics::common::{php_type_is_subtype, short_php_name};

const API_CLASS_RENAME_CODE: &str = "shopware.migration.api.class.rename";
const API_METHOD_RENAME_CODE: &str = "shopware.migration.api.method.rename";
const API_STATIC_METHOD_RENAME_CODE: &str = "shopware.migration.api.static_method.rename";
const API_CONSTANT_RENAME_CODE: &str = "shopware.migration.api.constant.rename";
const API_PROPERTY_MIGRATION_CODE: &str = "shopware.migration.api.property";
const API_EXCEPTION_FACTORY_CODE: &str = "shopware.migration.api.exception_factory";

/// The Shopware 6.x release a rule applies from.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) struct Since {
    pub minor: i64,
    pub patch: i64,
}

pub(crate) const fn since(minor: i64, patch: i64) -> Since {
    Since { minor, patch }
}

impl Since {
    pub fn active(self, analyzer: &ShopwareMigrationAnalyzer) -> bool {
        analyzer.at_least(self.minor, self.patch)
    }

    pub fn label(self) -> String {
        if self.patch == 0 {
            format!("6.{}", self.minor)
        } else {
            format!("6.{}.{}", self.minor, self.patch)
        }
    }
}

#[derive(Clone, Copy, Debug)]
pub(crate) struct ApiClassMigration {
    pub since: Since,
    pub from: &'static str,
    pub to: &'static str,
}

#[derive(Clone, Copy, Debug)]
pub(crate) struct ApiMemberMigration {
    pub since: Since,
    pub owner: &'static str,
    pub from: &'static str,
    pub to: &'static str,
    pub new_owner: &'static str,
}

#[derive(Clone, Copy, Debug)]
pub(crate) struct ApiFactoryMigration {
    pub since: Since,
    pub from: &'static str,
    pub to: &'static str,
    pub method: &'static str,
}

const fn class(since: Since, from: &'static str, to: &'static str) -> ApiClassMigration {
    ApiClassMigration { since, from, to }
}

const fn member(
    since: Since,
    owner: &'static str,
    from: &'static str,
    to: &'static str,
    new_owner: &'static str,
) -> ApiMemberMigration {
    ApiMemberMigration {
        since,
        owner,
        from,
        to,
        new_owner,
    }
}

const fn factory(
    since: Since,
    from: &'static str,
    to: &'static str,
    method: &'static str,
) -> ApiFactoryMigration {
    ApiFactoryMigration {
        since,
        from,
        to,
        method,
    }
}

pub(crate) const API_CLASS_MIGRATIONS: &[ApiClassMigration] = &[
    class(
        since(5, 0),
        "League\\Flysystem\\FilesystemInterface",
        "League\\Flysystem\\FilesystemOperator",
    ),
    class(
        since(5, 0),
        "League\\Flysystem\\AdapterInterface",
        "League\\Flysystem\\FilesystemAdapter",
    ),
    class(
        since(5, 0),
        "League\\Flysystem\\Memory\\MemoryAdapter",
        "League\\Flysystem\\InMemory\\InMemoryFilesystemAdapter",
    ),
    class(
        since(5, 0),
        "Shopware\\Core\\Framework\\Adapter\\Asset\\ThemeAssetPackage",
        "Shopware\\Storefront\\Theme\\ThemeAssetPackage",
    ),
    class(
        since(5, 0),
        "Maltyxx\\ImagesGenerator\\ImagesGeneratorProvider",
        "bheller\\ImagesGenerator\\ImagesGeneratorProvider",
    ),
    class(
        since(5, 0),
        "Shopware\\Core\\Framework\\Event\\BusinessEventInterface",
        "Shopware\\Core\\Framework\\Event\\FlowEventAware",
    ),
    class(
        since(5, 0),
        "Shopware\\Core\\Framework\\Event\\MailActionInterface",
        "Shopware\\Core\\Framework\\Event\\MailAware",
    ),
    class(
        since(5, 0),
        "Shopware\\Core\\Framework\\Log\\LogAwareBusinessEventInterface",
        "Shopware\\Core\\Framework\\Log\\LogAware",
    ),
    class(
        since(5, 0),
        "Shopware\\Storefront\\Event\\ProductExportContentTypeEvent",
        "Shopware\\Core\\Content\\ProductExport\\Event\\ProductExportContentTypeEvent",
    ),
    class(
        since(5, 0),
        "Shopware\\Storefront\\Page\\Product\\Review\\MatrixElement",
        "Shopware\\Core\\Content\\Product\\SalesChannel\\Review\\MatrixElement",
    ),
    class(
        since(5, 0),
        "Shopware\\Storefront\\Page\\Product\\Review\\RatingMatrix",
        "Shopware\\Core\\Content\\Product\\SalesChannel\\Review\\RatingMatrix",
    ),
    class(
        since(5, 0),
        "Shopware\\Storefront\\Page\\Address\\Listing\\AddressListingCriteriaEvent",
        "Shopware\\Core\\Checkout\\Customer\\Event\\AddressListingCriteriaEvent",
    ),
    class(
        since(5, 0),
        "Shopware\\Administration\\Service\\AdminOrderCartService",
        "Shopware\\Core\\Checkout\\Cart\\ApiOrderCartService",
    ),
    class(
        since(5, 0),
        "Shopware\\Core\\System\\User\\Service\\UserProvisioner",
        "Shopware\\Core\\Maintenance\\User\\Service\\UserProvisioner",
    ),
    class(
        since(5, 0),
        "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityRepositoryInterface",
        "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityRepository",
    ),
    class(
        since(5, 0),
        "Shopware\\Core\\System\\SalesChannel\\Entity\\SalesChannelRepositoryInterface",
        "Shopware\\Core\\System\\SalesChannel\\Entity\\SalesChannelRepository",
    ),
    class(
        since(5, 0),
        "Shopware\\Core\\Framework\\Adapter\\Console\\ShopwareStyle",
        "Symfony\\Component\\Console\\Style\\SymfonyStyle",
    ),
    class(
        since(6, 0),
        "Shopware\\Core\\Framework\\DataAbstractionLayer\\Event\\BeforeDeleteEvent",
        "Shopware\\Core\\Framework\\DataAbstractionLayer\\Event\\EntityDeleteEvent",
    ),
    class(
        since(6, 0),
        "Shopware\\Core\\Framework\\Api\\Exception\\ExceptionFailedException",
        "Shopware\\Core\\Framework\\Api\\Exception\\ExpectationFailedException",
    ),
    class(
        since(8, 0),
        "Shopware\\Administration\\Controller\\NotificationController",
        "Shopware\\Core\\Framework\\Notification\\Api\\NotificationController",
    ),
    class(
        since(8, 0),
        "Shopware\\Administration\\Notification\\NotificationCollection",
        "Shopware\\Core\\Framework\\Notification\\NotificationCollection",
    ),
    class(
        since(8, 0),
        "Shopware\\Administration\\Notification\\NotificationDefinition",
        "Shopware\\Core\\Framework\\Notification\\NotificationDefinition",
    ),
    class(
        since(8, 0),
        "Shopware\\Administration\\Notification\\NotificationEntity",
        "Shopware\\Core\\Framework\\Notification\\NotificationEntity",
    ),
    class(
        since(8, 0),
        "Shopware\\Core\\Framework\\Plugin\\Util\\AssetService",
        "Shopware\\Core\\Framework\\Adapter\\Asset\\AssetService",
    ),
    class(
        since(8, 0),
        "Shopware\\Elasticsearch\\Product\\SearchConfigLoader",
        "Shopware\\Core\\Framework\\DataAbstractionLayer\\Search\\SearchConfigLoader",
    ),
];

const FLYSYSTEM_CLASSES: [&str; 4] = [
    "League\\Flysystem\\FilesystemOperator",
    "League\\Flysystem\\Filesystem",
    "League\\Flysystem\\FilesystemInterface",
    "League\\Flysystem\\FilesystemAdapter",
];

pub(crate) static API_METHOD_MIGRATIONS: LazyLock<Vec<ApiMemberMigration>> = LazyLock::new(|| {
    let mut result = vec![
        member(
            since(5, 0),
            "Shopware\\Core\\Framework\\Adapter\\Twig\\EntityTemplateLoader",
            "clearInternalCache",
            "reset",
            "",
        ),
        member(
            since(5, 0),
            "Shopware\\Core\\Content\\ImportExport\\Processing\\Mapping\\Mapping",
            "getDefault",
            "getDefaultValue",
            "",
        ),
        member(
            since(5, 0),
            "Shopware\\Core\\Content\\ImportExport\\Processing\\Mapping\\Mapping",
            "getMappedDefault",
            "getDefaultValue",
            "",
        ),
        member(
            since(6, 0),
            "Shopware\\Elasticsearch\\Framework\\Indexing\\IndexerOffset",
            "setNextDefinition",
            "selectNextDefinition",
            "",
        ),
        member(
            since(6, 0),
            "Shopware\\Elasticsearch\\Framework\\Indexing\\IndexerOffset",
            "setNextLanguage",
            "selectNextLanguage",
            "",
        ),
    ];
    let methods = [
        ("rename", "move"),
        ("createDir", "createDirectory"),
        ("deleteDir", "deleteDirectory"),
        ("update", "write"),
        ("updateStream", "writeStream"),
        ("put", "write"),
        ("putStream", "writeStream"),
        ("getTimestamp", "lastModified"),
        ("has", "fileExists"),
        ("getMimetype", "mimeType"),
        ("getSize", "fileSize"),
        ("getVisibility", "visibility"),
    ];
    for class in FLYSYSTEM_CLASSES {
        for (from, to) in methods {
            result.push(member(since(5, 0), class, from, to, ""));
        }
    }
    result
});

pub(crate) const API_STATIC_METHOD_MIGRATIONS: &[ApiMemberMigration] = &[member(
    since(6, 0),
    "Shopware\\Core\\Framework\\DataAbstractionLayer\\FieldSerializer\\JsonFieldSerializer",
    "encodeJson",
    "encode",
    "Shopware\\Core\\Framework\\Util\\Json",
)];

pub(crate) const API_PROPERTY_MIGRATIONS: &[ApiMemberMigration] = &[member(
    since(5, 0),
    "Shopware\\Core\\Content\\Flow\\Dispatching\\FlowState",
    "sequenceId",
    "getSequenceId",
    "",
)];

pub(crate) const API_FACTORY_MIGRATIONS: &[ApiFactoryMigration] = &[
    factory(
        since(6, 0),
        "Shopware\\Core\\Framework\\Routing\\Exception\\InvalidRequestParameterException",
        "Shopware\\Core\\Framework\\Routing\\RoutingException",
        "invalidRequestParameter",
    ),
    factory(
        since(6, 0),
        "Shopware\\Core\\Framework\\Routing\\Exception\\MissingRequestParameterException",
        "Shopware\\Core\\Framework\\Routing\\RoutingException",
        "missingRequestParameter",
    ),
    factory(
        since(6, 0),
        "Shopware\\Core\\Framework\\Routing\\Exception\\LanguageNotFoundException",
        "Shopware\\Core\\Framework\\Routing\\RoutingException",
        "languageNotFound",
    ),
    factory(
        since(6, 0),
        "Shopware\\Core\\Framework\\DataAbstractionLayer\\Exception\\InvalidSerializerFieldException",
        "Shopware\\Core\\Framework\\DataAbstractionLayer\\DataAbstractionLayerException",
        "invalidSerializerField",
    ),
    factory(
        since(6, 0),
        "Shopware\\Core\\Framework\\DataAbstractionLayer\\Exception\\VersionMergeAlreadyLockedException",
        "Shopware\\Core\\Framework\\DataAbstractionLayer\\DataAbstractionLayerException",
        "versionMergeAlreadyLocked",
    ),
    factory(
        since(6, 0),
        "Shopware\\Elasticsearch\\Exception\\UnsupportedElasticsearchDefinitionException",
        "Shopware\\Elasticsearch\\ElasticsearchException",
        "unsupportedElasticsearchDefinition",
    ),
    factory(
        since(6, 0),
        "Shopware\\Elasticsearch\\Exception\\ElasticsearchIndexingException",
        "Shopware\\Elasticsearch\\ElasticsearchException",
        "indexingError",
    ),
    factory(
        since(6, 0),
        "Shopware\\Elasticsearch\\Exception\\ServerNotAvailableException",
        "Shopware\\Elasticsearch\\ElasticsearchException",
        "serverNotAvailable",
    ),
    factory(
        since(6, 0),
        "Shopware\\Core\\Content\\ProductExport\\Exception\\EmptyExportException",
        "Shopware\\Core\\Content\\ProductExport\\ProductExportException",
        "productExportNotFound",
    ),
    factory(
        since(6, 0),
        "Shopware\\Core\\Content\\ProductExport\\Exception\\RenderFooterException",
        "Shopware\\Core\\Content\\ProductExport\\ProductExportException",
        "renderFooterException",
    ),
    factory(
        since(6, 0),
        "Shopware\\Core\\Content\\ProductExport\\Exception\\RenderHeaderException",
        "Shopware\\Core\\Content\\ProductExport\\ProductExportException",
        "renderHeaderException",
    ),
    factory(
        since(6, 0),
        "Shopware\\Core\\Content\\ProductExport\\Exception\\RenderProductException",
        "Shopware\\Core\\Content\\ProductExport\\ProductExportException",
        "renderProductException",
    ),
];

pub(crate) static API_CONSTANT_MIGRATIONS: LazyLock<Vec<ApiMemberMigration>> =
    LazyLock::new(|| {
        let mut result = vec![
            member(
                since(6, 0),
                "Shopware\\Core\\Checkout\\Cart",
                "CHECKOUT_ORDER_PLACED",
                "CHECKOUT_ORDER_PLACED",
                "Shopware\\Core\\Framework\\Event\\BusinessEvents",
            ),
            member(
                since(6, 0),
                "Shopware\\Elasticsearch\\Product\\ElasticsearchProductDefinition",
                "KEYWORD_FIELD",
                "KEYWORD_FIELD",
                "Shopware\\Elasticsearch\\Framework\\AbstractElasticsearchDefinition",
            ),
            member(
                since(6, 0),
                "Shopware\\Elasticsearch\\Product\\ElasticsearchProductDefinition",
                "BOOLEAN_FIELD",
                "BOOLEAN_FIELD",
                "Shopware\\Elasticsearch\\Framework\\AbstractElasticsearchDefinition",
            ),
            member(
                since(6, 0),
                "Shopware\\Elasticsearch\\Product\\ElasticsearchProductDefinition",
                "FLOAT_FIELD",
                "FLOAT_FIELD",
                "Shopware\\Elasticsearch\\Framework\\AbstractElasticsearchDefinition",
            ),
            member(
                since(6, 0),
                "Shopware\\Elasticsearch\\Product\\ElasticsearchProductDefinition",
                "INT_FIELD",
                "INT_FIELD",
                "Shopware\\Elasticsearch\\Framework\\AbstractElasticsearchDefinition",
            ),
            member(
                since(6, 0),
                "Shopware\\Elasticsearch\\Product\\ElasticsearchProductDefinition",
                "SEARCH_FIELD",
                "SEARCH_FIELD",
                "Shopware\\Elasticsearch\\Framework\\AbstractElasticsearchDefinition",
            ),
            member(
                since(7, 0),
                "Shopware\\Core\\Content\\MailTemplate\\Subscriber\\MailSendSubscriberConfig",
                "MAIL_CONFIG_EXTENSION",
                "MAIL_CONFIG_EXTENSION",
                "Shopware\\Core\\Content\\Flow\\Dispatching\\Action\\SendMailAction",
            ),
            member(
                since(7, 0),
                "Shopware\\Core\\Content\\MailTemplate\\Subscriber\\MailSendSubscriberConfig",
                "ACTION_NAME",
                "ACTION_NAME",
                "Shopware\\Core\\Content\\Flow\\Dispatching\\Action\\SendMailAction",
            ),
            member(
                since(7, 0),
                "Shopware\\Core\\Content\\MailTemplate\\MailTemplateActions",
                "MAIL_TEMPLATE_MAIL_SEND_ACTION",
                "ACTION_NAME",
                "Shopware\\Core\\Content\\Flow\\Dispatching\\Action\\SendMailAction",
            ),
            member(
                since(7, 0),
                "Shopware\\Core\\Framework\\Adapter\\Cache\\Http\\CacheResponseSubscriber",
                "STATE_LOGGED_IN",
                "STATE_LOGGED_IN",
                "Shopware\\Core\\Framework\\Adapter\\Cache\\CacheStateSubscriber",
            ),
            member(
                since(7, 0),
                "Shopware\\Core\\Framework\\Adapter\\Cache\\Http\\CacheResponseSubscriber",
                "STATE_CART_FILLED",
                "STATE_CART_FILLED",
                "Shopware\\Core\\Framework\\Adapter\\Cache\\CacheStateSubscriber",
            ),
            member(
                since(7, 0),
                "Shopware\\Core\\Framework\\Adapter\\Cache\\Http\\CacheResponseSubscriber",
                "CURRENCY_COOKIE",
                "CURRENCY_COOKIE",
                "Shopware\\Core\\Framework\\Adapter\\Cache\\Http\\HttpCacheKeyGenerator",
            ),
            member(
                since(7, 0),
                "Shopware\\Core\\Framework\\Adapter\\Cache\\Http\\CacheResponseSubscriber",
                "CONTEXT_CACHE_COOKIE",
                "CONTEXT_CACHE_COOKIE",
                "Shopware\\Core\\Framework\\Adapter\\Cache\\Http\\HttpCacheKeyGenerator",
            ),
            member(
                since(7, 0),
                "Shopware\\Core\\Framework\\Adapter\\Cache\\Http\\CacheResponseSubscriber",
                "SYSTEM_STATE_COOKIE",
                "SYSTEM_STATE_COOKIE",
                "Shopware\\Core\\Framework\\Adapter\\Cache\\Http\\HttpCacheKeyGenerator",
            ),
            member(
                since(7, 0),
                "Shopware\\Core\\Framework\\Adapter\\Cache\\Http\\CacheResponseSubscriber",
                "INVALIDATION_STATES_HEADER",
                "INVALIDATION_STATES_HEADER",
                "Shopware\\Core\\Framework\\Adapter\\Cache\\Http\\HttpCacheKeyGenerator",
            ),
            member(
                since(8, 0),
                "Shopware\\Core\\Checkout\\Cart\\AbstractCartPersister",
                "PERSIST_CART_ERROR_PERMISSION",
                "PERSIST_CART_ERROR",
                "Shopware\\Core\\Checkout\\CheckoutPermissions",
            ),
            member(
                since(8, 0),
                "Shopware\\Core\\Checkout\\Cart\\Delivery\\DeliveryProcessor",
                "SKIP_DELIVERY_PRICE_RECALCULATION",
                "SKIP_DELIVERY_PRICE_RECALCULATION",
                "Shopware\\Core\\Checkout\\CheckoutPermissions",
            ),
            member(
                since(8, 0),
                "Shopware\\Core\\Checkout\\Cart\\Delivery\\DeliveryProcessor",
                "SKIP_DELIVERY_TAX_RECALCULATION",
                "SKIP_DELIVERY_TAX_RECALCULATION",
                "Shopware\\Core\\Checkout\\CheckoutPermissions",
            ),
            member(
                since(8, 0),
                "Shopware\\Core\\Checkout\\Promotion\\Cart\\PromotionCollector",
                "SKIP_PROMOTION",
                "SKIP_PROMOTION",
                "Shopware\\Core\\Checkout\\CheckoutPermissions",
            ),
            member(
                since(8, 0),
                "Shopware\\Core\\Checkout\\Promotion\\Cart\\PromotionCollector",
                "SKIP_AUTOMATIC_PROMOTIONS",
                "SKIP_AUTOMATIC_PROMOTIONS",
                "Shopware\\Core\\Checkout\\CheckoutPermissions",
            ),
            member(
                since(8, 0),
                "Shopware\\Core\\Checkout\\Promotion\\Cart\\PromotionCollector",
                "PIN_MANUAL_PROMOTIONS",
                "PIN_MANUAL_PROMOTIONS",
                "Shopware\\Core\\Checkout\\CheckoutPermissions",
            ),
            member(
                since(8, 0),
                "Shopware\\Core\\Checkout\\Promotion\\Cart\\PromotionCollector",
                "PIN_AUTOMATIC_PROMOTIONS",
                "PIN_AUTOMATIC_PROMOTIONS",
                "Shopware\\Core\\Checkout\\CheckoutPermissions",
            ),
            member(
                since(8, 0),
                "Shopware\\Core\\Content\\Product\\Cart\\ProductCartProcessor",
                "ALLOW_PRODUCT_PRICE_OVERWRITES",
                "ALLOW_PRODUCT_PRICE_OVERWRITES",
                "Shopware\\Core\\Checkout\\CheckoutPermissions",
            ),
            member(
                since(8, 0),
                "Shopware\\Core\\Content\\Product\\Cart\\ProductCartProcessor",
                "ALLOW_PRODUCT_LABEL_OVERWRITES",
                "ALLOW_PRODUCT_LABEL_OVERWRITES",
                "Shopware\\Core\\Checkout\\CheckoutPermissions",
            ),
            member(
                since(8, 0),
                "Shopware\\Core\\Content\\Product\\Cart\\ProductCartProcessor",
                "SKIP_PRODUCT_RECALCULATION",
                "SKIP_PRODUCT_RECALCULATION",
                "Shopware\\Core\\Checkout\\CheckoutPermissions",
            ),
            member(
                since(8, 0),
                "Shopware\\Core\\Content\\Product\\Cart\\ProductCartProcessor",
                "SKIP_PRODUCT_STOCK_VALIDATION",
                "SKIP_PRODUCT_STOCK_VALIDATION",
                "Shopware\\Core\\Checkout\\CheckoutPermissions",
            ),
            member(
                since(8, 0),
                "Shopware\\Core\\Content\\Product\\Cart\\ProductCartProcessor",
                "KEEP_INACTIVE_PRODUCT",
                "KEEP_INACTIVE_PRODUCT",
                "Shopware\\Core\\Checkout\\CheckoutPermissions",
            ),
        ];
        for class in FLYSYSTEM_CLASSES {
            result.push(member(
                since(5, 0),
                class,
                "VISIBILITY_PUBLIC",
                "PUBLIC",
                "League\\Flysystem\\Visibility",
            ));
            result.push(member(
                since(5, 0),
                class,
                "VISIBILITY_PRIVATE",
                "PRIVATE",
                "League\\Flysystem\\Visibility",
            ));
        }
        result
    });

type MemberIndex = HashMap<String, Vec<ApiMemberMigration>>;

fn index_api_member_migrations(rules: &[ApiMemberMigration]) -> MemberIndex {
    let mut result: MemberIndex = HashMap::new();
    for rule in rules {
        result.entry(lowercase(rule.from)).or_default().push(*rule);
    }
    result
}

static API_METHOD_MIGRATIONS_BY_NAME: LazyLock<MemberIndex> =
    LazyLock::new(|| index_api_member_migrations(&API_METHOD_MIGRATIONS));
static API_STATIC_METHOD_MIGRATIONS_BY_NAME: LazyLock<MemberIndex> =
    LazyLock::new(|| index_api_member_migrations(API_STATIC_METHOD_MIGRATIONS));
static API_PROPERTY_MIGRATIONS_BY_NAME: LazyLock<MemberIndex> =
    LazyLock::new(|| index_api_member_migrations(API_PROPERTY_MIGRATIONS));
static API_CONSTANT_MIGRATIONS_BY_NAME: LazyLock<MemberIndex> =
    LazyLock::new(|| index_api_member_migrations(&API_CONSTANT_MIGRATIONS));

/// A class that moved to another name, from the table or announced by the
/// indexed Shopware source.
#[derive(Clone, Debug)]
pub(crate) struct ClassMove {
    pub label: String,
    pub from: String,
    pub to: String,
}

/// The moves that apply to this document: the active table rules, and the
/// announced ones looked up once per class name.
struct ClassMoves<'a> {
    snapshot: Option<&'a Snapshot>,
    known: HashMap<String, Option<ClassMove>>,
}

impl ClassMoves<'_> {
    fn get(&mut self, name: &str) -> Option<ClassMove> {
        let name = trim_backslashes(name);
        let snapshot = self.snapshot;
        self.known
            .entry(lowercase(name))
            .or_insert_with(|| snapshot.and_then(|snapshot| announced_class_move(snapshot, name)))
            .clone()
    }
}

struct ApiClassOccurrence {
    range: TextRange,
    rule: ClassMove,
    node: SyntaxNode,
    in_use: bool,
    explicit_alias: bool,
}

pub(crate) fn valid_source_range(start: u32, end: u32, source: &str) -> bool {
    start < end && end as usize <= source.len()
}

fn slice(source: &str, range: TextRange) -> String {
    source
        .get(u32::from(range.start()) as usize..u32::from(range.end()) as usize)
        .unwrap_or_default()
        .to_owned()
}

fn trim_backslashes(value: &str) -> &str {
    value.trim_matches('\\')
}

impl ShopwareMigrationAnalyzer {
    pub(super) fn api_migration_problems(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        document: &Document,
        snapshot: Option<&Snapshot>,
        source: &str,
    ) -> Vec<Problem> {
        let mut result = self.api_class_migration_problems(ctx, root, document, snapshot, source);
        result.extend(self.api_member_migration_problems(ctx, root, document, snapshot, source));
        result.extend(self.api_factory_migration_problems(ctx, root, source));
        result
    }

    fn api_class_migration_problems(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        document: &Document,
        snapshot: Option<&Snapshot>,
        source: &str,
    ) -> Vec<Problem> {
        let mut active = ClassMoves {
            snapshot,
            known: HashMap::new(),
        };
        for rule in API_CLASS_MIGRATIONS {
            if rule.since.active(self) {
                active.known.insert(
                    lowercase(rule.from),
                    Some(ClassMove {
                        label: rule.since.label(),
                        from: rule.from.to_owned(),
                        to: rule.to.to_owned(),
                    }),
                );
            }
        }
        let mut occurrences = Vec::new();
        let mut imports: HashMap<String, bool> = HashMap::new();
        for declaration in phpquery::use_declarations(root) {
            for imported in parse_use_declaration(&node_text(&declaration)) {
                if imported.kind != ImportKind::Class {
                    continue;
                }
                let Some(rule) = active.get(&imported.target) else {
                    continue;
                };
                let Some(range) = php_use_target_range(&declaration, &imported.target, source)
                else {
                    continue;
                };
                imports.insert(lowercase(&rule.from), true);
                occurrences.push(ApiClassOccurrence {
                    range,
                    rule,
                    node: declaration.clone(),
                    in_use: true,
                    explicit_alias: !eq_ignore_case(
                        &imported.alias,
                        short_php_name(&imported.target),
                    ),
                });
            }
        }
        for reference in &document.references {
            if ctx.is_cancelled() {
                return Vec::new();
            }
            if reference.kind != NameKind::Class {
                continue;
            }
            let Some(rule) = class_migration_for_reference(reference, &mut active) else {
                continue;
            };
            let range = reference.range;
            if !valid_source_range(range.start().into(), range.end().into(), source) {
                continue;
            }
            let Some(node) = root.node_at(range.start()) else {
                continue;
            };
            let in_use = ancestor_php_kind(&node, PHP_USE_DECLARATION).is_some();
            if in_use {
                imports.insert(lowercase(&rule.from), true);
            }
            occurrences.push(ApiClassOccurrence {
                range,
                rule,
                node,
                in_use,
                explicit_alias: false,
            });
        }
        let mut result = Vec::with_capacity(occurrences.len());
        for occurrence in &occurrences {
            if imports
                .get(&lowercase(&occurrence.rule.from))
                .copied()
                .unwrap_or(false)
                && !occurrence.in_use
            {
                continue;
            }
            let (edits, safe) = api_class_migration_edits(root, occurrence, &occurrences, source);
            let (original, replacement) = edits
                .first()
                .map(|edit| (edit.original.clone(), edit.replacement.clone()))
                .unwrap_or_default();
            result.push(api_migration_problem(
                API_CLASS_RENAME_CODE,
                occurrence.range,
                &occurrence.node,
                &occurrence.rule.label,
                "class",
                &occurrence.rule.from,
                &occurrence.rule.to,
                original,
                replacement,
                safe,
                edits,
            ));
        }
        result
    }

    fn api_factory_migration_problems(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        source: &str,
    ) -> Vec<Problem> {
        let resolver = scan::resolver(root);
        let mut result = Vec::new();
        for creation in phpquery::object_creations(root, &[]) {
            if ctx.is_cancelled() {
                return result;
            }
            let resolved = resolver.resolve(&phpquery::object_class_name(&creation));
            let class = trim_backslashes(&resolved);
            for rule in API_FACTORY_MIGRATIONS {
                if !rule.since.active(self) || !eq_ignore_case(class, rule.from) {
                    continue;
                }
                let Some(arguments) = phpquery::direct_child(&creation, PHP_ARGUMENT_LIST) else {
                    break;
                };
                let range = creation.range_trimmed_trivia();
                if !valid_source_range(range.start().into(), range.end().into(), source) {
                    break;
                }
                result.push(api_migration_problem(
                    API_EXCEPTION_FACTORY_CODE,
                    range,
                    &creation,
                    &rule.since.label(),
                    "exception-factory",
                    &format!("new {}", rule.from),
                    &format!("{}::{}", rule.to, rule.method),
                    slice(source, range),
                    format!("\\{}::{}{}", rule.to, rule.method, node_text(&arguments)),
                    true,
                    Vec::new(),
                ));
                break;
            }
        }
        result
    }

    fn api_member_migration_problems(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        document: &Document,
        snapshot: Option<&Snapshot>,
        source: &str,
    ) -> Vec<Problem> {
        let mut result = Vec::new();
        for reference in &document.references {
            if ctx.is_cancelled() {
                return result;
            }
            let range = reference.range;
            if reference.kind != NameKind::Member
                || !valid_source_range(range.start().into(), range.end().into(), source)
            {
                continue;
            }
            let Some(node) = root.node_at(range.start()) else {
                continue;
            };
            let target = reference.target_kind;
            if target == SymbolKind::Property && !reference.is_static {
                if let Some(rule) = self.match_api_member_rule(
                    reference,
                    snapshot,
                    &API_PROPERTY_MIGRATIONS_BY_NAME,
                ) {
                    result.push(api_migration_problem(
                        API_PROPERTY_MIGRATION_CODE,
                        range,
                        &node,
                        &rule.since.label(),
                        "property",
                        &format!("{}::${}", rule.owner, rule.from),
                        &format!("{}::{}()", rule.owner, rule.to),
                        slice(source, range),
                        format!("{}()", rule.to),
                        !reference.write,
                        Vec::new(),
                    ));
                }
                continue;
            }
            if target == SymbolKind::Method && !reference.is_static {
                if let Some(rule) =
                    self.match_api_member_rule(reference, snapshot, &API_METHOD_MIGRATIONS_BY_NAME)
                {
                    result.push(api_migration_problem(
                        API_METHOD_RENAME_CODE,
                        range,
                        &node,
                        &rule.since.label(),
                        "method",
                        &format!("{}::{}", rule.owner, rule.from),
                        &format!("{}::{}", rule.owner, rule.to),
                        slice(source, range),
                        rule.to.to_owned(),
                        true,
                        Vec::new(),
                    ));
                }
                continue;
            }
            if target == SymbolKind::Method && reference.is_static {
                if let Some(rule) = self.match_api_member_rule(
                    reference,
                    snapshot,
                    &API_STATIC_METHOD_MIGRATIONS_BY_NAME,
                ) {
                    let Some(call) = ancestor_php_kind(&node, PHP_SCOPED_CALL) else {
                        continue;
                    };
                    let Some(arguments) = phpquery::direct_child(&call, PHP_ARGUMENT_LIST) else {
                        continue;
                    };
                    let call_range = call.range_trimmed_trivia();
                    result.push(api_migration_problem(
                        API_STATIC_METHOD_RENAME_CODE,
                        call_range,
                        &call,
                        &rule.since.label(),
                        "static-method",
                        &format!("{}::{}", rule.owner, rule.from),
                        &format!("{}::{}", rule.new_owner, rule.to),
                        slice(source, call_range),
                        format!("\\{}::{}{}", rule.new_owner, rule.to, node_text(&arguments)),
                        true,
                        Vec::new(),
                    ));
                }
                continue;
            }
            if target == SymbolKind::ClassConstant
                && reference.is_static
                && let Some(rule) = self.match_api_member_rule(
                    reference,
                    snapshot,
                    &API_CONSTANT_MIGRATIONS_BY_NAME,
                )
            {
                let Some(access) = ancestor_php_scoped_access(&node) else {
                    continue;
                };
                let access_range = access.range_trimmed_trivia();
                result.push(api_migration_problem(
                    API_CONSTANT_RENAME_CODE,
                    access_range,
                    &access,
                    &rule.since.label(),
                    "constant",
                    &format!("{}::{}", rule.owner, rule.from),
                    &format!("{}::{}", rule.new_owner, rule.to),
                    slice(source, access_range),
                    format!("\\{}::{}", rule.new_owner, rule.to),
                    true,
                    Vec::new(),
                ));
            }
        }
        result
    }

    fn match_api_member_rule(
        &self,
        reference: &Reference,
        snapshot: Option<&Snapshot>,
        rules_by_name: &MemberIndex,
    ) -> Option<ApiMemberMigration> {
        rules_by_name
            .get(&lowercase(&reference.name))?
            .iter()
            .find(|rule| {
                rule.since.active(self)
                    && php_type_is_subtype(&reference.receiver, snapshot, rule.owner)
            })
            .copied()
    }
}

fn api_class_migration_edits(
    root: &SyntaxNode,
    occurrence: &ApiClassOccurrence,
    occurrences: &[ApiClassOccurrence],
    source: &str,
) -> (Vec<ShopwareMigrationEdit>, bool) {
    let range = occurrence.range;
    let (start, end) = (u32::from(range.start()), u32::from(range.end()));
    if !valid_source_range(start, end, source) {
        return (Vec::new(), false);
    }
    let original = slice(source, range);
    if !occurrence.in_use {
        return (
            vec![ShopwareMigrationEdit {
                start,
                end,
                original,
                replacement: format!("\\{}", occurrence.rule.to),
            }],
            true,
        );
    }
    let Some(use_declaration) = ancestor_php_kind(&occurrence.node, PHP_USE_DECLARATION) else {
        return (Vec::new(), false);
    };
    if node_text(&use_declaration).contains('{') {
        return (Vec::new(), false);
    }
    let mut edits = vec![ShopwareMigrationEdit {
        start,
        end,
        original,
        replacement: occurrence.rule.to.clone(),
    }];
    let old_short = short_php_name(&occurrence.rule.from);
    let new_short = short_php_name(&occurrence.rule.to);
    if eq_ignore_case(old_short, new_short) || occurrence.explicit_alias {
        return (edits, true);
    }
    if php_import_alias_conflict(root, &use_declaration, new_short) {
        edits[0].replacement.push_str(" as ");
        edits[0].replacement.push_str(old_short);
        return (edits, true);
    }
    for other in occurrences {
        if other.in_use || !eq_ignore_case(&other.rule.from, &occurrence.rule.from) {
            continue;
        }
        let (other_start, other_end) =
            (u32::from(other.range.start()), u32::from(other.range.end()));
        if !valid_source_range(other_start, other_end, source) {
            return (Vec::new(), false);
        }
        let other_original = slice(source, other.range);
        let other_replacement = if other_original.contains('\\') {
            format!("\\{}", occurrence.rule.to)
        } else {
            new_short.to_owned()
        };
        edits.push(ShopwareMigrationEdit {
            start: other_start,
            end: other_end,
            original: other_original,
            replacement: other_replacement,
        });
    }
    (edits, true)
}

fn php_import_alias_conflict(root: &SyntaxNode, current: &SyntaxNode, alias: &str) -> bool {
    phpquery::use_declarations(root)
        .iter()
        .filter(|declaration| *declaration != current)
        .any(|declaration| {
            parse_use_declaration(&node_text(declaration))
                .iter()
                .any(|imported| {
                    imported.kind == ImportKind::Class && eq_ignore_case(&imported.alias, alias)
                })
        })
}

/// The source range of `target` inside a `use` declaration, including a leading
/// backslash.
fn php_use_target_range(declaration: &SyntaxNode, target: &str, source: &str) -> Option<TextRange> {
    if target.is_empty() {
        return None;
    }
    let text = node_text(declaration);
    let trimmed = trim_backslashes(target);
    let mut index = lowercase(&text).find(&lowercase(trimmed))?;
    let bytes = text.as_bytes();
    if index >= bytes.len() {
        return None;
    }
    if index > 0 && bytes[index - 1] == b'\\' {
        index -= 1;
    }
    let start = u32::from(declaration.text_range().start()) + index as u32;
    let mut end = start + trimmed.len() as u32;
    if bytes[index] == b'\\' {
        end += 1;
    }
    if !valid_source_range(start, end, source) {
        return None;
    }
    Some(TextRange::new(TextSize::from(start), TextSize::from(end)))
}

#[allow(clippy::too_many_arguments)]
fn api_migration_problem(
    code: &str,
    range: TextRange,
    element: &SyntaxNode,
    label: &str,
    kind: &str,
    from: &str,
    to: &str,
    original: String,
    replacement: String,
    safe: bool,
    edits: Vec<ShopwareMigrationEdit>,
) -> Problem {
    migration_problem(
        code,
        range,
        element,
        format!("Shopware {label}: replace {from} with {to}"),
        &ShopwareMigrationPayload {
            rule: "api-rename".into(),
            kind: kind.into(),
            safe,
            original,
            replacement,
            start: range.start().into(),
            end: range.end().into(),
            edits,
            ..ShopwareMigrationPayload::default()
        },
    )
}

fn class_migration_for_reference(
    reference: &Reference,
    active: &mut ClassMoves<'_>,
) -> Option<ClassMove> {
    for name in reference.qualified_names() {
        if let Some(rule) = active.get(name) {
            return Some(rule);
        }
    }
    active.get(&reference.name)
}

/// `node` or its nearest ancestor of `kind`.
pub(crate) fn ancestor_php_kind(node: &SyntaxNode, kind: Kind) -> Option<SyntaxNode> {
    node.ancestors().find(|current| current.kind() == kind)
}

fn ancestor_php_scoped_access(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.ancestors().find(|current| {
        matches!(current.kind(), PHP_SCOPED_ACCESS | PHP_MEMBER_ACCESS)
            && current.child_token_of_kind(TK_SCOPE_RESOLUTION).is_some()
    })
}
