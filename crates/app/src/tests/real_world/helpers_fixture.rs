//! Assertion helpers of the real-world suite (workspace opening lives in
//! `fixture.rs`).

#[allow(unused_imports)]
use super::indexing_main::record_failure as soft;

/// Records a soft failure and
/// continues with the zero value.
#[allow(dead_code)]
fn soft_default<T: Default>(message: String) -> T {
    soft(message);
    T::default()
}

macro_rules! soft_assert {
    ($cond:expr, $($arg:tt)+) => {
        if !$cond {
            soft(format!($($arg)+));
        }
    };
}

use swlsp_lsp_protocol as protocol;

pub fn real_world_document_link_targets(links: &[protocol::DocumentLink]) -> Vec<String> {
    links.iter().map(|link| link.target.clone()).collect()
}

pub fn format_bytes(value: u64) -> String {
    const KIB: f64 = 1024.0;
    const MIB: f64 = 1024.0 * KIB;
    const GIB: f64 = 1024.0 * MIB;
    let value = value as f64;
    if value >= GIB {
        format!("{:.1} GiB", value / GIB)
    } else if value >= MIB {
        format!("{:.1} MiB", value / MIB)
    } else if value >= KIB {
        format!("{:.1} KiB", value / KIB)
    } else {
        format!("{value:.1} B")
    }
}

pub fn require_twig_global(globals: &[swlsp_twig::Global], name: &str, type_name: &str) {
    soft_assert!(
        globals
            .iter()
            .any(|global| global.name == name && (type_name.is_empty() || global.ty == type_name)),
        "missing Twig global name={name} type={type_name} globals={globals:#?}"
    );
}

pub fn require_twig_tag(tags: &[swlsp_twig::TwigTag], name: &str) {
    soft_assert!(
        tags.iter().any(|tag| tag.name == name),
        "Twig tag {name:?} not found in {tags:#?}"
    );
}

pub fn require_twig_operator(
    operators: &[swlsp_twig::TwigOperator],
    name: &str,
    path_suffix: &str,
) {
    let suffix = path_suffix.replace('\\', "/");
    soft_assert!(
        operators.iter().any(|operator| operator.name == name
            && operator.file_path.replace('\\', "/").ends_with(&suffix)),
        "Twig operator {name:?} from {path_suffix:?} not found in {operators:#?}"
    );
}

pub fn require_template_reference_path(references: &[swlsp_twig::TemplateReference], suffix: &str) {
    let suffix = swlsp_uriutil::filepath::clean(suffix);
    soft_assert!(
        references.iter().any(
            |reference| swlsp_uriutil::filepath::clean(&reference.file_path).ends_with(&suffix)
        ),
        "template reference path ending in {suffix:?} not found"
    );
}

pub fn require_twig_variable(variables: &[swlsp_php::TwigTemplateVariable], name: &str) {
    soft_assert!(
        variables.iter().any(|variable| variable.name == name),
        "Twig variable {name:?} not found in {variables:#?}"
    );
}

pub fn require_twig_template_input(variables: &[swlsp_twig::TemplateVariable], name: &str) {
    soft_assert!(
        variables.iter().any(|variable| variable.name == name),
        "Twig template input {name:?} not found in {variables:#?}"
    );
}

pub fn require_twig_block(blocks: &[swlsp_twig::TemplateBlock], name: &str) {
    let Some(block) = blocks.iter().find(|block| block.name == name) else {
        soft(format!("Twig block {name:?} not found in {blocks:#?}"));
        return;
    };
    soft_assert!(!block.range.is_empty(), "block {name} has an empty range");
    soft_assert!(
        std::path::Path::new(&block.file_path).is_file(),
        "{}",
        block.file_path
    );
}

pub fn require_route(routes: &[swlsp_symfony::Route], name: &str) {
    soft_assert!(
        routes.iter().any(|route| route.name == name),
        "Symfony route {name:?} not found in {routes:#?}"
    );
}

pub fn require_console_input(inputs: &[swlsp_console::Input], name: &str) {
    soft_assert!(
        inputs.iter().any(|input| input.name == name),
        "Console input {name:?} not found in {inputs:#?}"
    );
}

pub fn require_console_catalog_input(inputs: &[swlsp_console::CatalogInput], name: &str) {
    soft_assert!(
        inputs.iter().any(|input| input.name == name),
        "console catalog input {name:?} not found in {inputs:#?}"
    );
}
