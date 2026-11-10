//! Code actions of the Administration domain.
//!
//! Every edit is a validated workspace edit built through `swlsp-rewrite`
//! or a protocol text edit derived from CST byte ranges; providers never
//! write files.

mod admin_context;
mod admin_twig_override;

pub use admin_context::{
    AdminContextProvider, EXTEND_ADMIN_COMPONENT_ACTION, OVERRIDE_ADMIN_METHOD_ACTION,
};
pub use admin_twig_override::{
    ADMIN_TWIG_OVERRIDE_ACTION, ADMIN_TWIG_OVERRIDE_COMMAND, AdminTwigOverrideProvider,
};
