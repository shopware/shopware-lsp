//! Call-site signature matching, template inference, and return
//! specialization (port of `resolver/signatures.go`).

use swlsp_php_semantic::{
    AttributeValue, AttributeValueKind, CallReturnContract, Flags, Parameter, Str, Symbol,
    attribute_named,
};
use swlsp_php_types::{
    Joiner, Kind, Relations, Type, contains_template, contains_uncertain, equal_fold, parse,
    substitute,
};

use crate::call_contracts::effective_call_return_type;
use crate::members::{TemplateBindings, type_contains_template_named};

/// One call argument. `name` is set for named arguments (with or without a
/// leading `$`); `expression` is the argument's source text.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Argument {
    pub name: String,
    pub ty: Type,
    pub expression: String,
}

impl Argument {
    /// A positional argument of type `ty`.
    pub fn of(ty: Type) -> Self {
        Argument {
            ty,
            ..Argument::default()
        }
    }

    /// A named argument of type `ty`.
    pub fn named(name: &str, ty: Type) -> Self {
        Argument {
            name: name.to_owned(),
            ty,
            expression: String::new(),
        }
    }
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct ResolvedSignature {
    pub symbol: Symbol,
    pub return_type: Type,
    /// Inferred and defaulted template bindings; empty when none apply.
    pub templates: TemplateBindings,
    pub compatible: bool,
    pub contract_applied: bool,
    /// The first reason an incompatible signature was rejected; empty when
    /// `compatible` is true.
    pub mismatch: String,
}

pub fn resolve_signature(
    relations: Relations<'_>,
    symbol: Symbol,
    arguments: &[Argument],
) -> ResolvedSignature {
    resolve_signature_with_contracts(relations, symbol, arguments, &[])
}

/// Resolves an ordinary declaration signature and then applies any matching
/// dynamic return metadata. Keeping the effective return on
/// [`ResolvedSignature`] ensures consumers agree on overload compatibility
/// before accepting a `.phpstorm.meta.php` override.
pub fn resolve_signature_with_contracts(
    relations: Relations<'_>,
    symbol: Symbol,
    arguments: &[Argument],
    contracts: &[CallReturnContract],
) -> ResolvedSignature {
    let parameter_count = symbol.parameters.len();
    let return_type = symbol.return_type.clone();
    let mut resolver = SignatureResolver {
        relations,
        arguments,
        contracts,
        provided: vec![false; parameter_count],
        mapped_types: vec![Type::UNKNOWN; parameter_count],
        next_positional: 0,
        saw_named: false,
        result: ResolvedSignature {
            symbol,
            return_type,
            templates: TemplateBindings::default(),
            compatible: true,
            contract_applied: false,
            mismatch: String::new(),
        },
    };
    resolver.resolve();
    resolver.result
}

struct SignatureResolver<'r, 'h> {
    relations: Relations<'h>,
    arguments: &'r [Argument],
    contracts: &'r [CallReturnContract],
    provided: Vec<bool>,
    mapped_types: Vec<Type>,
    next_positional: usize,
    saw_named: bool,
    result: ResolvedSignature,
}

fn is_variadic(parameter: &Parameter) -> bool {
    parameter.flags.has(Flags::VARIADIC)
}

impl SignatureResolver<'_, '_> {
    fn resolve(&mut self) {
        for argument in self.arguments {
            if !self.add_argument(argument) {
                return;
            }
        }
        if let Some(missing) = self.missing_required_parameter() {
            self.reject(format!("missing argument for parameter {missing}"));
            return;
        }
        self.validate_template_bounds();
        self.resolve_return_type();
    }

    fn add_argument(&mut self, argument: &Argument) -> bool {
        let Some(parameter_index) = self.parameter_index(argument) else {
            self.reject("positional argument after named argument".to_owned());
            return false;
        };
        let Some(parameter_index) = parameter_index else {
            if argument.name.is_empty() && !self.result.symbol.flags.has(Flags::GENERATED_STUB) {
                // PHP user-defined callables accept additional positional
                // arguments, which remain available through
                // func_get_arg(s). Internal functions reject them, so
                // generated stubs retain strict arity validation.
                return true;
            }
            if argument.name.is_empty() {
                self.reject("too many arguments".to_owned());
            } else {
                self.reject(format!(
                    "unknown named argument ${}",
                    argument.name.strip_prefix('$').unwrap_or(&argument.name)
                ));
            }
            return false;
        };
        let parameter = self.result.symbol.parameters[parameter_index].clone();
        self.capture_direct_return_template(&parameter, &argument.ty);
        if self.provided[parameter_index] && !is_variadic(&parameter) {
            self.reject(format!(
                "parameter {} is passed more than once",
                describe_parameter(parameter_index, &parameter)
            ));
            return false;
        }
        self.capture_argument(parameter_index, &parameter, argument);
        true
    }

    fn capture_direct_return_template(&mut self, parameter: &Parameter, actual: &Type) {
        if !type_contains_template_named(&self.result.symbol.return_type, &parameter.name) {
            return;
        }
        self.result
            .templates
            .insert(parameter.name.clone(), actual.clone());
    }

    /// `None` when a positional argument follows a named one; `Some(None)`
    /// when no parameter accepts the argument.
    fn parameter_index(&mut self, argument: &Argument) -> Option<Option<usize>> {
        let parameters = &self.result.symbol.parameters;
        if !argument.name.is_empty() {
            self.saw_named = true;
            return Some(named_parameter_index(parameters, &argument.name));
        }
        if self.saw_named {
            return None;
        }
        while self.next_positional < parameters.len()
            && self.provided[self.next_positional]
            && !is_variadic(&parameters[self.next_positional])
        {
            self.next_positional += 1;
        }
        if self.next_positional < parameters.len() {
            return Some(Some(self.next_positional));
        }
        if parameters.last().is_some_and(is_variadic) {
            return Some(Some(parameters.len() - 1));
        }
        Some(None)
    }

    fn capture_argument(
        &mut self,
        parameter_index: usize,
        parameter: &Parameter,
        argument: &Argument,
    ) {
        self.provided[parameter_index] = true;
        self.mapped_types[parameter_index] = argument.ty.clone();
        if !is_variadic(parameter) {
            self.next_positional = parameter_index + 1;
        }
        infer_templates(
            self.relations,
            &parameter.ty,
            &argument.ty,
            &mut self.result.templates,
        );
        if !self.argument_type_compatible(parameter, &argument.ty) {
            let message = format!(
                "parameter {} expects {}, {} given",
                describe_parameter(parameter_index, parameter),
                self.expected_type(parameter).text(),
                argument.ty.text()
            );
            self.reject(message);
        }
    }

    /// Marks the signature incompatible and keeps the first reason so
    /// diagnostics can explain the mismatch.
    fn reject(&mut self, reason: String) {
        self.result.compatible = false;
        if self.result.mismatch.is_empty() {
            self.result.mismatch = reason;
        }
    }

    fn expected_type(&self, parameter: &Parameter) -> Type {
        substitute(&parameter.ty, &self.result.templates)
    }

    fn argument_type_compatible(&self, parameter: &Parameter, actual: &Type) -> bool {
        let expected = self.expected_type(parameter);
        if contains_uncertain(actual) || self.relations.is_assignable_to(actual, &expected) {
            return true;
        }
        let documented = substitute(&parameter.doc_type, &self.result.templates);
        let native = substitute(&parameter.native_type, &self.result.templates);
        (!documented.is_unknown() && self.relations.is_assignable_to(actual, &documented))
            || (!native.is_unknown() && self.relations.is_assignable_to(actual, &native))
    }

    fn missing_required_parameter(&self) -> Option<String> {
        self.result
            .symbol
            .parameters
            .iter()
            .enumerate()
            .find(|(index, parameter)| {
                !self.provided[*index] && !parameter.optional && !is_variadic(parameter)
            })
            .map(|(index, parameter)| describe_parameter(index, parameter))
    }

    fn validate_template_bounds(&mut self) {
        let symbol_templates = self.result.symbol.templates().to_vec();
        for template in &symbol_templates {
            let mut value = self.result.templates.get(&template.name).cloned();
            if value.is_none() && !template.default.is_unknown() {
                let defaulted = substitute(&template.default, &self.result.templates);
                self.result
                    .templates
                    .insert(template.name.clone(), defaulted.clone());
                value = Some(defaulted);
            }
            let Some(value) = value else {
                continue;
            };
            if contains_uncertain(&value) {
                continue;
            }
            let bound = substitute(&template.bound, &self.result.templates);
            if !bound.is_unknown()
                && !self.relations.is_assignable_to(&value, &bound)
                && !nominally_satisfies_generic_bound(self.relations, &value, &bound)
            {
                self.reject(format!(
                    "template {} expects {}, {} given",
                    template.name,
                    bound.text(),
                    value.text()
                ));
            }
        }
    }

    fn resolve_return_type(&mut self) {
        if !self.result.templates.is_empty() {
            self.result.return_type =
                substitute(&self.result.symbol.return_type, &self.result.templates);
        }
        if let Some(contracted) = return_type_contract(
            &self.result.symbol.parameters,
            &self.provided,
            &self.mapped_types,
        ) {
            self.result.return_type = refine_contracted_shape(contracted, &self.result.return_type);
            if !self.result.templates.is_empty() {
                self.result.return_type =
                    substitute(&self.result.return_type, &self.result.templates);
            }
        }
        if let Some(contracted) =
            effective_call_return_type(self.relations, self.arguments, self.contracts)
        {
            self.result.return_type = contracted;
            self.result.contract_applied = true;
        }
    }
}

fn named_parameter_index(parameters: &[Parameter], argument_name: &str) -> Option<usize> {
    let name = argument_name.strip_prefix('$').unwrap_or(argument_name);
    if let Some(index) = parameters
        .iter()
        .position(|parameter| parameter.name.strip_prefix('$').unwrap_or(&parameter.name) == name)
    {
        return Some(index);
    }
    if parameters.last().is_some_and(is_variadic) {
        return Some(parameters.len() - 1);
    }
    None
}

fn describe_parameter(index: usize, parameter: &Parameter) -> String {
    format!(
        "#{} (${})",
        index + 1,
        parameter.name.strip_prefix('$').unwrap_or(&parameter.name)
    )
}

fn refine_contracted_shape(contracted: Type, declared: &Type) -> Type {
    let wanted = match contracted.kind() {
        Kind::Array | Kind::NonEmptyArray | Kind::List | Kind::NonEmptyList => Kind::ArrayShape,
        Kind::Object if contracted.name().is_empty() => Kind::ObjectShape,
        _ => return contracted,
    };
    if declared.kind() == wanted {
        return declared.clone();
    }
    if declared.kind() == Kind::Union
        && let Some(member) = declared
            .arguments()
            .iter()
            .find(|member| member.kind() == wanted)
    {
        return member.clone();
    }
    contracted
}

fn return_type_contract(
    parameters: &[Parameter],
    provided: &[bool],
    mapped_types: &[Type],
) -> Option<Type> {
    for (index, parameter) in parameters.iter().enumerate() {
        let Some(attribute) = attribute_named(&parameter.attributes, "ReturnTypeContract") else {
            continue;
        };
        let argument_name =
            if provided.get(index).copied().unwrap_or(false) && index < mapped_types.len() {
                match mapped_types[index].kind() {
                    Kind::True => "true",
                    Kind::False => "false",
                    _ => "exists",
                }
            } else {
                match default_contract_branch(parameter.default_value.as_deref()) {
                    "" => "notExists",
                    branch => branch,
                }
            };
        let Some(value) = attribute.argument(argument_name, usize::MAX) else {
            continue;
        };
        if value.kind != AttributeValueKind::String || value.value.is_empty() {
            continue;
        }
        let Ok(contracted) = parse(&value.value) else {
            continue;
        };
        if contracted.is_unknown() || contracted.kind() == Kind::Error {
            continue;
        }
        return Some(contracted);
    }
    None
}

fn default_contract_branch(value: Option<&AttributeValue>) -> &'static str {
    let Some(value) = value else {
        return "";
    };
    if value.kind != AttributeValueKind::Bool {
        return "";
    }
    if equal_fold(&value.value, "true") {
        "true"
    } else if equal_fold(&value.value, "false") {
        "false"
    } else {
        ""
    }
}

fn nominally_satisfies_generic_bound(relations: Relations<'_>, value: &Type, bound: &Type) -> bool {
    let Some(hierarchy) = relations.hierarchy else {
        return false;
    };
    if value.kind() != Kind::Object
        || value.name().is_empty()
        || bound.kind() != Kind::Object
        || bound.name().is_empty()
        || bound.argument_count() == 0
    {
        return false;
    }
    equal_fold(value.name(), bound.name()) || hierarchy.is_subtype_of(value.name(), bound.name())
}

/// Infers template bindings by matching `actual` against `pattern`.
pub fn infer_templates(
    relations: Relations<'_>,
    pattern: &Type,
    actual: &Type,
    result: &mut TemplateBindings,
) {
    if actual.is_unknown() || actual.kind() == Kind::Mixed {
        return;
    }
    if pattern.kind() == Kind::Template {
        let name = pattern.name();
        match result.get_mut(name) {
            Some(existing) => *existing = Type::union([existing.clone(), actual.clone()]),
            None => {
                result.insert(Str::from(name), actual.clone());
            }
        }
        return;
    }
    if pattern.kind() == Kind::Callable && actual.kind() == Kind::Callable {
        let pattern_parameters = pattern.parameters();
        let actual_parameters = actual.parameters();
        if pattern_parameters.len() == actual_parameters.len() {
            for (expected, given) in pattern_parameters.iter().zip(actual_parameters) {
                infer_templates(relations, &expected.ty, &given.ty, result);
            }
        }
        infer_templates(relations, pattern.result(), actual.result(), result);
        return;
    }
    if pattern.kind() == Kind::Union {
        infer_templates_from_union(relations, pattern, actual, result);
        return;
    }
    if actual.kind() == Kind::Union {
        for member in actual.arguments() {
            infer_templates(relations, pattern, member, result);
        }
        return;
    }
    let mut actual = actual.clone();
    if pattern.kind() == Kind::Object
        && actual.kind() == Kind::Object
        && !equal_fold(pattern.name(), actual.name())
        && let Some(hierarchy) = relations.hierarchy
        && let Some(projected) = hierarchy.as_supertype(&actual, pattern.name())
    {
        actual = projected;
    }
    let iterable_pattern = matches!(
        pattern.kind(),
        Kind::Array | Kind::NonEmptyArray | Kind::Iterable
    );
    if iterable_pattern
        && matches!(actual.kind(), Kind::List | Kind::NonEmptyList)
        && pattern.argument_count() == 2
        && actual.argument_count() == 1
    {
        infer_templates(relations, &pattern.argument(0), &Type::int(), result);
        infer_templates(relations, &pattern.argument(1), &actual.argument(0), result);
        return;
    }
    if iterable_pattern && actual.kind() == Kind::ArrayShape && pattern.argument_count() == 2 {
        let (key, value) = shape_iterable_types(&actual, relations);
        infer_templates(relations, &pattern.argument(0), &key, result);
        infer_templates(relations, &pattern.argument(1), &value, result);
        return;
    }
    let pattern_arguments = pattern.arguments();
    let actual_arguments = actual.arguments();
    if pattern_arguments.len() != actual_arguments.len() {
        return;
    }
    for (expected, given) in pattern_arguments.iter().zip(actual_arguments) {
        infer_templates(relations, expected, given, result);
    }
}

/// Key and value types of an array shape.
pub(crate) fn shape_iterable_types(value: &Type, relations: Relations<'_>) -> (Type, Type) {
    let mut keys = Joiner::new(relations, Type::never());
    let mut values = Joiner::new(relations, Type::never());
    for field in value.fields() {
        let name = field.name.trim_matches(['"', '\'']);
        if name.parse::<i64>().is_ok() {
            keys.add(Type::literal_int(name));
        } else {
            keys.add(Type::literal_string(name));
        }
        values.add(field.ty.clone());
    }
    (keys.value(), values.value())
}

fn infer_templates_from_union(
    relations: Relations<'_>,
    pattern: &Type,
    actual: &Type,
    result: &mut TemplateBindings,
) {
    let (template_patterns, concrete_patterns): (Vec<&Type>, Vec<&Type>) = pattern
        .arguments()
        .iter()
        .partition(|alternative| contains_template(alternative));
    if template_patterns.is_empty() {
        return;
    }
    let alternatives: &[Type] = if actual.kind() == Kind::Union {
        actual.arguments()
    } else {
        std::slice::from_ref(actual)
    };
    let mut remaining = Vec::with_capacity(alternatives.len());
    for alternative in alternatives {
        if alternative.is_unknown() || alternative.kind() == Kind::Mixed {
            continue;
        }
        if let Some(structural) = template_patterns.iter().find(|template_pattern| {
            template_pattern.kind() != Kind::Template
                && template_pattern.kind() == alternative.kind()
        }) {
            infer_templates(relations, structural, alternative, result);
            continue;
        }
        let matched = concrete_patterns
            .iter()
            .any(|concrete| relations.is_assignable_to(alternative, concrete));
        if !matched {
            remaining.push(alternative.clone());
        }
    }
    if remaining.is_empty() {
        return;
    }
    if template_patterns.len() == 1 {
        infer_templates(
            relations,
            template_patterns[0],
            &Type::union(remaining),
            result,
        );
        return;
    }
    // Multiple generic alternatives are inherently ambiguous. Preserve the
    // useful structural cases without assigning every actual alternative to
    // every template-bearing branch.
    for alternative in &remaining {
        if let Some(template_pattern) = template_patterns.iter().find(|template_pattern| {
            template_pattern.kind() == Kind::Template
                || template_pattern.kind() == alternative.kind()
        }) {
            infer_templates(relations, template_pattern, alternative, result);
        }
    }
}
