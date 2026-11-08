//! The Rust server returns the configuration catalog with sorted object
//! keys; Go's CLI decodes it into typed structs and prints the struct field
//! order. This restores that order (map-typed members stay sorted).

use crate::ordered::Node;

enum Shape {
    Leaf,
    List(&'static Shape),
    Struct(&'static [(&'static str, Shape)]),
}

use Shape::{Leaf, List, Struct};

const OVERRIDE: Shape = Struct(&[
    ("files", Leaf),
    ("enabled", Leaf),
    ("inspections", Leaf),
    ("rules", Leaf),
]);
const PHP: Shape = Struct(&[("extensions", Leaf), ("disabledExtensions", Leaf)]);
const SHOPWARE: Shape = Struct(&[("targetVersion", Leaf)]);
const INDEXING: Shape = Struct(&[
    ("enabled", Leaf),
    ("exclude", Leaf),
    ("maxFileSizeMiB", Leaf),
]);
const MCP: Shape = Struct(&[("tools", Leaf)]);
const DIAGNOSTICS: Shape = Struct(&[
    ("enabled", Leaf),
    ("inspections", Leaf),
    ("rules", Leaf),
    ("overrides", List(&OVERRIDE)),
]);
const CHECK: Shape = Struct(&[("severity", Leaf), ("failOn", Leaf)]);
const EFFECTIVE: Shape = Struct(&[
    ("php", PHP),
    ("shopware", SHOPWARE),
    ("features", Leaf),
    ("indexing", INDEXING),
    ("mcp", MCP),
    ("domains", Leaf),
    ("diagnostics", DIAGNOSTICS),
    ("check", CHECK),
    ("disabledDomainReasons", Leaf),
    ("origins", Leaf),
]);
const PARTIAL: Shape = Struct(&[
    ("$schema", Leaf),
    ("version", Leaf),
    ("php", PHP),
    ("shopware", SHOPWARE),
    ("features", Leaf),
    ("indexing", INDEXING),
    ("mcp", MCP),
    ("domains", Leaf),
    ("diagnostics", DIAGNOSTICS),
    ("check", CHECK),
]);
const SCOPE: Shape = Struct(&[
    ("root", Leaf),
    ("path", Leaf),
    ("configuration", PARTIAL),
    ("error", Leaf),
]);
const ENTRY: Shape = Struct(&[
    ("id", Leaf),
    ("label", Leaf),
    ("description", Leaf),
    ("parent", Leaf),
    ("dependsOn", Leaf),
]);
const RULE: Shape = Struct(&[
    ("id", Leaf),
    ("source", Leaf),
    ("defaultSeverity", Leaf),
    ("defaultEnabled", Leaf),
]);
const INSPECTION: Shape = Struct(&[("id", Leaf), ("languages", Leaf), ("rules", List(&RULE))]);
const ISSUE: Shape = Struct(&[("path", Leaf), ("message", Leaf)]);
const CATALOG: Shape = Struct(&[
    ("path", Leaf),
    ("effective", EFFECTIVE),
    ("scopes", List(&SCOPE)),
    ("features", List(&ENTRY)),
    ("mcpTools", List(&ENTRY)),
    ("domains", List(&ENTRY)),
    ("inspections", List(&INSPECTION)),
    ("error", Leaf),
    ("errors", List(&ISSUE)),
]);

fn reorder(node: Node, shape: &Shape) -> Node {
    match (node, shape) {
        (Node::Array(items), List(item)) => Node::Array(
            items
                .into_iter()
                .map(|value| reorder(value, item))
                .collect(),
        ),
        (Node::Object(mut entries), Struct(fields)) => {
            let mut result = Vec::with_capacity(entries.len());
            for (name, field_shape) in *fields {
                if let Some(index) = entries.iter().position(|(key, _)| key == name) {
                    let (key, value) = entries.remove(index);
                    result.push((key, reorder(value, field_shape)));
                }
            }
            result.extend(entries);
            Node::Object(result)
        }
        (node, _) => node,
    }
}

pub fn reorder_catalog(node: Node) -> Node {
    reorder(node, &CATALOG)
}
