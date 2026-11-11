//! What to do about the Administration diagnostics; see
//! [`swlsp_lsp::RuleHelp`].
//!
//! Many of these rules report only a likely misspelling: an unknown name
//! with no registered name close to it stays silent, because plugins and
//! runtime code can add components, directives and members the index does
//! not see.

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

/// A Twig component the Meteor component library replaces.
macro_rules! migration {
    ($code:literal, $old:literal, $new:literal) => {
        help(
            concat!("admin.twig.migration.", $code),
            concat!(
                "Replace ",
                $old,
                " with ",
                $new,
                ". The quick fix \"Migrate ",
                $old,
                " to ",
                $new,
                "\" renames the tag and rewrites props, events and slots when the usage \
                 converts mechanically; otherwise migrate by hand and review what the \
                 message names."
            ),
            "Reported from Shopware 6.7, in .html.twig templates, once the project's \
             Administration marks the component deprecated or no longer registers it. A usage \
             with constructs that have no direct counterpart (a bound variant, conflicting \
             attributes, complex label slots) is reported without a quick fix.",
            "",
        )
    };
}

const SPELLING: &str = "Reported only when a registered name is close to the one written; \
                        a name with no close match is taken for one added at runtime.";

pub(crate) const ADMIN: &[RuleHelp] = &[
    help(
        "admin.block-override.nested",
        "Move the inner <sw-block extends> out of the outer one; each override is declared \
         on its own, at the top level of the template.",
        "<sw-block> without extends is a new block, not an override, and may be nested.",
        "",
    ),
    help(
        "admin.block-override.conditional",
        "Declare the override unconditionally and put the v-if (or {% if %}) inside it, \
         around the content.",
        "v-show does not count as a condition.",
        "",
    ),
    help(
        "admin.block-override.repeated",
        "Declare the override once and put the v-for (or {% for %}) inside it.",
        "",
        "",
    ),
    help(
        "admin.block-parent.conditional",
        "Render <sw-block-parent> unconditionally; to hide the parent content conditionally \
         wrap it in an element with the condition inside the override.",
        "",
        "",
    ),
    help(
        "admin.block-parent.repeated",
        "Render <sw-block-parent> once, outside the loop.",
        "",
        "",
    ),
    help(
        "admin.component.not-found",
        "Use the name of a registered component; a quick fix offers close names. A \
         component of the extension itself must be registered with Component.register() and \
         imported from the module's entry file.",
        "Only sw- and mt- tags are checked. Nothing is reported while the component index \
         is empty.",
        "",
    ),
    help(
        "admin.component.parent-not-found",
        "Pass the name of a registered component as the second argument of Component.extend().",
        "",
        "",
    ),
    help(
        "admin.component.registry-not-found",
        "Use the name of a registered component; a quick fix offers close names.",
        "Nothing is reported while the component index is empty.",
        "Guard an optional component with getComponentRegistry().has('name'); has() is not checked.",
    ),
    help(
        "admin.module.not-found",
        "Use the name of a registered module; a quick fix offers close names.",
        "Nothing is reported while the module index is empty.",
        "Guard an optional module with getModuleRegistry().has('name'); has() is not checked.",
    ),
    help(
        "admin.directive.not-found",
        "Correct the directive name; a quick fix offers the close registered name.",
        SPELLING,
        "",
    ),
    help(
        "admin.filter.not-found",
        "Use the name of a registered filter; a quick fix offers close names.",
        "Only Filter.getByName() calls are checked.",
        "",
    ),
    help(
        "admin.cms-element.not-found",
        "Use the name of a CMS element registered with registerCmsElement(); a quick fix \
         offers close names.",
        "The name property of a register call declares an element and is not checked.",
        "",
    ),
    help(
        "admin.cms-block.not-found",
        "Use the name of a CMS block registered with registerCmsBlock(); a quick fix offers \
         close names.",
        "The name property of a register call declares a block and is not checked.",
        "",
    ),
    help(
        "admin.component.block-not-found",
        "Correct the block name to one the parent component's template defines; a quick fix \
         offers the close name.",
        "Reported only for a block name close to a parent block: a template of an extended \
         or overridden component may define blocks of its own.",
        "",
    ),
    help(
        "admin.component.deprecated-block",
        "Override the block the deprecation note names instead.",
        "",
        "",
    ),
    help(
        "admin.component.deprecated",
        "Use the replacement the deprecation note names.",
        "From Shopware 6.7 the components replaced by Meteor are reported by \
         admin.twig.migration.* instead.",
        "",
    ),
    help(
        "admin.component.missing-required-prop",
        "Pass the prop; a quick fix adds the attribute with a placeholder value.",
        "Not reported when the prop arrives through v-model, or when the tag has a v-bind \
         object that cannot be read statically.",
        "",
    ),
    help(
        "admin.component.unknown-prop",
        "Correct the prop name; a quick fix offers the close name.",
        "Reported only for a name close to a declared prop. HTML attributes, data-* and \
         aria-* fall through to the root element and are not checked.",
        "",
    ),
    help(
        "admin.component.unknown-event",
        "Correct the event name; a quick fix offers the close name.",
        "Reported only for a name close to a declared event. Native DOM events are not checked.",
        "",
    ),
    help(
        "admin.component.unknown-model",
        "Correct the v-model argument; a quick fix offers the close name.",
        "Reported only for a name close to a declared model.",
        "",
    ),
    help(
        "admin.component.unknown-slot",
        "Correct the slot name; a quick fix offers the close name.",
        "Reported only for a name close to a declared slot. Dynamic slot families such as \
         column-* are accepted.",
        "",
    ),
    help(
        "admin.component.unknown-slot-prop",
        "Use a prop the slot exposes; a quick fix offers close names.",
        "Not reported when the slot's props are not fully known, or when the whole scope \
         object is bound to one variable.",
        "",
    ),
    help(
        "admin.component.invalid-prop-value",
        "Use one of the values the prop accepts; a quick fix offers close ones.",
        "Only literal values of props with a closed set of values are checked.",
        "",
    ),
    help(
        "admin.component.static-prop-type",
        "Bind the value so it is read as an expression: write :prop=\"...\". A quick fix \
         adds the colon.",
        "String props, and boolean attributes without a value, are not reported.",
        "",
    ),
    help(
        "admin.component.bound-prop-type",
        "Pass a value of the type the prop expects.",
        "Only mismatches between whole categories are reported (a string for an Array \
         prop); nullable values and expressions of unknown type are not.",
        "",
    ),
    help(
        "admin.component.deprecated-prop",
        "Use the prop the deprecation note names.",
        "",
        "",
    ),
    help(
        "admin.component.model-not-assignable",
        "Bind v-model to something that can be assigned: a data property or a writable \
         computed property, not a literal or a call.",
        "",
        "",
    ),
    help(
        "admin.component.model-type",
        "Bind a value of the type the component's model expects.",
        "Not reported when the type of the expression is unknown.",
        "",
    ),
    help(
        "admin.component.unknown-instance-member",
        "Declare the member on the component (data, props, computed, methods, inject), or \
         correct its name; a quick fix offers close names.",
        "Names starting with $ are not checked, nor are components whose members cannot be \
         listed completely (object spreads, mixins that cannot be resolved).",
        "",
    ),
    help(
        "admin.component.unknown-template-member",
        "Correct the name to a member of the component; a quick fix offers the close name.",
        SPELLING,
        "",
    ),
    help(
        "admin.component.unknown-vue-member",
        "Use a property the value has; a quick fix offers close names.",
        "Not reported when the type of the value is not fully known.",
        "",
    ),
    help(
        "admin.component.deprecated-member",
        "Use the member the deprecation note names.",
        "",
        "",
    ),
    help(
        "admin.application-container.unknown-member",
        "Correct the service or factory name; a quick fix offers the close name.",
        SPELLING,
        "",
    ),
    help(
        "admin.shopware-context.unknown-member",
        "Correct the property name; a quick fix offers the close name.",
        SPELLING,
        "",
    ),
    help(
        "admin.shopware-utils.unknown-member",
        "Correct the helper name; a quick fix offers the close name.",
        SPELLING,
        "",
    ),
    help(
        "admin.event-bus.missing-payload",
        "Pass the payload the event declares as the second argument of emit().",
        "Only events declared in the typed EventBus event map are checked; events whose \
         payload may be undefined need none.",
        "",
    ),
    help(
        "admin.event-bus.missing-handler",
        "Pass a handler function as the second argument of on().",
        "Only events declared in the typed EventBus event map are checked.",
        "",
    ),
    help(
        "admin.event-bus.payload-type",
        "Pass a payload of the type the event declares.",
        "Not reported when the type of the argument is unknown.",
        "",
    ),
    help(
        "admin.event-bus.handler-type",
        "Pass a function as the handler.",
        "Not reported when the type of the argument is unknown.",
        "",
    ),
    help(
        "admin.module-route.not-found",
        "Use the name of a route a module registers (module name, a dot, the route key, for \
         example sw.product.detail); a quick fix offers close names.",
        "Nothing is reported while the route index is empty.",
        "",
    ),
    help(
        "admin.privilege.not-found",
        "Use a privilege key a privilege mapping registers (for example product.viewer); a \
         quick fix offers close names.",
        "Only acl.can() in templates is checked. Nothing is reported until the project \
         registers privileges of its own.",
        "",
    ),
    help(
        "admin.slot-syntax-deprecated",
        "Use <template #name=\"scope\"> in place of slot and slot-scope; a quick fix \
         rewrites the tag.",
        "slot on an element other than <template> is not reported.",
        "",
    ),
    help(
        "admin.vue-i18n.tc-deprecated",
        "Call $t() with the same arguments; it handles pluralization. A quick fix renames \
         the call.",
        "",
        "",
    ),
    migration!("alert", "sw-alert", "mt-banner"),
    migration!("button", "sw-button", "mt-button"),
    migration!("card", "sw-card", "mt-card"),
    migration!("checkbox-field", "sw-checkbox-field", "mt-checkbox"),
    migration!("colorpicker", "sw-colorpicker", "mt-colorpicker"),
    migration!("datepicker", "sw-datepicker", "mt-datepicker"),
    migration!("email-field", "sw-email-field", "mt-email-field"),
    migration!("icon", "sw-icon", "mt-icon"),
    migration!("loader", "sw-loader", "mt-loader"),
    migration!("number-field", "sw-number-field", "mt-number-field"),
    migration!("password-field", "sw-password-field", "mt-password-field"),
    migration!("popover", "sw-popover", "mt-floating-ui"),
    migration!("progress-bar", "sw-progress-bar", "mt-progress-bar"),
    migration!("select-field", "sw-select-field", "mt-select"),
    migration!("skeleton-bar", "sw-skeleton-bar", "mt-skeleton-bar"),
    migration!("switch-field", "sw-switch-field", "mt-switch"),
    migration!("text-field", "sw-text-field", "mt-text-field"),
    migration!("textarea-field", "sw-textarea-field", "mt-textarea"),
    migration!("url-field", "sw-url-field", "mt-url-field"),
];
