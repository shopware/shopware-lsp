//! Doctrine DBAL type registration references.

use std::sync::Arc;

use swlsp_doctrine::{Index, type_registration_reference_at, type_registrations_in_document};
use swlsp_lsp::{Context, ProviderResult, ReferenceRequest, ReferencesProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_syntax::TextSize;
use swlsp_textutil::eq_ignore_case;

use crate::{FileLines, document_location, request_offset, request_path, unique_locations};

pub struct DoctrineReferenceProvider {
    index: Option<Arc<Index>>,
    php: Option<PhpIndex>,
}

impl DoctrineReferenceProvider {
    pub fn new(index: Option<Arc<Index>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

impl ReferencesProvider for DoctrineReferenceProvider {
    fn get_references(
        &self,
        ctx: &Context,
        request: &ReferenceRequest,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let (Some(index), Some(root), Some(_), Some(line_index), Some(offset)) = (
            &self.index,
            &request.root,
            &request.document,
            request.line_index(),
            request_offset(request),
        ) else {
            return Ok(Vec::new());
        };
        let path = request_path(request)?;
        let Some(reference) = type_registration_reference_at(&path, root, TextSize::from(offset))
        else {
            return Ok(Vec::new());
        };
        if reference.name.is_empty() {
            return Ok(Vec::new());
        }
        let mut files = FileLines::default();
        let mut result = Vec::new();
        for registration in index.type_registrations(&reference.name)? {
            ctx.check()?;
            if registration.file == path {
                continue;
            }
            result.extend(files.location(&registration.file, registration.name_range));
        }
        for registration in type_registrations_in_document(&path, root) {
            ctx.check()?;
            if eq_ignore_case(&registration.name, &reference.name) {
                result.push(document_location(
                    request,
                    line_index,
                    registration.name_range,
                ));
            }
        }
        if request.params.context.include_declaration
            && let Some(php) = &self.php
        {
            for declaration in index.type_declarations(php) {
                ctx.check()?;
                if eq_ignore_case(&declaration.name, &reference.name) {
                    result.extend(files.location(&declaration.file, declaration.range));
                }
            }
        }
        Ok(unique_locations(result))
    }
}
