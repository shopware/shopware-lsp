//! Shopware block versioning: upstream block resolution and version
//! comments (port of `versioning_service.go` and `block_override.go`).

use std::collections::{BTreeMap, HashMap, HashSet};
use std::sync::Arc;

use swlsp_syntax::{SyntaxNode, TextRange};

use crate::indexer::TwigIndexer;
use crate::parser::{TwigBlock, TwigBlockHash, TwigFile, parse_twig};
use crate::path::{
    clean, dir, format_version_comment, is_storefront_template, is_upstream_template, same_path,
};

const MAXIMUM_EXTENDS_DEPTH: usize = 10;

/// The statically resolvable parents of one overridden block.
/// `parent_resolved` distinguishes a missing block from a missing checkout:
/// removal diagnostics are only sound in the former case.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct UpstreamResolution {
    pub candidates: Vec<TwigBlockHash>,
    pub chain_paths: Vec<String>,
    pub parent_resolved: bool,
}

pub struct VersioningService {
    root: String,
    index: Option<Arc<TwigIndexer>>,
    fallback_version: String,
}

impl VersioningService {
    pub fn new(root: &str, index: Option<Arc<TwigIndexer>>, fallback_version: &str) -> Self {
        VersioningService {
            root: clean(root),
            index,
            fallback_version: fallback_version.trim().to_owned(),
        }
    }

    pub fn resolve(
        &self,
        file: &TwigFile,
        block_name: &str,
    ) -> swlsp_indexer::Result<UpstreamResolution> {
        let Some(index) = &self.index else {
            return Ok(UpstreamResolution::default());
        };
        if block_name.is_empty() {
            return Ok(UpstreamResolution::default());
        }
        let (chain, parent_resolved) = self.extends_chain(file)?;
        let hashes = index.get_twig_block_hashes(block_name)?;
        Ok(resolve_block(file, &hashes, &chain, parent_resolved))
    }

    /// Resolves every declared block while reading the extends chain once.
    pub fn resolve_blocks(
        &self,
        file: &TwigFile,
    ) -> swlsp_indexer::Result<BTreeMap<String, UpstreamResolution>> {
        let mut result = BTreeMap::new();
        let Some(index) = &self.index else {
            return Ok(result);
        };
        if file.blocks.is_empty() {
            return Ok(result);
        }
        let (chain, parent_resolved) = self.extends_chain(file)?;
        for block_name in file.blocks.keys() {
            let hashes = index.get_twig_block_hashes(block_name)?;
            result.insert(
                block_name.clone(),
                resolve_block(file, &hashes, &chain, parent_resolved),
            );
        }
        Ok(result)
    }

    pub fn resolve_document(
        &self,
        file_path: &str,
        source: &str,
        block_name: &str,
    ) -> swlsp_indexer::Result<(TwigFile, TwigBlock, UpstreamResolution)> {
        let file = parse_twig(file_path, source);
        let Some(block) = file.blocks.get(block_name).cloned() else {
            return Err(swlsp_indexer::Error::msg(format!(
                "twig block {} is no longer available",
                swlsp_php_types::go_quote(block_name)
            )));
        };
        let resolution = self.resolve(&file, block_name)?;
        Ok((file, block, resolution))
    }

    /// The edit inserting or replacing the version comment of a block.
    pub fn version_comment_edit(
        &self,
        file_path: &str,
        source: &str,
        block_name: &str,
    ) -> swlsp_indexer::Result<(TextRange, String)> {
        let (_, block, resolution) = self.resolve_document(file_path, source, block_name)?;
        let Some(upstream) = resolution.candidates.first() else {
            return Err(swlsp_indexer::Error::msg(format!(
                "upstream Twig block {} is unavailable",
                swlsp_php_types::go_quote(block_name)
            )));
        };
        let comment = format_version_comment(
            &upstream.hash,
            &self.version_for_path(&upstream.absolute_path),
        );
        if block.has_versioning_comment
            && let Some(range) = block.version_comment_range
        {
            return Ok((range, comment.trim_end_matches('\n').to_owned()));
        }
        let start = u32::from(block.range.start()) as usize;
        if start > source.len() {
            return Err(swlsp_indexer::Error::msg(
                "twig block range is outside the document",
            ));
        }
        let mut line_start = source[..start].rfind('\n').map_or(0, |index| index + 1);
        let mut indent = &source[line_start..start];
        if !indent.trim_matches([' ', '\t']).is_empty() {
            line_start = start;
            indent = "";
        }
        let at = TextRange::empty((line_start as u32).into());
        Ok((at, format!("{indent}{comment}")))
    }

    pub fn other_locations(
        &self,
        block_name: &str,
        current_path: &str,
    ) -> swlsp_indexer::Result<Vec<TwigBlockHash>> {
        let Some(index) = &self.index else {
            return Ok(Vec::new());
        };
        let mut result: Vec<TwigBlockHash> = index
            .get_twig_block_hashes(block_name)?
            .into_iter()
            .filter(|hash| {
                !same_path(&hash.absolute_path, current_path) && !hash.has_versioning_comment
            })
            .collect();
        result.sort_by(|left, right| left.absolute_path.cmp(&right.absolute_path));
        Ok(result)
    }

    pub fn block_at_path(
        &self,
        block_name: &str,
        path: &str,
    ) -> swlsp_indexer::Result<Option<TwigBlockHash>> {
        let Some(index) = &self.index else {
            return Ok(None);
        };
        Ok(index
            .get_twig_block_hashes(block_name)?
            .into_iter()
            .find(|hash| same_path(&hash.absolute_path, path)))
    }

    /// The installed version of the package owning `path`: Composer
    /// metadata first, then the nearest `composer.json` version, then the
    /// fallback for Storefront templates.
    pub fn version_for_path(&self, path: &str) -> String {
        let path = clean(path);
        if let Some(project) = self
            .index
            .as_ref()
            .and_then(|index| index.php_index())
            .and_then(|php| php.project())
        {
            let mut best_root = String::new();
            let mut version = String::new();
            for dependency in &project.dependencies {
                let root = clean(&dependency.install_path.to_string_lossy());
                if path_inside(&root, &path) && root.len() > best_root.len() {
                    version.clone_from(&dependency.version);
                    best_root = root;
                }
            }
            if !version.is_empty() {
                return version;
            }
        }
        let search_root = package_search_root(&self.root, &path);
        let mut directory = dir(&path);
        while path_inside(&search_root, &directory) {
            if let Ok(content) = std::fs::read(format!("{directory}/composer.json")) {
                #[derive(serde::Deserialize)]
                struct Composer {
                    #[serde(default)]
                    version: serde_json::Value,
                }
                if let Ok(composer) = serde_json::from_slice::<Composer>(&content)
                    && let Some(version) = composer.version.as_str()
                    && !version.trim().is_empty()
                {
                    return version.trim().to_owned();
                }
                break;
            }
            let parent = dir(&directory);
            if same_path(&directory, &search_root) || parent == directory {
                break;
            }
            directory = parent;
        }
        if is_storefront_template(&path) {
            return self.fallback_version.clone();
        }
        String::new()
    }

    fn extends_chain(&self, file: &TwigFile) -> swlsp_indexer::Result<(Vec<String>, bool)> {
        let Some(index) = &self.index else {
            return Ok((Vec::new(), false));
        };
        if file.extends_file.is_empty() {
            return Ok((Vec::new(), false));
        }
        let mut visited: HashSet<String> = HashSet::from([clean(&file.path)]);
        let mut current = file.clone();
        let mut chain = Vec::new();
        let mut parent_resolved = false;
        for depth in 0..MAXIMUM_EXTENDS_DEPTH {
            if current.extends_file.is_empty() {
                break;
            }
            let files = index.get_twig_files_by_rel_path(&current.extends_file)?;
            let Some(parent) =
                choose_parent_template(files, &current.extends_file, &current.path, &visited)
            else {
                break;
            };
            if depth == 0 {
                parent_resolved = true;
            }
            visited.insert(clean(&parent.path));
            chain.push(parent.path.clone());
            current = parent;
        }
        Ok((chain, parent_resolved))
    }
}

fn resolve_block(
    file: &TwigFile,
    hashes: &[TwigBlockHash],
    chain: &[String],
    parent_resolved: bool,
) -> UpstreamResolution {
    let mut resolution = UpstreamResolution {
        candidates: Vec::new(),
        chain_paths: chain.to_vec(),
        parent_resolved,
    };
    for path in chain {
        if let Some(hash) = hashes
            .iter()
            .find(|hash| same_path(&hash.absolute_path, path))
        {
            resolution.candidates.push(hash.clone());
        }
    }
    if !resolution.candidates.is_empty() {
        return resolution;
    }
    let current_view_path = template_view_path(&file.path);
    resolution.candidates = hashes
        .iter()
        .filter(|hash| {
            !same_path(&hash.absolute_path, &file.path)
                && !hash.has_versioning_comment
                && template_view_path(&hash.relative_path) == current_view_path
        })
        .cloned()
        .collect();
    resolution.candidates.sort_by(|left, right| {
        is_storefront_template(&right.absolute_path)
            .cmp(&is_storefront_template(&left.absolute_path))
            .then_with(|| {
                is_upstream_template(&right.absolute_path)
                    .cmp(&is_upstream_template(&left.absolute_path))
            })
            .then_with(|| left.absolute_path.cmp(&right.absolute_path))
    });
    resolution
}

fn choose_parent_template(
    files: Vec<TwigFile>,
    reference: &str,
    current_path: &str,
    visited: &HashSet<String>,
) -> Option<TwigFile> {
    let namespace = normalized_bundle_name(&template_namespace(reference));
    let storefront_namespace = template_namespace(reference).eq_ignore_ascii_case("Storefront");
    let mut candidates: Vec<TwigFile> = files
        .into_iter()
        .filter(|file| {
            let path = clean(&file.path);
            !same_path(&path, current_path) && !visited.contains(&path)
        })
        .collect();
    candidates.sort_by(|left, right| {
        let left_bundle = normalized_bundle_name(&left.bundle_name) == namespace;
        let right_bundle = normalized_bundle_name(&right.bundle_name) == namespace;
        right_bundle
            .cmp(&left_bundle)
            .then_with(|| {
                if storefront_namespace {
                    is_storefront_template(&right.path).cmp(&is_storefront_template(&left.path))
                } else {
                    std::cmp::Ordering::Equal
                }
            })
            .then_with(|| is_upstream_template(&right.path).cmp(&is_upstream_template(&left.path)))
            .then_with(|| left.path.cmp(&right.path))
    });
    candidates.into_iter().next()
}

fn template_namespace(reference: &str) -> String {
    let reference = reference.trim();
    let reference = reference.strip_prefix('@').unwrap_or(reference);
    reference
        .split_once('/')
        .map(|(namespace, _)| namespace.to_owned())
        .unwrap_or_default()
}

fn normalized_bundle_name(value: &str) -> String {
    swlsp_php_types::go_to_lower(value).replace(['-', '_', '\\', '/'], "")
}

fn template_view_path(value: &str) -> String {
    let marker = "/Resources/views/";
    if let Some(index) = value.rfind(marker) {
        let rest = &value[index + marker.len()..];
        return rest.strip_prefix('/').unwrap_or(rest).to_owned();
    }
    let mut value = value.strip_prefix('@').unwrap_or(value);
    if let Some(index) = value.find('/') {
        value = &value[index + 1..];
    }
    value.strip_prefix('/').unwrap_or(value).to_owned()
}

/// Go `filepath.Rel` containment.
fn path_inside(root: &str, candidate: &str) -> bool {
    let root = clean(root);
    let candidate = clean(candidate);
    if root == candidate {
        return true;
    }
    if root.starts_with('/') != candidate.starts_with('/') {
        return false;
    }
    if root == "." {
        return !candidate.starts_with("../") && candidate != "..";
    }
    let prefix = if root.ends_with('/') {
        root.clone()
    } else {
        format!("{root}/")
    };
    candidate.starts_with(&prefix)
}

fn package_search_root(workspace_root: &str, path: &str) -> String {
    let path = clean(path);
    let marker = "/custom/plugins/";
    let Some(index) = path.find(marker) else {
        return clean(workspace_root);
    };
    let remainder = &path[index + marker.len()..];
    let name = remainder.split('/').next().unwrap_or_default();
    if name.is_empty() {
        return clean(workspace_root);
    }
    format!("{}{name}", &path[..index + marker.len()])
}

// --- block overrides ---

/// The nearest resolved parent when the current block is an exact copy.
/// Fallback candidates are excluded: they may share a view path without
/// participating in the template's extends chain.
pub fn redundant_block_override(
    block: &TwigBlock,
    resolution: &UpstreamResolution,
) -> Option<TwigBlockHash> {
    if !resolution.parent_resolved || block.hash.is_empty() || block.text.is_empty() {
        return None;
    }
    let parent = resolution.candidates.first()?;
    if parent.hash != block.hash || parent.text != block.text {
        return None;
    }
    resolution
        .chain_paths
        .iter()
        .any(|path| same_path(&parent.absolute_path, path))
        .then(|| parent.clone())
}

/// The exact source owned by a block's CST body.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct BlockBodySnapshot {
    pub text: String,
    pub range: TextRange,
}

/// Block bodies collected in one traversal, keyed by each declaration's
/// trimmed range.
pub fn block_bodies(root: &SyntaxNode, source: &str) -> HashMap<TextRange, BlockBodySnapshot> {
    use swlsp_parser_twig::ast::{AstNode, TwigBlock as TwigBlockNode};
    use swlsp_syntax::SyntaxNodeExt;
    let mut bodies = HashMap::new();
    for node in root.descendants() {
        let Some(body) = TwigBlockNode::cast(node.clone()).and_then(|block| block.body()) else {
            continue;
        };
        let range = body.syntax().text_range();
        let Some(text) = source.get(usize::from(range.start())..usize::from(range.end())) else {
            continue;
        };
        bodies.insert(
            node.range_trimmed_trivia(),
            BlockBodySnapshot {
                text: text.to_owned(),
                range,
            },
        );
    }
    bodies
}

/// The body of one persisted block, relative to the block text.
pub fn parse_block_body(block_text: &str) -> Option<(String, TextRange)> {
    let parse = swlsp_parser_twig::parse(block_text);
    let full = TextRange::new(0.into(), (block_text.len() as u32).into());
    block_bodies(&parse.root(), block_text)
        .remove(&full)
        .map(|body| (body.text, body.range))
}

/// Whether the body is the canonical delegation emitted by the
/// redundant-block quick fix.
pub fn is_parent_delegation(body: &str) -> bool {
    body.trim() == "{{ parent() }}"
}
