//! Symfony UX Twig and Live components (port of Go `internal/twigcomponent`):
//! component declarations from PHP attributes, service tags, the compiled
//! container and anonymous templates; props; Live actions, listeners and
//! events; and their usages in Twig templates.

mod config;
mod index;
mod model;
mod php;
mod twig;

pub use index::Index;
pub use model::{
    Block, Component, ComponentBlockUsage, Declaration, LiveAction, LiveActionArgumentReference,
    LiveActionParameter, LiveActionReference, LiveActionReferenceKind, LiveEventArgumentReference,
    LiveEventReference, LiveEventReferenceKind, LiveListener, Namespace, Prop, PropUsage, Record,
    SourceKind, Usage, UsageKind,
};
pub use php::{
    declaration_at_php, declarations_in_php, live_event_argument_context_php,
    live_event_argument_reference_at_php, live_event_argument_references_in_php,
    live_event_reference_at_php, live_event_references_in_php, live_listener_at_php,
    live_listeners_in_php,
};
pub use twig::{
    accessor_member_at, block_usage_at, block_usages_in_twig, enclosing_html_component,
    live_action_argument_context, live_action_argument_reference_at,
    live_action_argument_references_in_twig, live_action_reference_at,
    live_action_references_in_twig, live_event_argument_reference_at_twig,
    live_event_argument_references_in_twig, live_event_reference_at_twig,
    live_event_references_in_twig, prop_usage_at, props_in_twig, usage_at, usages_in_twig,
    variable_at,
};

#[cfg(test)]
mod tests;
