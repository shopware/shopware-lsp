//! Shopware Administration completions: Vue component tags, props, events, slots,
//! scoped-slot and `v-for` locals in Twig templates, and registry, store,
//! `this` and runtime-container members in JavaScript/TypeScript.

mod components;
mod javascript;
mod provider;
mod scope;
mod slots;
mod twig;
mod values;

#[cfg(test)]
mod tests;

pub use provider::AdminCompletionProvider;
