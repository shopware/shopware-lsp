//! Hover providers.
//!
//! Every provider builds Markdown from byte ranges and converts byte
//! ranges to UTF-16 positions only when building the protocol range.

mod live_component_event;
mod twig;
mod twig_component;
mod twig_constant;
mod twig_enum;
mod twig_include_parameter;
mod twig_macro;
mod twig_render_block;
mod twig_versioning;

pub use live_component_event::LiveComponentEventHoverProvider;
pub use twig::TwigHoverProvider;
pub use twig_component::TwigComponentHoverProvider;
pub use twig_constant::TwigConstantHoverProvider;
pub use twig_enum::TwigEnumHoverProvider;
pub use twig_include_parameter::TwigIncludeParameterHoverProvider;
pub use twig_macro::TwigMacroHoverProvider;
pub use twig_render_block::TwigRenderBlockHoverProvider;
pub use twig_versioning::TwigVersioningHoverProvider;
// What the hover providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::hover::*;
