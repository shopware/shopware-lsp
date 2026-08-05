package semantic

import "strings"

// CallTargetKind distinguishes global functions from class methods in dynamic
// call metadata. Constructors are represented as methods named __construct.
type CallTargetKind uint8

const (
	FunctionCallTarget CallTargetKind = iota
	MethodCallTarget
)

// CallTarget identifies a callable independently from its declaration ID.
// Metadata files commonly describe vendor callables that may not be indexed
// yet, so stable PHP names are a better persistence boundary than SymbolID.
type CallTarget struct {
	Kind  CallTargetKind
	Name  string
	Class string
}

func NewFunctionCallTarget(name string) CallTarget {
	return CallTarget{
		Kind: FunctionCallTarget,
		Name: normalizeCallContractName(name),
	}
}

func NewMethodCallTarget(className, methodName string) CallTarget {
	return CallTarget{
		Kind:  MethodCallTarget,
		Name:  strings.TrimSpace(methodName),
		Class: normalizeCallContractName(className),
	}
}

func normalizeCallContractName(name string) string {
	return strings.TrimPrefix(strings.TrimSpace(name), "\\")
}

func (target CallTarget) valid() bool {
	if target.Name == "" {
		return false
	}
	return target.Kind == FunctionCallTarget ||
		(target.Kind == MethodCallTarget && target.Class != "")
}

// CallReturnKind describes how a metadata contract derives a call's result.
// Additional PhpStorm metadata rules can be added without changing the call
// inference extension or encoding contracts as opaque signature strings.
type CallReturnKind uint8

const (
	CallReturnUnknown CallReturnKind = iota
	CallReturnArgumentType
	CallReturnArgumentElementType
	CallReturnArgumentMap
)

// CallValueKind describes a source-level PHP value retained for expected-value
// completion and argument-to-return maps.
type CallValueKind uint8

const (
	CallValueExpression CallValueKind = iota
	CallValueString
	CallValueNumber
	CallValueConstant
	CallValueClassConstant
)

// CallValue preserves both normalized meaning and insertion-ready PHP source.
// Value contains an unquoted literal for strings and the normalized source
// name for constants; Expression is what completion inserts.
type CallValue struct {
	Kind       CallValueKind
	Value      string
	Expression string
}

func (value CallValue) Label() string {
	if value.Kind == CallValueString {
		return value.Value
	}
	if value.Expression != "" {
		return value.Expression
	}
	return value.Value
}

type CallMapEntry struct {
	Key    CallValue
	Result CallValue
}

type ExpectedArgumentContract struct {
	Argument uint16
	Values   []CallValue
}

// CallReturnContract is the normalized return rule for one callable.
type CallReturnContract struct {
	Kind     CallReturnKind
	Argument uint16
	Map      []CallMapEntry
}

// CallContract is persisted with the semantic document that declared it.
// Fields intentionally use MessagePack's map representation so future
// expected-value, map, and exit-point metadata remains backward compatible.
type CallContract struct {
	Target               CallTarget
	Return               CallReturnContract
	ExpectedArguments    []ExpectedArgumentContract
	ExpectedReturnValues []CallValue
	ExitPoint            bool
	ExitArguments        []ExpectedArgumentContract
}

func (contract CallContract) valid() bool {
	if !contract.Target.valid() {
		return false
	}
	validReturn := false
	switch contract.Return.Kind {
	case CallReturnArgumentType, CallReturnArgumentElementType:
		validReturn = true
	case CallReturnArgumentMap:
		validReturn = len(contract.Return.Map) > 0
	}
	return validReturn || len(contract.ExpectedArguments) > 0 ||
		len(contract.ExpectedReturnValues) > 0 || contract.ExitPoint
}

func cloneCallContracts(contracts []CallContract) []CallContract {
	if len(contracts) == 0 {
		return nil
	}
	result := make([]CallContract, 0, len(contracts))
	for _, contract := range contracts {
		if contract.valid() {
			contract.Return.Map = append(
				[]CallMapEntry(nil),
				contract.Return.Map...,
			)
			contract.ExpectedArguments = append(
				[]ExpectedArgumentContract(nil),
				contract.ExpectedArguments...,
			)
			for index := range contract.ExpectedArguments {
				contract.ExpectedArguments[index].Values = append(
					[]CallValue(nil),
					contract.ExpectedArguments[index].Values...,
				)
			}
			contract.ExpectedReturnValues = append(
				[]CallValue(nil),
				contract.ExpectedReturnValues...,
			)
			contract.ExitArguments = append(
				[]ExpectedArgumentContract(nil),
				contract.ExitArguments...,
			)
			for index := range contract.ExitArguments {
				contract.ExitArguments[index].Values = append(
					[]CallValue(nil),
					contract.ExitArguments[index].Values...,
				)
			}
			result = append(result, contract)
		}
	}
	return result
}

func functionCallContractKey(name string) string {
	return strings.ToLower(normalizeCallContractName(name))
}

func methodCallContractKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

type indexedCallContract struct {
	path     string
	contract CallContract
}

func indexCallContracts(
	documents []*workspaceDocument,
) (map[string][]indexedCallContract, map[string][]indexedCallContract) {
	functionCount := 0
	methodCount := 0
	for _, document := range documents {
		if document == nil {
			continue
		}
		for _, contract := range document.CallContracts {
			switch contract.Target.Kind {
			case FunctionCallTarget:
				functionCount++
			case MethodCallTarget:
				methodCount++
			}
		}
	}
	var functions map[string][]indexedCallContract
	var methods map[string][]indexedCallContract
	if functionCount > 0 {
		functions = make(map[string][]indexedCallContract)
	}
	if methodCount > 0 {
		methods = make(map[string][]indexedCallContract)
	}
	for _, document := range documents {
		if document == nil {
			continue
		}
		for _, contract := range document.CallContracts {
			if !contract.valid() {
				continue
			}
			indexed := indexedCallContract{
				path:     document.Path,
				contract: contract,
			}
			switch contract.Target.Kind {
			case FunctionCallTarget:
				key := functionCallContractKey(contract.Target.Name)
				functions[key] = append(functions[key], indexed)
			case MethodCallTarget:
				key := methodCallContractKey(contract.Target.Name)
				methods[key] = append(methods[key], indexed)
			}
		}
	}
	return functions, methods
}
