//! The snippet file inspection and its fix: every key the main language
//! file defines and this file lacks is added, with the text of the main
//! file to translate.

use std::sync::Arc;

use serde::{Deserialize, Serialize};
use swlsp_lsp::{
    BoxError, Context, FixContext, FixId, FixPresentation, FixResolution, Inspection, QuickFix,
    RewriteQuickFix, bind_fix,
};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_json::syntax::{JSON_ARRAY, JSON_OBJECT, TK_CLOSE_BRACE, TK_OPEN_BRACE};
use swlsp_rewrite::{Builder, DocumentPlan, HandleError, WorkspacePlan};
use swlsp_syntax::{SyntaxNode, TextRange, TextSize};
use swlsp_uriutil::filepath;

use crate::diagnostics::{
    DeclaredAnalyzer, SNIPPET_FILE_MISSING_CODE, SnippetFileAnalyzer, is_snippet_file,
    main_snippet_file, pair_key, pair_value, pairs, root_object,
};
use crate::inspections::BoundInspection;

pub const ADD_MISSING_SNIPPETS_FIX_ID: &str = "add-missing-snippets";

/// The indentation of a file that shows none.
const DEFAULT_INDENT: &str = "    ";

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(default)]
struct AddMissingSnippetsPayload {
    /// The name of the main language file.
    main: String,
}

pub fn new_snippet_files() -> Arc<dyn Inspection> {
    Arc::new(BoundInspection {
        definition: SnippetFileAnalyzer::INSPECTION.definition(),
        analyzers: vec![Arc::new(SnippetFileAnalyzer::new())],
        fixes: vec![Arc::new(AddMissingSnippetsFix)],
        bind: Some(Arc::new(|code, payload| {
            let main = payload.get("main").and_then(serde_json::Value::as_str);
            match main {
                Some(main) if code.as_str() == SNIPPET_FILE_MISSING_CODE => vec![bind_fix(
                    ADD_MISSING_SNIPPETS_FIX_ID,
                    AddMissingSnippetsPayload {
                        main: main.to_owned(),
                    },
                )],
                _ => Vec::new(),
            }
        })),
    })
}

/// Adds every key of the main language file the document lacks, wherever
/// in the file it is missing.
struct AddMissingSnippetsFix;

impl QuickFix for AddMissingSnippetsFix {
    fn id(&self) -> FixId {
        FixId::from(ADD_MISSING_SNIPPETS_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: AddMissingSnippetsPayload = fix.fix_payload()?;
        if payload.main.is_empty() {
            return Ok(None);
        }
        Ok(Some(FixPresentation {
            title: format!("Shopware: Add all missing snippets from {}", payload.main),
            kind: Some(protocol::CodeActionKind::QUICKFIX),
            // The added texts are in the main language: a person translates
            // them, so `check --fix` does not add them on its own.
            preferred: false,
            resolution: FixResolution::Lazy,
        }))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for AddMissingSnippetsFix {
    fn build(&self, _ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let document = &fix.document;
        let path = filepath::clean(&swlsp_uriutil::path(&document.uri)?);
        if !is_snippet_file(&filepath::to_slash(&path)) {
            return Err(Box::new(HandleError::Stale));
        }
        let main_path = main_snippet_file(&path).ok_or(HandleError::Stale)?;
        let main_source = std::fs::read_to_string(&main_path)
            .map_err(|error| format!("read {main_path}: {error}"))?;
        let source = document.source();
        let edits = missing_snippet_edits(&main_source, source).ok_or(HandleError::Stale)?;
        let mut builder = Builder::new(source);
        for (range, text) in edits {
            builder.replace_range(range, text)?;
        }
        Ok(WorkspacePlan {
            documents: vec![
                DocumentPlan::new(
                    document.uri.clone(),
                    Some(document.version),
                    document.source_arc().clone(),
                    builder.finish()?,
                )
                .with_line_index(document.line_index_arc().clone()),
            ],
            ..WorkspacePlan::default()
        })
    }
}

/// The edits of `source` that add every key the main language file defines
/// and `source` lacks, each beside the key it follows in the main file.
/// `None` when nothing is missing or one of the two is no object.
pub(crate) fn missing_snippet_edits(
    main_source: &str,
    source: &str,
) -> Option<Vec<(TextRange, String)>> {
    let main_root = swlsp_parser_json::parse(main_source).root();
    let root = swlsp_parser_json::parse(source).root();
    let (main, object) = (root_object(&main_root)?, root_object(&root)?);
    let unit = [(&object, source), (&main, main_source)]
        .into_iter()
        .find_map(|(object, source)| {
            let (_, key) = pairs(object).find_map(|pair| pair_key(&pair))?;
            Some(line_indent(source, key.start().into())).filter(|indent| !indent.is_empty())
        })
        .unwrap_or(DEFAULT_INDENT);
    let mut fill = Fill {
        source,
        unit,
        edits: Vec::new(),
    };
    fill.object(&main, &object);
    (!fill.edits.is_empty()).then_some(fill.edits)
}

struct Fill<'a> {
    source: &'a str,
    /// One level of indentation.
    unit: &'a str,
    edits: Vec<(TextRange, String)>,
}

impl Fill<'_> {
    fn object(&mut self, main: &SyntaxNode, object: &SyntaxNode) {
        let own: Vec<(String, TextRange, SyntaxNode)> = pairs(object)
            .filter_map(|pair| {
                let (key, range) = pair_key(&pair)?;
                Some((key, range, pair))
            })
            .collect();
        // What stands before a key of this object: a line break and its
        // indentation, or nothing in an object written on one line.
        let lead = match own.first() {
            Some((_, key, _)) => {
                let start = usize::from(key.start());
                let open = usize::from(object.text_range().start());
                if self.source[open..start].contains('\n') {
                    format!("\n{}", line_indent(self.source, start))
                } else {
                    String::new()
                }
            }
            None => format!(
                "\n{}{}",
                line_indent(self.source, brace(object, true).map_or(0, usize::from)),
                self.unit
            ),
        };
        // The new entries by the key of this object they follow.
        let mut groups: Vec<(Option<usize>, Vec<String>)> = Vec::new();
        let mut last = None;
        for pair in pairs(main) {
            let (Some((key, _)), Some(main_value)) = (pair_key(&pair), pair_value(&pair)) else {
                continue;
            };
            if let Some(index) = own.iter().position(|(name, _, _)| *name == key) {
                last = Some(index);
                if let Some(value) = pair_value(&own[index].2)
                    && value.kind() == JSON_OBJECT
                    && main_value.kind() == JSON_OBJECT
                {
                    self.object(&main_value, &value);
                }
                continue;
            }
            let entry = format!("\"{key}\": {}", render(&main_value, &lead, self.unit));
            match groups.iter_mut().find(|(anchor, _)| *anchor == last) {
                Some((_, entries)) => entries.push(entry),
                None => groups.push((last, vec![entry])),
            }
        }
        let between = if lead.is_empty() { " " } else { lead.as_str() };
        for (anchor, entries) in groups {
            match (anchor, own.first()) {
                (Some(index), _) => {
                    let end = own[index].2.text_range().end();
                    let text: String = entries
                        .iter()
                        .map(|entry| format!(",{between}{entry}"))
                        .collect();
                    self.edits.push((TextRange::new(end, end), text));
                }
                (None, Some((_, key, _))) => {
                    // Before the quote of the first key.
                    let start = key.start() - TextSize::from(1);
                    let text: String = entries
                        .iter()
                        .map(|entry| format!("{entry},{between}"))
                        .collect();
                    self.edits.push((TextRange::new(start, start), text));
                }
                (None, None) => {
                    let (Some(open), Some(close)) = (brace(object, true), brace(object, false))
                    else {
                        continue;
                    };
                    let inside = TextRange::new(open + TextSize::from(1), close);
                    let closing = line_indent(self.source, open.into());
                    let entries: Vec<String> = entries
                        .iter()
                        .map(|entry| format!("{lead}{entry}"))
                        .collect();
                    self.edits
                        .push((inside, format!("{}\n{closing}", entries.join(","))));
                }
            }
        }
    }
}

/// Where the opening or the closing brace of `object` starts.
fn brace(object: &SyntaxNode, opening: bool) -> Option<TextSize> {
    let kind = if opening {
        TK_OPEN_BRACE
    } else {
        TK_CLOSE_BRACE
    };
    object
        .children_with_tokens()
        .filter_map(|element| element.into_token())
        .find(|token| token.kind() == kind)
        .map(|token| token.text_range().start())
}

/// The indentation of the line `offset` is on.
fn line_indent(source: &str, offset: usize) -> &str {
    let start = source[..offset].rfind('\n').map_or(0, |index| index + 1);
    let line = &source[start..];
    let end = line
        .find(|character: char| character != ' ' && character != '\t')
        .unwrap_or(line.len());
    &line[..end]
}

/// A value of the main file as it is written after `lead`, the line break
/// and indentation of its key (empty on one line).
fn render(value: &SyntaxNode, lead: &str, unit: &str) -> String {
    let inner = if lead.is_empty() {
        String::new()
    } else {
        format!("{lead}{unit}")
    };
    let (open, close, entries): (char, char, Vec<String>) = match value.kind() {
        JSON_OBJECT => (
            '{',
            '}',
            pairs(value)
                .filter_map(|pair| {
                    let (key, _) = pair_key(&pair)?;
                    let value = pair_value(&pair)?;
                    Some(format!("\"{key}\": {}", render(&value, &inner, unit)))
                })
                .collect(),
        ),
        JSON_ARRAY => (
            '[',
            ']',
            value
                .children()
                .map(|element| render(&element, &inner, unit))
                .collect(),
        ),
        _ => return value.text().to_string().trim().to_owned(),
    };
    if entries.is_empty() {
        return format!("{open}{close}");
    }
    if lead.is_empty() {
        return format!("{open}{}{close}", entries.join(", "));
    }
    let entries: Vec<String> = entries
        .iter()
        .map(|entry| format!("{inner}{entry}"))
        .collect();
    format!("{open}{}{lead}{close}", entries.join(","))
}
