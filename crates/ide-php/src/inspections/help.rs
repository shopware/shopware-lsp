//! What to do about the core PHP diagnostics; see [`swlsp_lsp::RuleHelp`].
//!
//! The PHP analysis reports only what it is sure of: a value of unknown or
//! mixed type, or a class with a matching magic method, is never the
//! subject of a finding.

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

pub(crate) const PHP: &[RuleHelp] = &[
    help(
        "php.unusedImport",
        "Remove the import; a quick fix removes that item, and the Organize Imports source \
         action cleans up the whole file.",
        "A name used only in a doc comment counts as used. Files with a syntax error are \
         not checked.",
        "",
    ),
    help(
        "php.abstract",
        "Implement the method the message names, or declare the class abstract.",
        "Abstract classes, interfaces and traits are not checked; a method inherited from a \
         parent or a trait counts.",
        "",
    ),
    help(
        "php.arguments",
        "Pass the arguments the signature in the message expects: the detail after the \
         colon names the parameter and the type given.",
        "Arguments of unknown or mixed type match anything. Extra positional arguments to a \
         function declared in the project are accepted, as PHP does. Calls with a spread of \
         unknown shape, first-class callables and dynamic callees are not checked.",
        "",
    ),
    help(
        "php.deprecated",
        "Use the replacement the message or the declaration's @deprecated note names.",
        "Uses inside a declaration that is itself deprecated, and inside the deprecated \
         class itself, are not reported.",
        "",
    ),
    help(
        "php.duplicate",
        "Keep one declaration of the class: rename or remove the other, or correct the \
         namespace so it matches the file's PSR-4 path.",
        "",
        "",
    ),
    help(
        "php.extension",
        "If the runtime has the extension, add it to php.extensions (or remove it from \
         php.disabledExtensions) in .config/shopware-lsp.yaml; otherwise stop using the symbol.",
        "Reported only for an extension the project explicitly lacks; an extension whose \
         availability is unknown is not reported.",
        "",
    ),
    help(
        "php.inheritance",
        "Do not extend the class: use composition, or decorate its service. A class marked \
         @final in its PHPDoc is not meant to be extended and may become final.",
        "",
        "",
    ),
    help(
        "php.override",
        "Make the member compatible with the one it overrides: same static-ness, no \
         narrower visibility, compatible parameter and return types. For a method marked \
         #[Override] that overrides nothing, correct its name or remove the attribute.",
        "Constructors, private parent members and interfaces are not checked. With \
         #[\\ReturnTypeWillChange] the return type is not compared.",
        "",
    ),
    help(
        "php.parse",
        "Correct the syntax at the reported position; the message names what the parser \
         expected.",
        "",
        "",
    ),
    help(
        "php.returnType",
        "Return a value of the declared type, or change the declared return type.",
        "Generators are not checked, nor are values of unknown or mixed type or declared \
         types with unresolved templates.",
        "",
    ),
    help(
        "php.undefined",
        "Correct the name, import the class with a use statement, or declare the member. \
         For a class of an optional package, guard the use with class_exists().",
        "Functions and constants are never reported. Not reported: members on a receiver of \
         unknown type, on classes with a matching magic method (__call, __get, ...), on \
         stdClass and #[AllowDynamicProperties] classes, and static:: on a class that is not \
         final.",
        "Guard an optional class with class_exists(), interface_exists(), trait_exists() or \
         enum_exists(), and an optional method with method_exists(); guarded uses are not \
         reported.",
    ),
    help(
        "php.version",
        "Use syntax the configured PHP version supports, or raise the version: it comes \
         from config.platform.php or the php requirement in composer.json.",
        "",
        "",
    ),
    help(
        "php.visibility",
        "Use a public method or accessor of the class, or widen the member's visibility if \
         the class is yours.",
        "Not reported for classes with a matching magic method, and for Closure::bind \
         scopes.",
        "",
    ),
];
