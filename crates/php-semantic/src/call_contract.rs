//! Dynamic call metadata (PhpStorm `.phpstorm.meta.php` style contracts).

use crate::str::Str;

/// Distinguishes global functions from class methods. Constructors are
/// methods named `__construct`.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
#[repr(u8)]
pub enum CallTargetKind {
    #[default]
    Function = 0,
    Method,
}

/// A callable identified by stable PHP names rather than a declaration ID,
/// since metadata commonly describes not-yet-indexed vendor callables.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct CallTarget {
    pub kind: CallTargetKind,
    pub name: Str,
    pub class: Str,
}

impl CallTarget {
    pub fn function(name: &str) -> CallTarget {
        CallTarget {
            kind: CallTargetKind::Function,
            name: normalize_name(name).into(),
            class: Str::default(),
        }
    }

    pub fn method(class_name: &str, method_name: &str) -> CallTarget {
        CallTarget {
            kind: CallTargetKind::Method,
            name: method_name.trim().into(),
            class: normalize_name(class_name).into(),
        }
    }

    fn valid(&self) -> bool {
        !self.name.is_empty() && (self.kind == CallTargetKind::Function || !self.class.is_empty())
    }
}

fn normalize_name(name: &str) -> &str {
    let name = name.trim();
    name.strip_prefix('\\').unwrap_or(name)
}

/// How a metadata contract derives a call's result.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
#[repr(u8)]
pub enum CallReturnKind {
    #[default]
    Unknown = 0,
    ArgumentType,
    ArgumentElementType,
    ArgumentMap,
}

/// A source-level value retained for expected-value completion and
/// argument-to-return maps.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
#[repr(u8)]
pub enum CallValueKind {
    #[default]
    Expression = 0,
    String,
    Number,
    Constant,
    ClassConstant,
}

/// Normalized meaning plus insertion-ready source. `value` holds an
/// unquoted literal for strings and the normalized source name for
/// constants; `expression` is what completion inserts.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct CallValue {
    pub kind: CallValueKind,
    pub value: Str,
    pub expression: Str,
}

impl CallValue {
    pub fn label(&self) -> &str {
        if self.kind == CallValueKind::String {
            return &self.value;
        }
        if !self.expression.is_empty() {
            &self.expression
        } else {
            &self.value
        }
    }
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct CallMapEntry {
    pub key: CallValue,
    pub result: CallValue,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct ExpectedArgumentContract {
    pub argument: u16,
    pub values: Vec<CallValue>,
}

/// The normalized return rule for one callable.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct CallReturnContract {
    pub kind: CallReturnKind,
    pub argument: u16,
    pub map: Vec<CallMapEntry>,
}

/// A contract persisted with the semantic document that declared it.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct CallContract {
    pub target: CallTarget,
    pub return_contract: CallReturnContract,
    pub expected_arguments: Vec<ExpectedArgumentContract>,
    pub expected_return_values: Vec<CallValue>,
    pub exit_point: bool,
    pub exit_arguments: Vec<ExpectedArgumentContract>,
}

impl CallContract {
    pub(crate) fn valid(&self) -> bool {
        if !self.target.valid() {
            return false;
        }
        let valid_return = match self.return_contract.kind {
            CallReturnKind::ArgumentType | CallReturnKind::ArgumentElementType => true,
            CallReturnKind::ArgumentMap => !self.return_contract.map.is_empty(),
            CallReturnKind::Unknown => false,
        };
        valid_return
            || !self.expected_arguments.is_empty()
            || !self.expected_return_values.is_empty()
            || self.exit_point
    }
}

/// Valid contracts only, in order.
pub(crate) fn clone_call_contracts(contracts: &[CallContract]) -> Vec<CallContract> {
    contracts
        .iter()
        .filter(|contract| contract.valid())
        .cloned()
        .collect()
}

pub(crate) fn function_contract_key(name: &str) -> String {
    swlsp_textutil::lowercase(normalize_name(name))
}

pub(crate) fn method_contract_key(name: &str) -> String {
    swlsp_textutil::lowercase(name.trim())
}
