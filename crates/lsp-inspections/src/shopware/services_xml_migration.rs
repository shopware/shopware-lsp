//! The deprecated `services.xml` inspection and its migration fix.

use std::collections::HashMap;
use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoxError, Context, DiagnosticId, DocumentSnapshot, FixContext, FixId, FixPresentation,
    FixResolution, Inspection, InspectionDefinition, InspectionScope, ProblemDefinition, QuickFix,
    RewriteQuickFix, bind_fix,
};
use swlsp_lsp_diagnostics::{
    SERVICES_XML_DEPRECATED_CODE, ServicesXmlDeprecationAnalyzer, ServicesXmlDeprecationPayload,
};
use swlsp_lsp_protocol as protocol;
use swlsp_rewrite::{CreateFilePlan, DeleteFilePlan, WorkspacePlan};
use swlsp_uriutil::{file_uri, filepath};

use crate::{BoundInspection, resolve_anchor};

pub const CONVERT_SERVICES_XML_FIX_ID: &str = "convert-services-xml-to-yaml";

pub fn new_services_xml_migration() -> Arc<dyn Inspection> {
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "symfony.services_xml_migration".to_owned(),
            description: "XML service configuration, deprecated in favor of YAML or PHP, with an automated conversion.",
            domain: "symfony.services",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Xml],
            problems: vec![ProblemDefinition {
                id: DiagnosticId::from(SERVICES_XML_DEPRECATED_CODE),
                source: "symfony".to_owned(),
                default_severity: protocol::DiagnosticSeverity::WARNING,
                disabled_by_default: false,
            }],
        },
        analyzers: vec![Arc::new(ServicesXmlDeprecationAnalyzer::new())],
        fixes: vec![Arc::new(ServicesXmlMigrationFix)],
        bind: Some(Arc::new(|_, payload| {
            if payload
                .get("convertible")
                .and_then(serde_json::Value::as_bool)
                != Some(true)
            {
                return Vec::new();
            }
            vec![bind_fix(
                CONVERT_SERVICES_XML_FIX_ID,
                ServicesXmlDeprecationPayload { convertible: true },
            )]
        })),
    })
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
        Ok(payload.convertible.then(|| FixPresentation {
            title: "Symfony: Convert services.xml to services.yaml".to_owned(),
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
            return Err("services.xml cannot be converted safely".into());
        }
        resolve_anchor(fix)?;
        let entry_path = filepath::clean(&swlsp_uriutil::path(&fix.document.uri)?);
        let mut snapshots: HashMap<String, DocumentSnapshot> = HashMap::from([(
            entry_path.clone(),
            DocumentSnapshot {
                document: fix.document.clone(),
                version: Some(fix.document.version),
            },
        )]);
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
            swlsp_symfony::plan_services_xml_conversion(
                &ctx.token().indexer_token(),
                &entry_path,
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
}
