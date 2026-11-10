//! Code actions of the Twig domain.
//!
//! Every edit is a validated workspace edit built through `swlsp-rewrite`
//! or a protocol text edit derived from CST byte ranges; providers never
//! write files.

mod twig;
mod twig_extension_attribute;
mod twig_template_generator;
mod twig_translation_extract;

pub use twig::TwigCodeActionProvider;
pub use twig_extension_attribute::TwigExtensionAttributeCodeActionProvider;
pub use twig_template_generator::{
    GENERATE_TWIG_BLOCKS_ACTION, GENERATE_TWIG_BLOCKS_COMMAND, GENERATE_TWIG_EXTENDS_ACTION,
    GENERATE_TWIG_EXTENDS_COMMAND, TWIG_BLOCK_CANDIDATES_COMMAND, TWIG_EXTENDS_CANDIDATES_COMMAND,
    TwigTemplateGeneratorProvider,
};
pub use twig_translation_extract::{
    EXTRACT_TWIG_TRANSLATION_ACTION, GENERATE_TWIG_TRANSLATION_EXTRACTION_COMMAND,
    PREPARE_TWIG_TRANSLATION_EXTRACTION_COMMAND, TwigTranslationExtractProvider,
};
