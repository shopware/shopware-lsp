//! Entities whose table holds configuration, not shop data: a handful of
//! rows a shop owner or an extension sets up, so loading all of them is
//! fine.

use std::collections::BTreeSet;

/// The entities known to be small. Entities that grow with the shop
/// (products, orders, customers, media, log entries) are deliberately absent,
/// and so is everything not listed: an unknown table may be large.
pub const SMALL_ENTITIES: &[&str] = &[
    "app",
    "app_action_button",
    "app_payment_method",
    "country",
    "currency",
    "custom_field_set",
    "customer_group",
    "delivery_time",
    "document_type",
    "integration",
    "language",
    "locale",
    "mail_template_type",
    "number_range",
    "number_range_type",
    "payment_method",
    "plugin",
    "sales_channel",
    "sales_channel_type",
    "salutation",
    "scheduled_task",
    "shipping_method",
    "snippet_set",
    "state_machine",
    "state_machine_state",
    "tax",
    "tax_provider",
    "theme",
    "unit",
];

/// The built-in list with the project's additions and without its removals.
pub fn small_entities(add: &[String], remove: &[String]) -> BTreeSet<String> {
    let mut entities: BTreeSet<String> = SMALL_ENTITIES
        .iter()
        .map(|entity| (*entity).to_owned())
        .collect();
    entities.extend(add.iter().map(|entity| entity.trim().to_owned()));
    for entity in remove {
        entities.remove(entity.trim());
    }
    entities.remove("");
    entities
}

/// The entity name of a DAL class or a repository variable: `AppCollection`
/// and `appRepository` are `app`, `SalesChannelCollection` is
/// `sales_channel`. Empty when `name` is just the suffix.
pub fn entity_name_from_identifier(name: &str, suffix: &str) -> String {
    let name = name.rsplit('\\').next().unwrap_or(name);
    let Some(stem) = name
        .strip_suffix(suffix)
        .or_else(|| name.strip_suffix(&suffix.to_lowercase()))
    else {
        return String::new();
    };
    let mut entity = String::with_capacity(stem.len() + 4);
    for character in stem.chars() {
        if character.is_uppercase() {
            if !entity.is_empty() && !entity.ends_with('_') {
                entity.push('_');
            }
            entity.extend(character.to_lowercase());
        } else {
            entity.push(character);
        }
    }
    entity
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn additions_and_removals_change_the_built_in_list() {
        let entities = small_entities(
            &["swag_shop_finder".to_owned(), " ".to_owned()],
            &["country".to_owned(), "unknown".to_owned()],
        );
        assert!(entities.contains("app") && entities.contains("swag_shop_finder"));
        assert!(!entities.contains("country") && !entities.contains(""));
        assert!(!small_entities(&[], &[]).contains("product"));
    }

    #[test]
    fn entity_names_follow_the_class_and_variable_conventions() {
        for (name, suffix, entity) in [
            ("AppCollection", "Collection", "app"),
            (
                "Shopware\\Core\\System\\SalesChannel\\SalesChannelCollection",
                "Collection",
                "sales_channel",
            ),
            ("appRepository", "Repository", "app"),
            ("salesChannelRepository", "Repository", "sales_channel"),
            (
                "stateMachineStateRepository",
                "Repository",
                "state_machine_state",
            ),
            ("repository", "Repository", ""),
            ("Repository", "Repository", ""),
            ("apps", "Repository", ""),
        ] {
            assert_eq!(entity_name_from_identifier(name, suffix), entity, "{name}");
        }
    }
}
