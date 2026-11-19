//! Document formatting for the languages Biome has a formatter for: JSON,
//! JavaScript and TypeScript, CSS and SCSS.
//!
//! This crate is the one exception to the rule of a single parser stack: a
//! Biome formatter formats Biome's own syntax tree, so [`format`] parses the
//! source a second time with Biome's parser. That tree never leaves this
//! crate. It is text in, text out; indexes, diagnostics and providers keep
//! reading the native CST.

use biome_css_formatter::context::CssFormatOptions;
use biome_css_parser::CssParserOptions;
use biome_formatter::{
    Expand, FormatResult, IndentStyle, IndentWidth, LineEnding, LineWidth, QuoteStyle,
};
use biome_js_formatter::context::JsFormatOptions;
use biome_js_parser::JsParserOptions;
use biome_json_formatter::context::JsonFormatOptions;
use biome_json_parser::JsonParserOptions;
use biome_languages::{CssFileSource, JsFileSource};

/// A language with a formatter.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Language {
    Json,
    JavaScript,
    Jsx,
    TypeScript,
    Tsx,
    Css,
    Scss,
}

impl Language {
    /// The language of a file by its extension, without the dot.
    pub fn from_extension(extension: &str) -> Option<Self> {
        Some(match extension {
            "json" => Self::Json,
            "js" | "mjs" | "cjs" => Self::JavaScript,
            "jsx" => Self::Jsx,
            "ts" | "mts" | "cts" => Self::TypeScript,
            "tsx" => Self::Tsx,
            "css" => Self::Css,
            "scss" => Self::Scss,
            _ => return None,
        })
    }

    /// The language of the file at `path`, a path or a URI.
    pub fn from_path(path: &str) -> Option<Self> {
        let name = path.rsplit('/').next().unwrap_or(path);
        let (_, extension) = name.rsplit_once('.')?;
        Self::from_extension(extension)
    }
}

/// How a document is laid out. The defaults are those of Shopware's own
/// Prettier configuration.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Options {
    /// Spaces when set, tabs otherwise.
    pub insert_spaces: bool,
    /// The columns of one indentation level.
    pub indent_width: u8,
    /// The column lines are broken at.
    pub line_width: u16,
    /// Single quotes for strings in scripts and styles when set, double
    /// quotes otherwise.
    pub single_quotes: bool,
}

impl Default for Options {
    fn default() -> Self {
        Self {
            insert_spaces: true,
            indent_width: 4,
            line_width: 125,
            single_quotes: true,
        }
    }
}

impl Options {
    /// The options of an editor request: `tabSize` and `insertSpaces`.
    pub fn editor(tab_size: u32, insert_spaces: bool) -> Self {
        Self {
            insert_spaces,
            indent_width: u8::try_from(tab_size).unwrap_or(4),
            ..Self::default()
        }
    }

    fn indent_style(self) -> IndentStyle {
        if self.insert_spaces {
            IndentStyle::Space
        } else {
            IndentStyle::Tab
        }
    }

    fn indent_width(self) -> IndentWidth {
        IndentWidth::try_from(self.indent_width).unwrap_or_default()
    }

    fn quote_style(self) -> QuoteStyle {
        if self.single_quotes {
            QuoteStyle::Single
        } else {
            QuoteStyle::Double
        }
    }

    fn line_width(self) -> LineWidth {
        LineWidth::try_from(self.line_width).unwrap_or_default()
    }
}

/// `source` formatted as a whole document. `None` when it has syntax errors
/// or cannot be laid out: such a document is left as it is.
pub fn format(language: Language, source: &str, options: Options) -> Option<String> {
    let printed = match language {
        Language::Json => json(source, options),
        Language::JavaScript => script(source, JsFileSource::js_module(), options),
        Language::Jsx => script(source, JsFileSource::jsx(), options),
        Language::TypeScript => script(source, JsFileSource::ts(), options),
        Language::Tsx => script(source, JsFileSource::tsx(), options),
        Language::Css => style(source, CssFileSource::css(), options),
        Language::Scss => style(source, CssFileSource::scss(), options),
    };
    printed?.ok()
}

/// Comments and trailing commas are read in every JSON file, so that
/// `tsconfig.json` and its like are formatted too. Objects and arrays have
/// one member per line, the way Composer and npm write their files.
fn json(source: &str, options: Options) -> Option<FormatResult<String>> {
    let parse = biome_json_parser::parse_json(
        source,
        JsonParserOptions::default()
            .with_allow_comments()
            .with_allow_trailing_commas(),
    );
    if parse.has_errors() {
        return None;
    }
    let format_options = JsonFormatOptions::default()
        .with_indent_style(options.indent_style())
        .with_indent_width(options.indent_width())
        .with_line_width(options.line_width())
        .with_line_ending(LineEnding::Lf)
        .with_expand(Expand::Always);
    Some(
        biome_json_formatter::format_node(format_options, &parse.syntax())
            .and_then(|formatted| Ok(formatted.print()?.into_code())),
    )
}

fn script(
    source: &str,
    file_source: JsFileSource,
    options: Options,
) -> Option<FormatResult<String>> {
    let parse = biome_js_parser::parse(source, file_source, JsParserOptions::default());
    if parse.has_errors() {
        return None;
    }
    let format_options = JsFormatOptions::new(file_source)
        .with_indent_style(options.indent_style())
        .with_indent_width(options.indent_width())
        .with_line_width(options.line_width())
        .with_line_ending(LineEnding::Lf)
        .with_quote_style(options.quote_style());
    Some(
        biome_js_formatter::format_node(format_options, &parse.syntax(), Vec::new())
            .and_then(|formatted| Ok(formatted.print()?.into_code())),
    )
}

fn style(
    source: &str,
    file_source: CssFileSource,
    options: Options,
) -> Option<FormatResult<String>> {
    let parse = biome_css_parser::parse_css(source, file_source, CssParserOptions::default());
    if parse.has_errors() {
        return None;
    }
    let format_options = CssFormatOptions::new(file_source)
        .with_indent_style(options.indent_style())
        .with_indent_width(options.indent_width())
        .with_line_width(options.line_width())
        .with_line_ending(LineEnding::Lf)
        .with_quote_style(options.quote_style());
    Some(
        biome_css_formatter::format_node(format_options, &parse.syntax())
            .and_then(|formatted| Ok(formatted.print()?.into_code())),
    )
}

#[cfg(test)]
mod tests;
