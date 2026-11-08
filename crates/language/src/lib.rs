//! Built-in language registry: maps language ids and file extensions to
//! parsers.

use std::sync::Arc;

use swlsp_syntax::Parse;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum LanguageId {
    JavaScript,
    Json,
    Php,
    Scss,
    Twig,
    Vue,
    Xml,
    Yaml,
}

impl LanguageId {
    pub const ALL: [LanguageId; 8] = [
        LanguageId::JavaScript,
        LanguageId::Json,
        LanguageId::Php,
        LanguageId::Scss,
        LanguageId::Twig,
        LanguageId::Vue,
        LanguageId::Xml,
        LanguageId::Yaml,
    ];

    pub fn as_str(self) -> &'static str {
        match self {
            LanguageId::JavaScript => "javascript",
            LanguageId::Json => "json",
            LanguageId::Php => "php",
            LanguageId::Scss => "scss",
            LanguageId::Twig => "twig",
            LanguageId::Vue => "vue",
            LanguageId::Xml => "xml",
            LanguageId::Yaml => "yaml",
        }
    }

    pub fn from_id(id: &str) -> Option<Self> {
        Self::ALL
            .into_iter()
            .find(|language| language.as_str() == id)
    }

    pub fn extensions(self) -> &'static [&'static str] {
        match self {
            LanguageId::JavaScript => &["js", "ts"],
            LanguageId::Json => &["json"],
            LanguageId::Php => &["php"],
            LanguageId::Scss => &["scss"],
            LanguageId::Twig => &["twig", "html"],
            LanguageId::Vue => &["vue"],
            LanguageId::Xml => &["xml", "xlf", "xliff"],
            LanguageId::Yaml => &["yaml", "yml"],
        }
    }

    /// Language for a path by its (case-insensitive) extension.
    pub fn from_path(path: &str) -> Option<Self> {
        let extension = path.rsplit_once('.')?.1.to_ascii_lowercase();
        Self::ALL
            .into_iter()
            .find(|language| language.extensions().contains(&extension.as_str()))
    }

    /// Parses `source` with the language's native lossless parser.
    pub fn parse(self, source: &str) -> Option<Parse> {
        match self {
            LanguageId::Json => Some(swlsp_parser_json::parse(source)),
            LanguageId::JavaScript => Some(swlsp_parser_js::parse(source)),
            LanguageId::Xml => Some(swlsp_parser_xml::parse(source)),
            LanguageId::Scss => Some(swlsp_parser_scss::parse(source)),
            LanguageId::Php => Some(swlsp_parser_php::parse(source)),
            LanguageId::Twig => Some(swlsp_parser_twig::parse(source)),
            LanguageId::Yaml => Some(swlsp_parser_yaml::parse(source)),
            LanguageId::Vue => Some(swlsp_parser_vue::parse(source)),
        }
    }

    /// Like [`LanguageId::parse`], but the tree shares `source` instead of
    /// copying it, so callers that already hold the text in an `Arc<str>`
    /// keep a single copy.
    pub fn parse_shared(self, source: Arc<str>) -> Option<Parse> {
        match self {
            LanguageId::Json => Some(swlsp_parser_json::parse_shared(source)),
            LanguageId::JavaScript => Some(swlsp_parser_js::parse_shared(source)),
            LanguageId::Xml => Some(swlsp_parser_xml::parse_shared(source)),
            LanguageId::Scss => Some(swlsp_parser_scss::parse_shared(source)),
            LanguageId::Php => Some(swlsp_parser_php::parse_shared(source)),
            LanguageId::Twig => Some(swlsp_parser_twig::parse_shared(source)),
            LanguageId::Yaml => Some(swlsp_parser_yaml::parse_shared(source)),
            LanguageId::Vue => Some(swlsp_parser_vue::parse_shared(source)),
        }
    }
}
