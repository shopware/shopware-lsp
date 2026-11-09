use super::*;
use crate::twig::{TwigEnumAnalyzer, TwigMacroAnalyzer};

#[test]
fn twig_enum_diagnostics_validate_existence_and_enum_kind() {
    let root = tempdir();
    let php_dir = tempdir();
    let php = php_index(php_dir.path());
    index_file(
        &php,
        &root.path().join("src/Types.php").to_string_lossy(),
        "<?php\nnamespace App;\nenum OrderStatus { case Open; }\nclass NotAnEnum {}",
    );
    let (_, diagnostics) = check_problems(
        &TwigEnumAnalyzer::new(Some(php)),
        &swlsp_uriutil::file_uri(
            &root
                .path()
                .join("templates/order.html.twig")
                .to_string_lossy(),
        ),
        "{{ enum('App\\\\OrderStatus') }}\n{{ enum_cases('{|twig.enum.missing:App\\\\OrderStatu|}') }}\n{{ enum('{|twig.enum.invalid:App\\\\NotAnEnum|}') }}",
    );
    assert!(diagnostics[0].message.contains("OrderStatu"));
    assert!(suggestions(&diagnostics[0]).contains(&"App\\\\OrderStatus".to_owned()));
    assert!(diagnostics[1].message.contains("not an enum"));
}

#[test]
fn twig_macro_diagnostics_report_unknown_imported_macro() {
    let dir = tempdir();
    let index = twig_index(dir.path());
    index_file(
        index.as_ref(),
        "/project/templates/macros/forms.html.twig",
        "{% macro input(name) %}{% endmacro %}",
    );
    let (_, diagnostics) = check_problems(
        &TwigMacroAnalyzer::new(Some(index)),
        "file:///project/templates/page.html.twig",
        "{% import 'macros/forms.html.twig' as forms %}\n{{ forms.{|twig.macro.missing:inpt|}('email') }}\n{{ forms.input('name') }}\n",
    );
    assert!(diagnostics[0].message.contains("inpt"));
    assert!(suggestions(&diagnostics[0]).contains(&"input".to_owned()));
}

#[test]
fn twig_macro_diagnostics_overlay_unsaved_self_macros() {
    let dir = tempdir();
    let index = twig_index(dir.path());
    check_problems(
        &TwigMacroAnalyzer::new(Some(index)),
        "file:///project/templates/page.html.twig",
        "{% import _self as local %}\n{{ local.helper() }}\n{% macro helper() %}{% endmacro %}\n",
    );
}
