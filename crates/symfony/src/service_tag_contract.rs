//! Conventional Symfony service tags and the types they require.

use crate::util::go_trim_space;

const REQUIRED_SERVICE_TAG_TYPES: &[(&str, &[&str])] = &[
    ("assetic.asset", &["Assetic\\Filter\\FilterInterface"]),
    (
        "assetic.factory_worker",
        &["Assetic\\Factory\\Worker\\WorkerInterface"],
    ),
    ("assetic.filter", &["Assetic\\Filter\\FilterInterface"]),
    (
        "assetic.formula_loader",
        &["Assetic\\Factory\\Loader\\FormulaLoaderInterface"],
    ),
    (
        "console.command",
        &["Symfony\\Component\\Console\\Command\\Command"],
    ),
    (
        "data_collector",
        &["Symfony\\Component\\HttpKernel\\DataCollector\\DataCollectorInterface"],
    ),
    (
        "form.type",
        &["Symfony\\Component\\Form\\FormTypeInterface"],
    ),
    (
        "form.type_extension",
        &["Symfony\\Component\\Form\\FormTypeExtensionInterface"],
    ),
    (
        "form.type_guesser",
        &["Symfony\\Component\\Form\\FormTypeGuesserInterface"],
    ),
    (
        "kernel.cache_warmer",
        &["Symfony\\Component\\HttpKernel\\CacheWarmer\\CacheWarmerInterface"],
    ),
    (
        "kernel.event_subscriber",
        &["Symfony\\Component\\EventDispatcher\\EventSubscriberInterface"],
    ),
    (
        "kernel.fragment_renderer",
        &["Symfony\\Component\\HttpKernel\\Fragment\\FragmentRendererInterface"],
    ),
    (
        "routing.loader",
        &["Symfony\\Component\\Config\\Loader\\LoaderInterface"],
    ),
    (
        "security.voter",
        &["Symfony\\Component\\Security\\Core\\Authorization\\Voter\\VoterInterface"],
    ),
    (
        "serializer.encoder",
        &["Symfony\\Component\\Serializer\\Encoder\\EncoderInterface"],
    ),
    (
        "serializer.normalizer",
        &["Symfony\\Component\\Serializer\\Normalizer\\NormalizerInterface"],
    ),
    (
        "swiftmailer.default.plugin",
        &["Swift_Events_EventListener"],
    ),
    (
        "templating.helper",
        &["Symfony\\Component\\Templating\\Helper\\HelperInterface"],
    ),
    (
        "translation.loader",
        &["Symfony\\Component\\Translation\\Loader\\LoaderInterface"],
    ),
    (
        "translation.extractor",
        &["Symfony\\Component\\Translation\\Extractor\\ExtractorInterface"],
    ),
    (
        "translation.dumper",
        &["Symfony\\Component\\Translation\\Dumper\\DumperInterface"],
    ),
    (
        "twig.extension",
        &[
            "Twig\\Extension\\ExtensionInterface",
            "Twig_ExtensionInterface",
        ],
    ),
    (
        "twig.loader",
        &["Twig\\Loader\\LoaderInterface", "Twig_LoaderInterface"],
    ),
    (
        "validator.constraint_validator",
        &["Symfony\\Component\\Validator\\ConstraintValidator"],
    ),
    (
        "validator.initializer",
        &["Symfony\\Component\\Validator\\ObjectInitializerInterface"],
    ),
    (
        "routing.expression_language_provider",
        &["Symfony\\Component\\ExpressionLanguage\\ExpressionFunctionProviderInterface"],
    ),
    (
        "security.expression_language_provider",
        &["Symfony\\Component\\ExpressionLanguage\\ExpressionFunctionProviderInterface"],
    ),
];

/// Alternative base classes/interfaces accepted for a conventional Symfony
/// service tag. Empty when the tag has no portable type contract.
pub fn required_service_tag_types(tag: &str) -> Vec<String> {
    REQUIRED_SERVICE_TAG_TYPES
        .iter()
        .find(|(name, _)| *name == tag)
        .map(|(_, types)| types.iter().map(|value| (*value).to_owned()).collect())
        .unwrap_or_default()
}

/// The part of the PHP semantic graph needed to infer conventional tags.
pub trait ServiceTagTypeHierarchy {
    fn is_subtype_of(&self, candidate: &str, target: &str) -> bool;
}

impl ServiceTagTypeHierarchy for swlsp_php::PhpIndex {
    fn is_subtype_of(&self, candidate: &str, target: &str) -> bool {
        swlsp_php::PhpIndex::is_subtype_of(self, candidate, target)
    }
}

/// The conventional tags implied by the class hierarchy, sorted and unique.
pub fn suggested_service_tags(
    class_name: &str,
    hierarchy: Option<&dyn ServiceTagTypeHierarchy>,
) -> Vec<String> {
    let class_name = go_trim_space(class_name).trim_matches('\\');
    let Some(hierarchy) = hierarchy.filter(|_| !class_name.is_empty()) else {
        return Vec::new();
    };
    let mut result: Vec<String> = REQUIRED_SERVICE_TAG_TYPES
        .iter()
        .filter(|(_, targets)| {
            targets
                .iter()
                .any(|target| hierarchy.is_subtype_of(class_name, target))
        })
        .map(|(tag, _)| (*tag).to_owned())
        .collect();
    result.sort();
    result
}

#[cfg(test)]
#[path = "tests_refs/service_tag_contract.rs"]
mod tests;
