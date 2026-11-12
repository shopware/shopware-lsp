//! The deprecated XML service configuration inspection and its migration
//! fixes.

use std::collections::HashMap;
use std::sync::Arc;

use crate::diagnostics::{
    ROUTES_XML_DEPRECATED_CODE, SERVICES_XML_DEPRECATED_CODE, ServicesXmlDeprecationAnalyzer,
    ServicesXmlDeprecationPayload, ServicesXmlImportAnalyzer, ServicesXmlImportPayload,
};
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoxError, Context, DiagnosticId, DocumentSnapshot, FixContext, FixId, FixPresentation,
    FixResolution, Inspection, InspectionDefinition, InspectionScope, ProblemDefinition, QuickFix,
    RewriteQuickFix, bind_fix,
};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_rewrite::{Builder, DocumentPlan};
use swlsp_rewrite::{CreateFilePlan, DeleteFilePlan, WorkspacePlan};
use swlsp_uriutil::{file_uri, filepath};

use crate::inspections::{BoundInspection, resolve_anchor};

pub const CONVERT_SERVICES_XML_FIX_ID: &str = "convert-services-xml-to-yaml";
pub const CONVERT_IMPORTED_SERVICES_XML_FIX_ID: &str = "convert-imported-services-xml-to-yaml";

pub fn new_services_xml_migration() -> Arc<dyn Inspection> {
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "symfony.services_xml_migration".to_owned(),
            description: "XML service and route configuration, deprecated in favor of YAML or PHP, with an automated conversion: the services.xml and routes.xml loaded by convention, and XML files a services.php or routes.php imports.",
            domain: "symfony.services",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Xml, LanguageId::Php],
            problems: [SERVICES_XML_DEPRECATED_CODE, ROUTES_XML_DEPRECATED_CODE]
                .into_iter()
                .map(|code| ProblemDefinition {
                    id: DiagnosticId::from(code),
                    source: "symfony".to_owned(),
                    default_severity: protocol::DiagnosticSeverity::WARNING,
                    disabled_by_default: false,
                })
                .collect(),
        },
        analyzers: vec![
            Arc::new(ServicesXmlDeprecationAnalyzer::new()),
            Arc::new(ServicesXmlImportAnalyzer::new()),
        ],
        fixes: vec![
            Arc::new(ServicesXmlMigrationFix),
            Arc::new(ServicesXmlImportFix),
        ],
        bind: Some(Arc::new(|_, payload| {
            // An import in a PHP configuration names the file to convert.
            if let Some(resource) = payload.get("resource").and_then(serde_json::Value::as_str) {
                return vec![bind_fix(
                    CONVERT_IMPORTED_SERVICES_XML_FIX_ID,
                    ServicesXmlImportPayload {
                        resource: resource.to_owned(),
                        routes: routes(payload),
                    },
                )];
            }
            if payload
                .get("convertible")
                .and_then(serde_json::Value::as_bool)
                != Some(true)
            {
                return Vec::new();
            }
            vec![bind_fix(
                CONVERT_SERVICES_XML_FIX_ID,
                ServicesXmlDeprecationPayload {
                    convertible: true,
                    routes: routes(payload),
                },
            )]
        })),
    })
}

/// Whether a problem payload is about a routes file.
fn routes(payload: &serde_json::Map<String, serde_json::Value>) -> bool {
    payload.get("routes").and_then(serde_json::Value::as_bool) == Some(true)
}

struct ServicesXmlMigrationFix;

impl QuickFix for ServicesXmlMigrationFix {
    fn id(&self) -> FixId {
        FixId::from(CONVERT_SERVICES_XML_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: ServicesXmlDeprecationPayload = fix.fix_payload()?;
        let stem = if payload.routes { "routes" } else { "services" };
        Ok(payload.convertible.then(|| FixPresentation {
            title: format!("Symfony: Convert {stem}.xml to {stem}.yaml"),
            kind: Some(protocol::CodeActionKind::QUICKFIX),
            preferred: true,
            resolution: FixResolution::Lazy,
        }))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for ServicesXmlMigrationFix {
    fn build(&self, ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: ServicesXmlDeprecationPayload = fix.fix_payload()?;
        if !payload.convertible {
            return Err("the XML configuration cannot be converted safely".into());
        }
        resolve_anchor(fix)?;
        let entry_path = filepath::clean(&swlsp_uriutil::path(&fix.document.uri)?);
        let snapshots = HashMap::from([(
            entry_path.clone(),
            DocumentSnapshot {
                document: fix.document.clone(),
                version: Some(fix.document.version),
            },
        )]);
        conversion_plan(ctx, fix, &entry_path, payload.routes, snapshots)
    }
}

/// Converts the services XML at `entry_path` and its local XML imports:
/// each becomes a YAML file beside it and is deleted. `snapshots` holds the
/// documents already at hand.
fn conversion_plan(
    ctx: &Context,
    fix: &FixContext,
    entry_path: &str,
    routes: bool,
    mut snapshots: HashMap<String, DocumentSnapshot>,
) -> Result<WorkspacePlan, BoxError> {
    let conversions = {
        let mut read = |path: &str| -> std::io::Result<String> {
            let path = filepath::clean(path);
            if let Some(snapshot) = snapshots.get(&path) {
                return Ok(snapshot.document.source().to_owned());
            }
            let snapshot = fix
                .documents
                .resolve_document(ctx, &file_uri(&path))
                .map_err(|error| std::io::Error::other(error.to_string()))?;
            let source = snapshot.document.source().to_owned();
            snapshots.insert(path, snapshot);
            Ok(source)
        };
        let mut target_exists = |path: &str| -> std::io::Result<bool> {
            match std::fs::metadata(path) {
                Ok(_) => Ok(true),
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(false),
                Err(error) => Err(error),
            }
        };
        let plan = if routes {
            swlsp_symfony::plan_routes_xml_conversion
        } else {
            swlsp_symfony::plan_services_xml_conversion
        };
        plan(
            &ctx.token().indexer_token(),
            entry_path,
            &mut read,
            &mut target_exists,
        )?
    };
    let mut plan = WorkspacePlan::default();
    for conversion in conversions {
        let Some(snapshot) = snapshots.get(&filepath::clean(&conversion.source_path)) else {
            return Err(
                format!("source document {} is unavailable", conversion.source_path).into(),
            );
        };
        plan.creates.push(CreateFilePlan {
            uri: file_uri(&conversion.target_path),
            content: conversion.content,
        });
        plan.deletes.push(DeleteFilePlan {
            uri: file_uri(&conversion.source_path),
            version: snapshot.version,
            source: snapshot.document.source().to_owned(),
        });
    }
    Ok(plan)
}

struct ServicesXmlImportFix;

impl QuickFix for ServicesXmlImportFix {
    fn id(&self) -> FixId {
        FixId::from(CONVERT_IMPORTED_SERVICES_XML_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: ServicesXmlImportPayload = fix.fix_payload()?;
        Ok(Some(FixPresentation {
            title: format!("Symfony: Convert {} to YAML", payload.resource),
            kind: Some(protocol::CodeActionKind::QUICKFIX),
            preferred: true,
            resolution: FixResolution::Lazy,
        }))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for ServicesXmlImportFix {
    /// Converts the imported XML file (and what it imports) and points the
    /// `import()` call at the YAML file.
    fn build(&self, ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: ServicesXmlImportPayload = fix.fix_payload()?;
        let literal = match resolve_anchor(fix)? {
            swlsp_syntax::SyntaxElement::Node(node) if node.kind() == PHP_STRING => node,
            _ => return Err("the import is no longer a string literal".into()),
        };
        let resource = phpquery::string_value(&literal);
        if resource != payload.resource || !crate::diagnostics::is_local_xml_resource(&resource) {
            return Err("the import changed since it was reported".into());
        }
        let config_path = filepath::clean(&swlsp_uriutil::path(&fix.document.uri)?);
        let entry_path = filepath::clean(&filepath::join(&[
            filepath::dir(&config_path).as_str(),
            resource.as_str(),
        ]));
        let mut plan = conversion_plan(ctx, fix, &entry_path, payload.routes, HashMap::new())?;
        let mut builder = Builder::new(fix.document.source());
        let stem = &resource[..resource.len() - ".xml".len()];
        builder.replace_range(
            phpquery::string_content_range(&literal),
            format!("{stem}.yaml"),
        )?;
        plan.documents.push(DocumentPlan::new(
            fix.document.uri.clone(),
            Some(fix.document.version),
            fix.document.source_arc().clone(),
            builder.finish()?,
        ));
        Ok(plan)
    }
}
