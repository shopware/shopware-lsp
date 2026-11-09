//! Template reference, override, inheritance and block relationship lenses.

use std::collections::{BTreeMap, HashMap, HashSet};
use std::sync::Arc;

use swlsp_lsp::{CodeLensProvider, CodeLensRequest, Context, ProviderResult};
use swlsp_lsp_protocol::{self as protocol, Range};
use swlsp_syntax::LineIndex;
use swlsp_textutil::lowercase;
use swlsp_twig::{TemplateReference, TemplateReferenceKind, TwigFile, TwigIndexer};
use swlsp_uriutil::filepath;

use crate::{
    cancelled, counted_title, related_lens, related_protocol_range, related_source_line,
    related_target, same_path, sort_related_code_lenses, unique_related_targets,
};

pub struct TwigCodeLensProvider {
    twig: Option<Arc<TwigIndexer>>,
}

impl TwigCodeLensProvider {
    pub fn new(twig: Option<Arc<TwigIndexer>>) -> Self {
        Self { twig }
    }
}

impl CodeLensProvider for TwigCodeLensProvider {
    fn get_code_lenses(
        &self,
        ctx: &Context,
        request: &CodeLensRequest,
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let Some(twig) = &self.twig else {
            return Ok(Vec::new());
        };
        let document = &request.document;
        let uri = &request.params.text_document.uri;
        let Some(tree) = document.tree() else {
            return Ok(Vec::new());
        };
        if !uri.to_ascii_lowercase().ends_with(".twig") {
            return Ok(Vec::new());
        }
        let path = swlsp_uriutil::path(uri)?;
        let lines = document.line_index();
        let current = swlsp_twig::parse_twig_tree(&path, Some(tree), lines);
        let template_names = swlsp_twig::template_names(&path);
        let references = twig.get_template_references(&template_names)?;
        let overrides = template_overrides(twig, &path, &template_names)?;
        let descendants = descendant_templates(twig, ctx, &path, &template_names)?;

        let mut result = template_reference_code_lenses(&path, &references);
        let override_targets = twig_file_targets(&overrides);
        if !override_targets.is_empty() {
            result.push(related_lens(
                Range::default(),
                counted_title(
                    "Open template override",
                    "Open ",
                    " template overrides",
                    override_targets.len(),
                ),
                override_targets,
            ));
        }
        result.extend(block_relationship_code_lenses(
            twig,
            &current,
            &descendants,
            lines,
        )?);
        sort_related_code_lenses(&mut result);
        Ok(result)
    }

    fn resolve_code_lens(
        &self,
        _ctx: &Context,
        lens: &protocol::CodeLens,
    ) -> ProviderResult<Option<protocol::CodeLens>> {
        Ok(Some(lens.clone()))
    }
}

fn is_twig(path: &str) -> bool {
    filepath::ext(path).eq_ignore_ascii_case(".twig")
}

fn template_reference_code_lenses(
    current_path: &str,
    references: &[TemplateReference],
) -> Vec<protocol::CodeLens> {
    let mut extending = Vec::new();
    let mut usages = Vec::new();
    for reference in references {
        if same_path(&reference.file_path, current_path) || !is_twig(&reference.file_path) {
            continue;
        }
        let target = related_target(
            &reference.file_path,
            related_source_line(&reference.file_path, reference.range.start().into()),
        );
        match reference.kind {
            TemplateReferenceKind::Extends => extending.push(target),
            TemplateReferenceKind::Include
            | TemplateReferenceKind::Embed
            | TemplateReferenceKind::Import
            | TemplateReferenceKind::Use
            | TemplateReferenceKind::FormTheme
            | TemplateReferenceKind::Source
            | TemplateReferenceKind::Block => usages.push(target),
            _ => {}
        }
    }
    let extending = unique_related_targets(extending);
    let usages = unique_related_targets(usages);
    let mut result = Vec::new();
    if !extending.is_empty() {
        result.push(related_lens(
            Range::default(),
            counted_title(
                "Open extending template",
                "Open ",
                " extending templates",
                extending.len(),
            ),
            extending,
        ));
    }
    if !usages.is_empty() {
        result.push(related_lens(
            Range::default(),
            counted_title(
                "Open template reference",
                "Open ",
                " template references",
                usages.len(),
            ),
            usages,
        ));
    }
    result
}

fn template_overrides(
    twig: &TwigIndexer,
    current_path: &str,
    template_names: &[String],
) -> ProviderResult<Vec<TwigFile>> {
    let mut result = Vec::new();
    for template in template_names {
        for file in twig.get_twig_files_by_rel_path(template)? {
            if !same_path(&file.path, current_path) {
                result.push(file);
            }
        }
    }
    Ok(unique_twig_files(result))
}

fn descendant_templates(
    twig: &TwigIndexer,
    ctx: &Context,
    current_path: &str,
    template_names: &[String],
) -> ProviderResult<Vec<TwigFile>> {
    let mut visited_paths = HashSet::from([filepath::clean(current_path)]);
    let mut visited_names = HashSet::new();
    let mut queue = unique_twig_template_names(template_names.to_vec());
    let mut result = Vec::new();
    let mut depth = 0;
    while depth < 32 && !queue.is_empty() {
        cancelled(ctx)?;
        let mut pending = Vec::new();
        for name in queue {
            if visited_names.insert(lowercase(name.trim())) {
                pending.push(name);
            }
        }
        if pending.is_empty() {
            break;
        }
        let mut next = Vec::new();
        for reference in twig.get_template_references(&pending)? {
            if reference.kind != TemplateReferenceKind::Extends || !is_twig(&reference.file_path) {
                continue;
            }
            if !visited_paths.insert(filepath::clean(&reference.file_path)) {
                continue;
            }
            let Some(file) = twig.get_twig_file_by_path(&reference.file_path)? else {
                continue;
            };
            next.extend(swlsp_twig::template_names(&file.path));
            result.push(file);
        }
        queue = unique_twig_template_names(next);
        depth += 1;
    }
    Ok(unique_twig_files(result))
}

fn block_relationship_code_lenses(
    twig: &TwigIndexer,
    current: &TwigFile,
    descendants: &[TwigFile],
    lines: &LineIndex,
) -> ProviderResult<Vec<protocol::CodeLens>> {
    let parent_blocks = if current.extends_file.is_empty() {
        Vec::new()
    } else {
        twig.get_template_blocks(std::slice::from_ref(&current.extends_file))?
    };
    let descendant_paths: HashSet<String> = descendants
        .iter()
        .map(|file| filepath::clean(&file.path))
        .collect();
    let mut parents_by_name: HashMap<String, Vec<String>> = HashMap::new();
    for block in &parent_blocks {
        if same_path(&block.file_path, &current.path)
            || descendant_paths.contains(&filepath::clean(&block.file_path))
        {
            continue;
        }
        parents_by_name
            .entry(lowercase(&block.name))
            .or_default()
            .push(related_target(&block.file_path, block.line));
    }
    let mut implementations_by_name: HashMap<String, Vec<String>> = HashMap::new();
    for file in descendants {
        if same_path(&file.path, &current.path) {
            continue;
        }
        for block in file.blocks.values() {
            implementations_by_name
                .entry(lowercase(&block.name))
                .or_default()
                .push(related_target(&file.path, block.line));
        }
    }
    let mut result = Vec::new();
    for block in current.blocks.values() {
        let key = lowercase(&block.name);
        let range = related_protocol_range(block.name_range, lines);
        let parents =
            unique_related_targets(parents_by_name.get(&key).cloned().unwrap_or_default());
        if !parents.is_empty() {
            result.push(related_lens(
                range,
                counted_title(
                    "Open parent block",
                    "Open ",
                    " parent blocks",
                    parents.len(),
                ),
                parents,
            ));
        }
        let implementations = unique_related_targets(
            implementations_by_name
                .get(&key)
                .cloned()
                .unwrap_or_default(),
        );
        if !implementations.is_empty() {
            result.push(related_lens(
                range,
                counted_title(
                    "Open block implementation",
                    "Open ",
                    " block implementations",
                    implementations.len(),
                ),
                implementations,
            ));
        }
    }
    Ok(result)
}

fn unique_twig_files(files: Vec<TwigFile>) -> Vec<TwigFile> {
    let mut by_path: BTreeMap<String, TwigFile> = BTreeMap::new();
    for file in files {
        if file.path.is_empty() {
            continue;
        }
        by_path.entry(filepath::clean(&file.path)).or_insert(file);
    }
    let mut result: Vec<TwigFile> = by_path.into_values().collect();
    result.sort_by(|left, right| left.path.cmp(&right.path));
    result
}

fn unique_twig_template_names(names: Vec<String>) -> Vec<String> {
    let mut seen = HashSet::with_capacity(names.len());
    let mut result: Vec<String> = names
        .into_iter()
        .filter_map(|name| {
            let name = name.trim().to_owned();
            (!name.is_empty() && seen.insert(lowercase(&name))).then_some(name)
        })
        .collect();
    result.sort();
    result
}

fn twig_file_targets(files: &[TwigFile]) -> Vec<String> {
    unique_related_targets(
        files
            .iter()
            .map(|file| related_target(&file.path, 1))
            .collect(),
    )
}
