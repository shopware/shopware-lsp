//! Committed, content-addressed schema snapshots (port of Go `snapshot.go`).
//!
//! Snapshot IDs are SHA-256 digests of Go `json.Marshal` output, so the JSON
//! encoding goes through [`crate::gojson`] to stay compatible with snapshots
//! committed by the Go implementation.

use std::collections::{BTreeMap, HashMap};
use std::io::ErrorKind;

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use crate::gojson::{self, null_if_empty, nullable};
use crate::model::*;
use swlsp_uriutil::filepath;

pub const SNAPSHOT_FORMAT_VERSION: i64 = 2;
pub const SNAPSHOT_RELATIVE_DIRECTORY: &str = "src/Resources/shopware-lsp/schema";

pub type SnapshotKind = String;
pub const SNAPSHOT_MIGRATION: &str = "migration";
pub const SNAPSHOT_BASELINE: &str = "baseline";
pub const SNAPSHOT_MERGE: &str = "merge";

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct PluginIdentity {
    pub composer_name: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct MigrationReference {
    pub path: String,
    pub timestamp: i64,
    #[serde(rename = "sha256")]
    pub sha256: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Decision {
    pub kind: String,
    pub entity: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub from: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub to: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub reason: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct Snapshot {
    pub format_version: i64,
    pub id: String,
    #[serde(deserialize_with = "nullable", serialize_with = "null_if_empty")]
    pub parents: Vec<String>,
    pub kind: SnapshotKind,
    pub plugin: PluginIdentity,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub shopware_version: String,
    #[serde(deserialize_with = "nullable", serialize_with = "null_if_empty")]
    pub migrations: Vec<MigrationReference>,
    pub schema: Schema,
    #[serde(deserialize_with = "nullable", serialize_with = "null_if_empty")]
    pub decisions: Vec<Decision>,
}

impl Snapshot {
    fn normalized(&self, include_id: bool) -> Snapshot {
        let mut snapshot = self.clone();
        snapshot.format_version = SNAPSHOT_FORMAT_VERSION;
        if !include_id {
            snapshot.id.clear();
        }
        snapshot.parents.sort();
        snapshot.migrations.sort_by(|left, right| {
            left.timestamp
                .cmp(&right.timestamp)
                .then_with(|| left.path.cmp(&right.path))
        });
        snapshot.decisions.sort_by_cached_key(|decision| {
            format!(
                "{}:{}:{}:{}",
                decision.kind, decision.entity, decision.from, decision.to
            )
        });
        snapshot
    }

    pub fn seal(&self) -> Result<Snapshot> {
        let mut snapshot = self.normalized(false);
        let encoded = gojson::to_vec(&snapshot)?;
        snapshot.id = hex(&Sha256::digest(&encoded));
        Ok(snapshot.normalized(true))
    }
}

pub fn marshal_snapshot(snapshot: &Snapshot) -> Result<Vec<u8>> {
    let sealed = snapshot.seal()?;
    let mut encoded = gojson::to_vec_indent(&sealed)?;
    encoded.push(b'\n');
    Ok(encoded)
}

pub fn parse_snapshot(source: &[u8]) -> Result<Snapshot> {
    let snapshot: Snapshot = gojson::from_slice(source)?;
    if snapshot.format_version != SNAPSHOT_FORMAT_VERSION {
        return Err(errorf!(
            "unsupported entity snapshot format {}",
            snapshot.format_version
        ));
    }
    if snapshot.id.is_empty() {
        return Err(errorf!("entity snapshot id is empty"));
    }
    let sealed = snapshot.seal()?;
    if sealed.id != snapshot.id {
        return Err(errorf!("entity snapshot id does not match content"));
    }
    Ok(snapshot.normalized(true))
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct SnapshotFile {
    pub path: String,
    pub snapshot: Snapshot,
}

pub fn read_snapshots(plugin_root: &str) -> Result<Vec<SnapshotFile>> {
    let directory = filepath::join(&[plugin_root, SNAPSHOT_RELATIVE_DIRECTORY]);
    let entries = match std::fs::read_dir(&directory) {
        Ok(entries) => entries,
        Err(error) if error.kind() == ErrorKind::NotFound => return Ok(Vec::new()),
        Err(error) => return Err(error.into()),
    };
    let mut result = Vec::new();
    for entry in entries {
        let entry = entry?;
        let name = entry.file_name().to_string_lossy().into_owned();
        if entry.file_type()?.is_dir() || !name.ends_with(".snapshot.json") {
            continue;
        }
        let path = filepath::join(&[&directory, &name]);
        let content = std::fs::read(&path)?;
        let snapshot =
            parse_snapshot(&content).map_err(|error| errorf!("parse {path}: {error}"))?;
        result.push(SnapshotFile { path, snapshot });
    }
    result.sort_by(|left, right| left.path.cmp(&right.path));
    Ok(result)
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct SnapshotGraph {
    pub files: BTreeMap<String, SnapshotFile>,
    pub leaves: Vec<SnapshotFile>,
    pub missing: BTreeMap<String, Vec<String>>,
}

pub fn build_snapshot_graph(files: &[SnapshotFile]) -> Result<SnapshotGraph> {
    let mut graph = SnapshotGraph::default();
    let mut children: HashMap<&str, usize> = HashMap::new();
    for file in files {
        let id = &file.snapshot.id;
        if graph.files.contains_key(id) {
            return Err(errorf!("duplicate entity snapshot id {id}"));
        }
        graph.files.insert(id.clone(), file.clone());
    }
    for file in files {
        for parent in &file.snapshot.parents {
            if !graph.files.contains_key(parent) {
                graph
                    .missing
                    .entry(file.snapshot.id.clone())
                    .or_default()
                    .push(parent.clone());
                continue;
            }
            *children.entry(parent).or_default() += 1;
        }
    }
    detect_snapshot_cycles(&graph.files)?;
    for (id, file) in &graph.files {
        if children.get(id.as_str()).copied().unwrap_or_default() == 0 {
            graph.leaves.push(file.clone());
        }
    }
    graph
        .leaves
        .sort_by(|left, right| left.snapshot.id.cmp(&right.snapshot.id));
    Ok(graph)
}

fn detect_snapshot_cycles(files: &BTreeMap<String, SnapshotFile>) -> Result<()> {
    #[derive(Clone, Copy, PartialEq, Eq)]
    enum State {
        Visiting,
        Done,
    }
    fn visit(
        id: &str,
        files: &BTreeMap<String, SnapshotFile>,
        state: &mut HashMap<String, State>,
    ) -> Result<()> {
        match state.get(id) {
            Some(State::Visiting) => {
                return Err(errorf!("entity snapshot ancestry contains a cycle at {id}"));
            }
            Some(State::Done) => return Ok(()),
            None => {}
        }
        state.insert(id.to_owned(), State::Visiting);
        if let Some(file) = files.get(id) {
            for parent in &file.snapshot.parents {
                if files.contains_key(parent) {
                    visit(parent, files, state)?;
                }
            }
        }
        state.insert(id.to_owned(), State::Done);
        Ok(())
    }
    let mut state = HashMap::new();
    for id in files.keys() {
        visit(id, files, &mut state)?;
    }
    Ok(())
}

pub fn file_sha256(content: &[u8]) -> String {
    hex(&Sha256::digest(content))
}
