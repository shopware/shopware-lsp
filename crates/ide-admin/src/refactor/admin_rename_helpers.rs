//! Administration rename handlers for document-local symbols, targets and
//! validation.

use swlsp_admin::{
    AdminComponentIndexer, AdminNameStyle, AdminSourceRange, AdminSymbolKind, AdminSymbolTarget,
    AdminUsageSet, TwigVueBindingKind, VueComponent, VueLocalComponent, camel_to_kebab,
    kebab_to_camel, normalize_slot_name, twig_component_object_binding_field_at_offset,
    twig_dynamic_component_selector, twig_scoped_slot_binding_references,
    twig_slot_owner_starting_tag, twig_symbol_at_offset, twig_vue_binding_at_offset,
    twig_vue_binding_references, twig_vue_bindings, vue_slot_reference_for_attribute,
};
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, RenameRequest, TextDocument, effective_syntax_language};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_twig::ast::{AstNode, HtmlAttribute, HtmlEndingTag, HtmlStartingTag};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{HTML_ENDING_TAG, HTML_STARTING_TAG};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, TextSize};
use swlsp_uriutil::{file_uri, filepath};

use crate::refactor::admin_rename::{AdminRenameProvider, Changes, Handled};
use crate::refactor::{lower_ext, protocol_range};

/// A rename request with its document, tree and params unpacked.
pub(crate) struct Request<'a> {
    pub params: &'a protocol::RenameParams,
    pub document: &'a TextDocument,
    pub root: &'a SyntaxNode,
    pub node: Option<&'a SyntaxNode>,
}

impl<'a> Request<'a> {
    pub fn new(request: &'a RenameRequest) -> Option<Self> {
        Some(Self {
            params: &request.params,
            document: request.syntax.document.as_deref()?,
            root: request.syntax.root.as_ref()?,
            node: request.syntax.node.as_ref(),
        })
    }

    fn uri(&self) -> &str {
        &self.params.text_document.uri
    }

    fn offset(&self) -> u32 {
        self.document
            .line_index()
            .offset_utf16(self.params.position.line, self.params.position.character)
    }

    fn range(&self, range: TextRange) -> protocol::Range {
        protocol_range(self.document.line_index(), range)
    }

    fn effective(&self) -> Option<LanguageId> {
        effective_syntax_language(Some(LanguageId::Vue), self.node)
    }

    /// Whether the request is in a Twig template (`.twig`, or Vue with a
    /// Twig template block at the cursor).
    pub fn is_template(&self) -> bool {
        if self.uri().is_empty() {
            return false;
        }
        let ext = lower_ext(self.uri());
        ext == ".twig" || ext == ".vue" && self.effective() == Some(LanguageId::Twig)
    }

    /// Whether the request is in a JavaScript or TypeScript script.
    fn is_script(&self) -> bool {
        if self.uri().is_empty() {
            return false;
        }
        let ext = lower_ext(self.uri());
        ext == ".js"
            || ext == ".ts"
            || ext == ".vue" && self.effective() == Some(LanguageId::JavaScript)
    }
}

fn single_file(uri: &str, edits: Vec<protocol::TextEdit>) -> protocol::WorkspaceEdit {
    let mut changes = Changes::new();
    changes.insert(uri.to_owned(), edits);
    sort_admin_text_edits(&mut changes);
    protocol::WorkspaceEdit {
        changes,
        ..protocol::WorkspaceEdit::default()
    }
}

fn err(message: String) -> Handled {
    Some(Err(message.into()))
}

fn handled_error(error: impl Into<BoxError>) -> Handled {
    Some(Err(error.into()))
}

fn empty_edit() -> Handled {
    Some(Ok(Some(protocol::WorkspaceEdit::default())))
}

// --- name patterns -----------------------------------------------------------

/// `^[A-Za-z][A-Za-z0-9_-]*$` (component and directive names).
pub(crate) fn is_component_name(value: &str) -> bool {
    matches_pattern(
        value,
        |byte| byte.is_ascii_alphabetic(),
        |byte| byte.is_ascii_alphanumeric() || byte == b'_' || byte == b'-',
    )
}

/// `^[A-Za-z_$][A-Za-z0-9_$]*$`.
pub(crate) fn is_identifier(value: &str) -> bool {
    matches_pattern(
        value,
        |byte| byte.is_ascii_alphabetic() || byte == b'_' || byte == b'$',
        |byte| byte.is_ascii_alphanumeric() || byte == b'_' || byte == b'$',
    )
}

/// `^[A-Za-z][A-Za-z0-9_.:-]*$`.
fn is_event_name(value: &str) -> bool {
    matches_pattern(
        value,
        |byte| byte.is_ascii_alphabetic(),
        |byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'.' | b':' | b'-'),
    )
}

/// `^[A-Za-z][A-Za-z0-9_.-]*$`.
fn is_slot_name(value: &str) -> bool {
    matches_pattern(
        value,
        |byte| byte.is_ascii_alphabetic(),
        |byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'.' | b'-'),
    )
}

fn matches_pattern(value: &str, first: impl Fn(u8) -> bool, rest: impl Fn(u8) -> bool) -> bool {
    let bytes = value.as_bytes();
    !bytes.is_empty() && first(bytes[0]) && bytes[1..].iter().all(|&byte| rest(byte))
}

// --- local handlers ----------------------------------------------------------

impl AdminRenameProvider {
    pub(crate) fn rename_scoped_slot_local(
        &self,
        index: &AdminComponentIndexer,
        request: &Request<'_>,
    ) -> Handled {
        if !request.is_template() {
            return None;
        }
        let template_path = match swlsp_uriutil::path(request.uri()) {
            Ok(path) => path,
            Err(error) => return handled_error(error),
        };
        let node = request.node?;
        let offset = request.offset();
        let content = request.document.source();
        let live_owner = match index.get_component_for_document(
            &template_path,
            Some(request.root),
            content,
            Some(request.document.line_index()),
        ) {
            Ok(owner) => owner,
            Err(error) => return handled_error(error),
        };
        match index.resolve_twig_scoped_slot_member_for_owner(
            request.root,
            node,
            content,
            offset,
            &template_path,
            live_owner.as_ref(),
        ) {
            Err(error) => return handled_error(error),
            Ok(Some(member)) => {
                return err(format!(
                    "cannot rename scoped-slot contract member {:?} from a consumer template",
                    member.access.member
                ));
            }
            Ok(None) => {}
        }
        let resolved = match index.resolve_twig_scoped_slot_binding_for_owner(
            request.root,
            node,
            content,
            offset,
            &template_path,
            live_owner.as_ref(),
        ) {
            Err(error) => return handled_error(error),
            Ok(None) => return None,
            Ok(Some(resolved)) => resolved,
        };
        let binding = &resolved.binding;
        if resolved.scoped.scope.is_binding_offset(offset)
            && resolved.identifier == binding.member_name
            && binding.member_name != binding.local_name
        {
            return err(format!(
                "cannot rename scoped-slot contract member {:?} from a consumer template",
                binding.member_name
            ));
        }
        let new_name = request.params.new_name.trim();
        if new_name == binding.local_name {
            return empty_edit();
        }
        if !is_identifier(new_name) {
            return err(format!(
                "{:?} is not a valid scoped-slot local identifier",
                new_name
            ));
        }
        if resolved
            .scoped
            .scope
            .bindings
            .iter()
            .any(|other| other.local_name == new_name && other.local_range != binding.local_range)
        {
            return err(format!(
                "scoped-slot local {:?} already exists in this slot scope",
                new_name
            ));
        }
        let ranges = twig_scoped_slot_binding_references(request.root, content, &resolved);
        if ranges.is_empty() {
            return Some(Ok(None));
        }
        let edits = ranges
            .into_iter()
            .map(|range| {
                let replacement = if range == binding.local_range
                    && !binding.whole_object
                    && binding.member_name == binding.local_name
                {
                    format!("{}: {new_name}", binding.member_name)
                } else {
                    new_name.to_owned()
                };
                protocol::TextEdit::new(request.range(range), replacement)
            })
            .collect();
        Some(Ok(Some(single_file(request.uri(), edits))))
    }

    /// `Some(Ok((target, ranges)))` when handled; an empty target name means
    /// handled without a target.
    pub(crate) fn dynamic_twig_slot_rename_target(
        &self,
        index: &AdminComponentIndexer,
        request: &Request<'_>,
    ) -> Option<Result<(AdminSymbolTarget, Vec<TextRange>), BoxError>> {
        if !request.is_template() {
            return None;
        }
        let offset = request.offset();
        let attribute =
            twigquery::html_attribute_at(&request.root.node_at(TextSize::from(offset))?)?;
        let attribute_name = twigquery::html_attribute_name(&attribute).unwrap_or_default();
        if normalize_slot_name(&attribute_name).is_empty() {
            return None;
        }
        let start_tag = twigquery::starting_html_tag_at(&attribute)?;
        let owner = twig_slot_owner_starting_tag(&start_tag)?;
        twig_dynamic_component_selector(&owner)?;
        let none = || Some(Ok((AdminSymbolTarget::default(), Vec::new())));
        let Some(name_token) = HtmlAttribute::cast(attribute.clone()).and_then(|node| node.name())
        else {
            return none();
        };
        let Some(reference) =
            vue_slot_reference_for_attribute(&attribute_name, name_token.text_range())
        else {
            return none();
        };
        if offset < u32::from(reference.range.start()) || offset > u32::from(reference.range.end())
        {
            return none();
        }
        Some((|| {
            let template_path = swlsp_uriutil::path(request.uri())?;
            let live_owner = index.get_component_for_document(
                &template_path,
                Some(request.root),
                request.document.source(),
                Some(request.document.line_index()),
            )?;
            let (components, complete) = index.resolve_twig_slot_consumer_components(
                &template_path,
                &start_tag,
                live_owner.as_ref(),
            )?;
            if !complete {
                return Err::<_, BoxError>(
                    "cannot safely rename an Administration slot on a runtime-dynamic component"
                        .into(),
                );
            }
            let targets = slot_rename_targets(&components, &reference.name);
            if targets.is_empty() {
                return Ok((AdminSymbolTarget::default(), Vec::new()));
            }
            if targets.len() > 1 {
                return Err(format!(
                    "cannot safely rename Administration slot {:?} because the dynamic component candidates expose distinct declarations",
                    reference.name
                )
                .into());
            }
            let target = targets.into_iter().next().expect("one target");
            let ranges = dynamic_twig_slot_rename_ranges(
                index,
                request.root,
                &template_path,
                live_owner.as_ref(),
                &target,
            )?;
            Ok((target, ranges))
        })())
    }

    pub(crate) fn rename_local_component_declaration(
        &self,
        index: &AdminComponentIndexer,
        request: &Request<'_>,
    ) -> Handled {
        let ext = lower_ext(request.uri());
        if ext != ".js" && ext != ".ts" && (ext != ".vue" || !request.is_script()) {
            return None;
        }
        let definition_path = match swlsp_uriutil::path(request.uri()) {
            Ok(path) => path,
            Err(error) => return handled_error(error),
        };
        let (owner, local) = match index.get_local_component_at_definition_position(
            &definition_path,
            request.params.position.line,
            request.params.position.character,
        ) {
            Err(error) => return handled_error(error),
            Ok(None) => return None,
            Ok(Some(found)) => found,
        };
        let new_name = request.params.new_name.trim();
        if let Some(result) = validate_local_component(&owner, &local, new_name) {
            return Some(result);
        }
        let mut changes = Changes::new();
        let sets = match index.get_usages(AdminSymbolKind::Component, "", &local.name) {
            Ok(sets) => sets,
            Err(error) => return handled_error(error),
        };
        let owner_template = filepath::clean(&owner.template_path);
        for set in &sets {
            if filepath::clean(&set.file_path) != owner_template {
                continue;
            }
            let uri = file_uri(&set.file_path);
            for occurrence in &set.occurrences {
                changes
                    .entry(uri.clone())
                    .or_default()
                    .push(protocol::TextEdit::new(source_range(occurrence), new_name));
            }
        }
        add_local_declaration(&mut changes, &local, new_name);
        sort_admin_text_edits(&mut changes);
        Some(Ok(Some(protocol::WorkspaceEdit {
            changes,
            ..protocol::WorkspaceEdit::default()
        })))
    }

    pub(crate) fn rename_local_component_tag(
        &self,
        index: &AdminComponentIndexer,
        request: &Request<'_>,
    ) -> Handled {
        if !request.is_template() {
            return None;
        }
        let template_path = match swlsp_uriutil::path(request.uri()) {
            Ok(path) => path,
            Err(error) => return handled_error(error),
        };
        let target = twig_symbol_at_offset(request.root, request.offset())?;
        if target.kind != AdminSymbolKind::Component {
            return None;
        }
        let owner = match index.get_component_by_template_path(&template_path) {
            Err(error) => return handled_error(error),
            Ok(owner) => owner?,
        };
        let local = owner.local_component(&target.name)?.clone();
        let new_name = request.params.new_name.trim();
        if let Some(result) = validate_local_component(&owner, &local, new_name) {
            return Some(result);
        }
        let uri = request.uri().to_owned();
        let mut edits = Vec::new();
        for node in twigquery::iterate_nodes(request.root, &[HTML_STARTING_TAG, HTML_ENDING_TAG]) {
            let mut name_range = None;
            if node.kind() == HTML_STARTING_TAG {
                if let Some(name) = HtmlStartingTag::cast(node.clone()).and_then(|tag| tag.name())
                    && name.text() == local.name
                {
                    name_range = Some(request.range(name.text_range()));
                }
                if let Some(selector) = twig_dynamic_component_selector(&node) {
                    for candidate in &selector.candidates {
                        if candidate.name == local.name {
                            edits.push(protocol::TextEdit::new(
                                request.range(candidate.range),
                                new_name,
                            ));
                        }
                    }
                }
            } else if let Some(name) = HtmlEndingTag::cast(node.clone()).and_then(|tag| tag.name())
                && name.text() == local.name
            {
                name_range = Some(request.range(name.text_range()));
            }
            if let Some(range) = name_range {
                edits.push(protocol::TextEdit::new(range, new_name));
            }
        }
        let mut changes = Changes::new();
        if !edits.is_empty() {
            changes.insert(uri, edits);
        }
        add_local_declaration(&mut changes, &local, new_name);
        sort_admin_text_edits(&mut changes);
        Some(Ok(Some(protocol::WorkspaceEdit {
            changes,
            ..protocol::WorkspaceEdit::default()
        })))
    }

    pub(crate) fn validate_rename(
        &self,
        index: &AdminComponentIndexer,
        target: &AdminSymbolTarget,
        new_name: &str,
    ) -> Result<(), BoxError> {
        let quoted = format!("{new_name:?}");
        if new_name.is_empty()
            || new_name
                .chars()
                .any(|character| matches!(character, '\'' | '"' | '`' | '\\' | '\r' | '\n'))
        {
            return Err(format!("{quoted} is not a valid Administration registry name").into());
        }
        let invalid = |what: &str| -> Result<(), BoxError> {
            Err(format!("{quoted} is not a valid {what}").into())
        };
        match target.kind {
            AdminSymbolKind::Component if !is_component_name(new_name) => {
                return invalid("Administration component name");
            }
            AdminSymbolKind::CmsElement | AdminSymbolKind::CmsBlock
                if !is_component_name(new_name) =>
            {
                return invalid("Shopware CMS registry name");
            }
            AdminSymbolKind::ComponentEvent if !is_event_name(new_name) => {
                return invalid("Administration component event name");
            }
            AdminSymbolKind::EventBusEvent if !is_event_name(new_name) => {
                return invalid("Shopware EventBus event name");
            }
            AdminSymbolKind::ComponentProp if !is_identifier(new_name) => {
                return invalid("Administration component prop name");
            }
            AdminSymbolKind::ComponentSlot if !is_slot_name(new_name) => {
                return invalid("Administration component slot name");
            }
            AdminSymbolKind::Directive if !is_component_name(new_name) => {
                return invalid("Administration Vue directive name");
            }
            AdminSymbolKind::ComponentMember if !is_identifier(new_name) => {
                return invalid("Administration component member name");
            }
            _ => {}
        }
        if target.kind == AdminSymbolKind::ComponentSlot
            && index.is_dynamic_component_slot(target)?
        {
            return Err(format!(
                "cannot rename Administration component slot {:?} because it is provided by a dynamic slot family",
                target.name
            )
            .into());
        }
        if matches!(
            target.kind,
            AdminSymbolKind::ComponentProp
                | AdminSymbolKind::ComponentEvent
                | AdminSymbolKind::ComponentSlot
        ) && is_external_meteor_path(&target.owner)
        {
            return Err(format!(
                "cannot rename external Meteor component {} {}",
                target.kind.as_str(),
                target.name
            )
            .into());
        }
        if target.kind != AdminSymbolKind::Component {
            return Ok(());
        }
        if index
            .get_component(&target.name)?
            .iter()
            .any(|component| is_external_meteor_path(&component.file_path))
        {
            return Err(format!("cannot rename external Meteor component {}", target.name).into());
        }
        Ok(())
    }

    /// The Administration symbol under the cursor, if any.
    pub(crate) fn target_at(
        &self,
        index: &AdminComponentIndexer,
        request: &Request<'_>,
    ) -> Result<Option<AdminSymbolTarget>, BoxError> {
        let ext = lower_ext(request.uri());
        let javascript = |request: &Request<'_>| -> Result<Option<AdminSymbolTarget>, BoxError> {
            let path = swlsp_uriutil::path(request.uri())?;
            let Some(node) = request.node else {
                return Ok(None);
            };
            Ok(index.javascript_symbol_at(&path, node)?)
        };
        let template = |request: &Request<'_>| -> Result<Option<AdminSymbolTarget>, BoxError> {
            let path = swlsp_uriutil::path(request.uri())?;
            let offset = request.offset();
            if twig_component_object_binding_field_at_offset(request.root, offset).is_none()
                && let Some((target, _)) = index.twig_component_member_at(
                    &path,
                    request.root,
                    request.document.source(),
                    offset,
                )?
            {
                return Ok(Some(target));
            }
            Ok(index.twig_symbol_at(&path, request.root, offset)?)
        };
        match ext.as_str() {
            ".js" | ".ts" => javascript(request),
            ".twig" => template(request),
            ".vue" => {
                if request.is_script() {
                    return javascript(request);
                }
                if !request.is_template() {
                    return Ok(None);
                }
                template(request)
            }
            _ => Ok(None),
        }
    }
}

fn validate_local_component(
    owner: &VueComponent,
    local: &VueLocalComponent,
    new_name: &str,
) -> Option<Result<Option<protocol::WorkspaceEdit>, BoxError>> {
    if new_name == local.name {
        return Some(Ok(Some(protocol::WorkspaceEdit::default())));
    }
    if !is_component_name(new_name) {
        return Some(Err(format!(
            "{:?} is not a valid Administration component name",
            new_name
        )
        .into()));
    }
    if owner
        .local_component(new_name)
        .is_some_and(|conflict| conflict.name != local.name)
    {
        return Some(Err(format!(
            "local Administration component {:?} already exists",
            new_name
        )
        .into()));
    }
    let range = &local.name_range;
    if local.file_path.is_empty()
        || (range.end_line == 0
            && range.end_character == 0
            && range.start_line == 0
            && range.start_character == 0)
    {
        return Some(Err(format!(
            "cannot safely rename local Administration component {:?} because its declaration range is not indexed",
            local.name
        )
        .into()));
    }
    None
}

fn add_local_declaration(changes: &mut Changes, local: &VueLocalComponent, new_name: &str) {
    let replacement = if local.shorthand {
        format!("'{new_name}': {}", local.symbol)
    } else if !local.quoted {
        format!("'{new_name}'")
    } else {
        new_name.to_owned()
    };
    changes
        .entry(file_uri(&local.file_path))
        .or_default()
        .push(protocol::TextEdit::new(
            source_range(&local.name_range),
            replacement,
        ));
}

fn slot_rename_targets(components: &[VueComponent], slot_name: &str) -> Vec<AdminSymbolTarget> {
    let mut result: Vec<AdminSymbolTarget> = Vec::new();
    for component in components {
        let Some(slot) = component.component_slot(slot_name) else {
            continue;
        };
        let owner = if slot.file_path.is_empty() {
            component.template_path.clone()
        } else {
            slot.file_path.clone()
        };
        let target = AdminSymbolTarget {
            kind: AdminSymbolKind::ComponentSlot,
            owner,
            name: slot_name.to_owned(),
        };
        if target.owner.is_empty() || result.contains(&target) {
            continue;
        }
        result.push(target);
    }
    result
}

fn dynamic_twig_slot_rename_ranges(
    index: &AdminComponentIndexer,
    root: &SyntaxNode,
    template_path: &str,
    live_owner: Option<&VueComponent>,
    target: &AdminSymbolTarget,
) -> Result<Vec<TextRange>, BoxError> {
    let mut result = Vec::new();
    for start_tag in twigquery::iterate_nodes(root, &[HTML_STARTING_TAG]) {
        let Some(owner) = twig_slot_owner_starting_tag(&start_tag) else {
            continue;
        };
        if twig_dynamic_component_selector(&owner).is_none() {
            continue;
        }
        let (components, complete) =
            index.resolve_twig_slot_consumer_components(template_path, &start_tag, live_owner)?;
        if !complete {
            continue;
        }
        let Some(tag) = HtmlStartingTag::cast(start_tag.clone()) else {
            continue;
        };
        for attribute in tag.attributes() {
            let Some(name_token) = attribute.name() else {
                continue;
            };
            let name = twigquery::html_attribute_name(attribute.syntax()).unwrap_or_default();
            let Some(reference) = vue_slot_reference_for_attribute(&name, name_token.text_range())
            else {
                continue;
            };
            if slot_rename_targets(&components, &reference.name).contains(target) {
                result.push(reference.range);
            }
        }
    }
    Ok(result)
}

/// Renames a Vue template-local symbol (such as a slot-scope or `v-for`
/// binding) within its template.
pub(crate) fn rename_vue_local(request: &Request<'_>) -> Handled {
    if !request.is_template() {
        return None;
    }
    let content = request.document.source();
    let binding = twig_vue_binding_at_offset(request.root, content, request.offset())?;
    let new_name = request.params.new_name.trim();
    if new_name == binding.name {
        return empty_edit();
    }
    if binding.kind == TwigVueBindingKind::Event {
        return err("cannot rename Vue's implicit $event handler payload".to_owned());
    }
    if !is_identifier(new_name) {
        return err(format!(
            "{:?} is not a valid Vue template identifier",
            new_name
        ));
    }
    if twig_vue_bindings(request.root, content)
        .iter()
        .any(|candidate| {
            candidate.kind == TwigVueBindingKind::For
                && candidate.scope_range == binding.scope_range
                && candidate.declaration_range != binding.declaration_range
                && candidate.name == new_name
        })
    {
        return err(format!(
            "vue template identifier {:?} already exists in this v-for scope",
            new_name
        ));
    }
    let ranges = twig_vue_binding_references(request.root, content, &binding);
    if ranges.is_empty() {
        return Some(Ok(None));
    }
    let edits = ranges
        .into_iter()
        .map(|range| protocol::TextEdit::new(request.range(range), new_name))
        .collect();
    Some(Ok(Some(single_file(request.uri(), edits))))
}

pub(crate) fn has_admin_owned_source_occurrence(sets: &[AdminUsageSet], source_path: &str) -> bool {
    let source_path = filepath::clean(source_path);
    sets.iter()
        .any(|set| filepath::clean(&set.file_path) == source_path && !set.occurrences.is_empty())
}

pub(crate) fn has_admin_declaration_occurrence(sets: &[AdminUsageSet]) -> bool {
    sets.iter()
        .flat_map(|set| &set.occurrences)
        .any(|occurrence| occurrence.declaration)
}

pub(crate) fn admin_rename_replacement(
    target: &AdminSymbolTarget,
    new_name: &str,
    occurrence: &AdminSourceRange,
) -> String {
    if occurrence.name_style == AdminNameStyle::Shorthand {
        match target.kind {
            AdminSymbolKind::Directive if occurrence.declaration => {
                return format!(
                    "{}: {}",
                    kebab_to_camel(new_name),
                    kebab_to_camel(&target.name)
                );
            }
            AdminSymbolKind::ComponentProp => return format!("{new_name}: {}", target.name),
            AdminSymbolKind::ComponentMember => {
                return if occurrence.declaration {
                    format!("{new_name}: {}", target.name)
                } else {
                    format!("{}: {new_name}", target.name)
                };
            }
            _ => {}
        }
    }
    let mut replacement = new_name.to_owned();
    if target.kind == AdminSymbolKind::ComponentProp
        && !occurrence.declaration
        && !occurrence.identifier
    {
        replacement = camel_to_kebab(new_name);
    }
    if matches!(
        target.kind,
        AdminSymbolKind::ComponentEvent | AdminSymbolKind::Directive
    ) && occurrence.name_style == AdminNameStyle::Camel
    {
        replacement = kebab_to_camel(new_name);
    }
    if target.kind == AdminSymbolKind::ComponentEvent
        && occurrence.identifier
        && !is_identifier(&replacement)
    {
        return format!("{replacement:?}");
    }
    replacement
}

fn is_external_meteor_path(path: &str) -> bool {
    filepath::to_slash(path).contains("/node_modules/@shopware-ag/meteor-component-library/")
}

pub(crate) fn source_range(range: &AdminSourceRange) -> protocol::Range {
    protocol::Range {
        start: protocol::Position {
            line: range.start_line,
            character: range.start_character,
        },
        end: protocol::Position {
            line: range.end_line,
            character: range.end_character,
        },
    }
}

/// Stable sort, descending by start position.
pub(crate) fn sort_admin_text_edits(changes: &mut Changes) {
    for edits in changes.values_mut() {
        edits.sort_by(|left, right| {
            (right.range.start.line, right.range.start.character)
                .cmp(&(left.range.start.line, left.range.start.character))
        });
    }
}
