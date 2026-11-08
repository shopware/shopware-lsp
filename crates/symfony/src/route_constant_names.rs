//! Route names written as class constants, resolved at query time.

use swlsp_php::PhpIndex;
use swlsp_php_semantic::Symbol;
use swlsp_php_types::Kind;

use crate::model::Route;

/// The slice of the PHP index needed to resolve a route name written as a
/// class constant. [`PhpIndex`] implements it.
pub trait ClassConstantLookup {
    fn constants(&self, class_name: &str) -> Vec<Symbol>;
}

impl ClassConstantLookup for PhpIndex {
    fn constants(&self, class_name: &str) -> Vec<Symbol> {
        PhpIndex::constants(self, class_name)
    }
}

/// The literal route name behind a `Class::CONSTANT` reference, or "" when
/// it cannot be resolved. Only literal string constants are usable.
pub fn resolve_constant_route_name(
    reference: &str,
    lookup: Option<&dyn ClassConstantLookup>,
) -> String {
    let Some(lookup) = lookup else {
        return String::new();
    };
    let Some((class_name, constant_name)) = reference.split_once("::") else {
        return String::new();
    };
    if class_name.is_empty() || constant_name.is_empty() {
        return String::new();
    }
    for constant in lookup.constants(class_name) {
        if constant.name.as_str() != constant_name {
            continue;
        }
        if constant.ty.kind() != Kind::LiteralString {
            return String::new();
        }
        return constant.ty.name().to_owned();
    }
    String::new()
}

/// Fills in the name of every route that carries only a constant reference,
/// dropping those that stay unresolved. Literal names pass through.
pub fn resolve_constant_route_names(
    routes: Vec<Route>,
    lookup: Option<&dyn ClassConstantLookup>,
) -> Vec<Route> {
    routes
        .into_iter()
        .filter_map(|mut route| {
            if route.name.is_empty() && !route.name_constant.is_empty() {
                route.name = resolve_constant_route_name(&route.name_constant, lookup);
            }
            (!route.name.is_empty()).then_some(route)
        })
        .collect()
}

#[cfg(test)]
#[path = "tests_routes/route_constant_names.rs"]
mod tests;
