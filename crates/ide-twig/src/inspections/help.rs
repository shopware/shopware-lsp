//! What to do about the Twig markup diagnostics; see
//! [`swlsp_lsp::RuleHelp`].

use swlsp_lsp::RuleHelp;

const fn help(
    code: &'static str,
    fix: &'static str,
    exceptions: &'static str,
    opt_out: &'static str,
) -> RuleHelp {
    RuleHelp {
        code,
        fix,
        exceptions,
        opt_out,
    }
}

pub(crate) const MARKUP: &[RuleHelp] = &[
    help(
        "twig.raw.attribute",
        "Remove |raw so Twig escapes the value, or use |escape('html_attr') for a value \
         that is not quoted. For JSON in an attribute use |json_encode without raw.",
        "raw in element content is not reported.",
        "",
    ),
    help(
        "twig.sanitize.attribute",
        "Remove |sw_sanitize so Twig escapes the value; to drop markup from the text use \
         |striptags.",
        "sw_sanitize in element content is not reported.",
        "",
    ),
    help(
        "twig.raw.translation",
        "Replace |raw with |sw_sanitize: it keeps the allowed formatting of the snippet and \
         removes scripts.",
        "json_encode|raw and raw on a plain variable are not reported.",
        "",
    ),
    help(
        "twig.html.target_blank",
        "Add rel=\"noopener\" to the link (rel=\"noopener noreferrer\" to also hide the referrer).",
        "A rel or target built with Twig is not judged.",
        "",
    ),
    help(
        "twig.html.button_type",
        "Add type=\"button\" for a button that runs a script, type=\"submit\" for one that \
         submits its form.",
        "A tag with Twig between its attributes is not judged; it may add the type.",
        "",
    ),
    help(
        "twig.html.img_alt",
        "Add alt text that says what the image shows, from a snippet; use alt=\"\" for a \
         decorative image.",
        "A tag with Twig between its attributes is not judged.",
        "",
    ),
    help(
        "twig.html.duplicate_attribute",
        "Remove one of the two; browsers ignore the second. Merge class values into one \
         attribute.",
        "Attributes in different branches of an if are not duplicates.",
        "",
    ),
];

pub(crate) const TWIG: &[RuleHelp] = &[
    help(
        "twig.template.missing",
        "Correct the template name, or create the template; quick fixes offer close names \
         and, for a name below templates/, create the file.",
        "Names built at runtime, and includes marked `ignore missing`, are not checked.",
        "Add `ignore missing` to an include whose template is optional.",
    ),
    help(
        "twig.template.block.missing",
        "Use a block the template or one of its parents defines; a quick fix offers close names.",
        "Only renderBlock() and renderBlockView() on a controller are checked.",
        "",
    ),
    help(
        "twig.component.missing",
        "Use the name of a registered Twig component; a quick fix offers close names.",
        "",
        "",
    ),
    help(
        "twig.component.block.missing",
        "Use a block the component's template defines; a quick fix offers close names.",
        "",
        "",
    ),
    help(
        "twig.component.live_action.missing",
        "Use an action the live component declares with #[LiveAction]; a quick fix offers \
         close names.",
        "",
        "",
    ),
    help(
        "twig.component.live_argument.missing",
        "Use a parameter name of the live action; a quick fix offers close names.",
        "",
        "",
    ),
    help(
        "twig.component.mixed_syntax",
        "Inside <twig:Component> write <twig:block name=\"...\"> in place of {% block %}.",
        "{% block %} inside the {% component %} tag syntax is valid and not reported.",
        "",
    ),
    help(
        "twig.component.self_macro_import",
        "Import the macros by the template's full path; _self refers to the component's \
         template inside a component.",
        "",
        "",
    ),
    help(
        "twig.component.live_event.missing",
        "Emit an event a #[LiveListener] listens to, or add the listener; a quick fix \
         offers close names.",
        "",
        "",
    ),
    help(
        "twig.component.live_event_argument.missing",
        "Use an argument the listener declares with #[LiveArg]; a quick fix offers close names.",
        "",
        "",
    ),
    help(
        "twig.filter.deprecated",
        "Use the replacement the deprecation message names.",
        "",
        "",
    ),
    help(
        "twig.function.deprecated",
        "Use the replacement the deprecation message names.",
        "",
        "",
    ),
    help(
        "twig.tag.deprecated",
        "Use the replacement the deprecation message names.",
        "",
        "",
    ),
    help(
        "twig.test.deprecated",
        "Use the replacement the deprecation message names.",
        "",
        "",
    ),
    help(
        "twig.enum.missing",
        "Pass the fully qualified name of an existing PHP enum; a quick fix offers close names.",
        "",
        "",
    ),
    help(
        "twig.enum.invalid",
        "Pass an enum: enum() and enum_cases() do not accept other classes. For a class \
         constant use constant().",
        "",
        "",
    ),
    help(
        "twig.macro.missing",
        "Use a macro the imported template defines; a quick fix offers close names.",
        "Calls through a namespace that resolves to no template are not checked.",
        "",
    ),
    help(
        "twig.member.missing",
        "Use a property or method the PHP class has (Twig also tries getX(), isX() and \
         hasX()), or correct the declared type of the variable.",
        "Reported only for a variable with a definite object type. Arrays, values of \
         unknown type, ArrayAccess and Traversable objects, stdClass and classes with __get \
         or __call are not checked.",
        "",
    ),
    help(
        "twig.member.deprecated",
        "Use the member the @deprecated note of the PHP declaration names.",
        "",
        "",
    ),
    help(
        "twig.member.arguments",
        "Pass the arguments the PHP method expects; the message names the mismatch.",
        "Arguments of unknown type match anything. Calls with named or spread arguments, \
         and classes with __call, are not checked.",
        "",
    ),
    help(
        "twig.member.not-callable",
        "Drop the parentheses to read the property, or call a method that exists: with \
         parentheses Twig only calls methods.",
        "",
        "",
    ),
    help(
        "frontend.snippet.missing",
        "Add the key to a storefront snippet file of the extension \
         (Resources/snippet/**/*.json); the quick fix \"Create snippet\" does it. Or correct \
         the key.",
        "Keys built at runtime are not checked.",
        "",
    ),
    help(
        "admin.snippet.missing",
        "Add the key to an Administration snippet file of the extension; the quick fix \
         \"Create admin snippet\" does it. Or correct the key.",
        "Keys built at runtime are not checked, nor are module titles and descriptions that \
         contain spaces (plain text).",
        "",
    ),
];
