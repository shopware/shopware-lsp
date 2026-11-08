use swlsp_indexer::Result;
use swlsp_parser_php::query;
use swlsp_php::PhpContext;
use swlsp_php_types::Type;
use swlsp_syntax::SyntaxNode;

use crate::index::Index;
use crate::model::{Input, InputKind};

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub enum ReferenceRole {
    #[default]
    None,
    Command,
    Argument,
    Option,
}

/// A command name (`Application::find/get/has`) or input name
/// (`InputInterface::getArgument/getOption/...`) literal.
#[derive(Clone, Debug)]
pub struct Reference {
    pub role: ReferenceRole,
    pub name: String,
    pub node: SyntaxNode,
    pub container: SyntaxNode,
}

pub fn reference_at(node: &SyntaxNode) -> Option<Reference> {
    let literal = query::string_at(node)?;
    let call = query::call_at(&literal)?;
    if query::argument_index(&call, &literal) != Some(0)
        || query::argument_expression(&call, 0).as_ref() != Some(&literal)
    {
        return None;
    }
    let role = match query::call_method_name(&call).to_lowercase().as_str() {
        "getargument" | "hasargument" => ReferenceRole::Argument,
        "getoption" | "hasoption" => ReferenceRole::Option,
        "find" | "get" | "has" => ReferenceRole::Command,
        _ => return None,
    };
    Some(Reference {
        role,
        name: query::string_value(&literal),
        node: literal,
        container: call,
    })
}

/// Whether the call receiver is an `InputInterface` (inputs) or a Console
/// `Application` (commands).
pub fn validate_reference(context: Option<&PhpContext>, reference: &Reference) -> bool {
    let Some(context) = context else {
        return false;
    };
    let Some(receiver) = query::call_receiver(&reference.container) else {
        return false;
    };
    let receiver_type = context.document.type_of(&receiver).ty;
    if receiver_type.is_unknown() {
        return false;
    }
    let target = if reference.role == ReferenceRole::Command {
        "Symfony\\Component\\Console\\Application"
    } else {
        "Symfony\\Component\\Console\\Input\\InputInterface"
    };
    context
        .snapshot
        .relations()
        .is_subtype(&receiver_type, &Type::named(target, Vec::new()))
}

/// The inputs declared by the command class (and method) enclosing an
/// argument or option reference.
pub fn inputs_for_reference(
    context: Option<&PhpContext>,
    index: &Index,
    reference: &Reference,
) -> Result<Vec<Input>> {
    let kind = match reference.role {
        ReferenceRole::Argument => InputKind::Argument,
        ReferenceRole::Option => InputKind::Option,
        _ => return Ok(Vec::new()),
    };
    let Some(context) = context else {
        return Ok(Vec::new());
    };
    let class_name = match &context.inside_class {
        Some(class) => class.fully_qualified.to_string(),
        None => {
            let offset = reference.node.text_range().start();
            context
                .document
                .symbols
                .iter()
                .find(|symbol| symbol.is_class_like() && symbol.range.contains(offset))
                .map(|symbol| symbol.fully_qualified.to_string())
                .unwrap_or_default()
        }
    };
    if class_name.is_empty() {
        return Ok(Vec::new());
    }
    let method = query::method_name(&reference.node);
    index.inputs_for_target(&class_name, &method, kind)
}
