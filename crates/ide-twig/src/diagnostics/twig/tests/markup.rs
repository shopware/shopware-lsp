use super::*;
use crate::diagnostics::twig::TwigMarkupAnalyzer;

const URI: &str = "file:///project/src/Resources/views/storefront/page/index.html.twig";

fn check(fixture: &str) -> Vec<String> {
    let (_, problems) = check_problems(&TwigMarkupAnalyzer::new(), URI, fixture);
    problems
        .into_iter()
        .map(|problem| problem.message)
        .collect()
}

#[test]
fn output_is_escaped_for_its_context() {
    let messages = check(
        r#"<div title="{{ shipping.translated.description|{|twig.raw.attribute:raw|} }}"
     aria-label="{{ 'acme.label'|trans|{|twig.sanitize.attribute:sw_sanitize|} }}"
     data-options="{{ options|json_encode }}"
     data-name="{{ name|striptags }}">
    {{ 'acme.text'|trans|{|twig.raw.translation:raw|} }}
    {{ 'acme.text'|trans({'%name%': name})|{|twig.raw.translation:raw|} }}
    {{ 'acme.text'|trans|sw_sanitize }}
    {{ 'acme.text'|trans }}
    {{ structuredData|json_encode|raw }}
    {{ label|raw }}
</div>
"#,
    );
    assert_eq!(
        messages[0],
        "raw in an attribute value lets the value close the attribute; let Twig escape it, or use |escape('html_attr')"
    );
}

#[test]
fn elements_carry_the_attributes_they_need() {
    check(
        r#"<a href="https://example.com" {|twig.html.target_blank:target="_blank"|}>Docs</a>
<a href="https://example.com" {|twig.html.target_blank:target="_blank"|} rel="nofollow">Docs</a>
<a href="https://example.com" target="_blank" rel="noopener nofollow">Docs</a>
<a href="https://example.com" target="_blank" rel="{{ rel }}">Docs</a>
<a href="https://example.com" target="{{ target }}">Docs</a>
<a href="/account">Account</a>

<{|twig.html.button_type:button|} class="btn">Save</button>
<button type="button" class="btn">Save</button>
<button class="btn" {{ attributes }}>Save</button>
<button class="btn" {% if submit %}type="submit"{% endif %}>Save</button>

<{|twig.html.img_alt:img|} src="{{ asset('logo.png') }}">
<img src="{{ asset('logo.png') }}" alt="">
<img src="{{ asset('logo.png') }}" alt="{{ 'acme.logo'|trans }}">

<input type="text" autocomplete="off" name="city" {|twig.html.duplicate_attribute:autocomplete="address-level2"|}>
<input type="text" {% if a %}class="a"{% else %}class="b"{% endif %}>
"#,
    );
}

#[test]
fn administration_templates_are_left_alone() {
    check_problems(
        &TwigMarkupAnalyzer::new(),
        "file:///project/src/Resources/app/administration/src/module/acme/page/index.html.twig",
        r#"<button class="sw-button">{{ $tc('acme.save') }}</button>
"#,
    );
}
