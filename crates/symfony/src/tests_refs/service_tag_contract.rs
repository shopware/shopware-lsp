use std::collections::HashMap;

use super::*;

struct Hierarchy(HashMap<&'static str, Vec<&'static str>>);

impl ServiceTagTypeHierarchy for Hierarchy {
    fn is_subtype_of(&self, candidate: &str, target: &str) -> bool {
        self.0
            .get(candidate)
            .is_some_and(|targets| targets.contains(&target))
    }
}

#[test]
fn suggested_service_tags_uses_all_matching_conventional_contracts() {
    let hierarchy = Hierarchy(HashMap::from([(
        "App\\Subscriber",
        vec![
            "Symfony\\Component\\EventDispatcher\\EventSubscriberInterface",
            "Twig\\Extension\\ExtensionInterface",
        ],
    )]));
    assert_eq!(
        suggested_service_tags("App\\Subscriber", Some(&hierarchy)),
        vec!["kernel.event_subscriber", "twig.extension"]
    );
}

#[test]
fn suggested_service_tags_supports_alternative_contracts() {
    let hierarchy = Hierarchy(HashMap::from([(
        "App\\LegacyExtension",
        vec!["Twig_ExtensionInterface"],
    )]));
    assert_eq!(
        suggested_service_tags("\\App\\LegacyExtension", Some(&hierarchy)),
        vec!["twig.extension"]
    );
    assert!(suggested_service_tags("", Some(&hierarchy)).is_empty());
    assert!(suggested_service_tags("App\\LegacyExtension", None).is_empty());
}

#[test]
fn required_service_tag_types_returns_alternatives() {
    assert_eq!(
        required_service_tag_types("twig.loader"),
        vec!["Twig\\Loader\\LoaderInterface", "Twig_LoaderInterface"]
    );
    assert!(required_service_tag_types("unknown").is_empty());
}
