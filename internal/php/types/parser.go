package types

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Parse parses native PHP and common PHPDoc type syntax into a canonical Type.
func Parse(source string) (Type, error) {
	return parse(source, true)
}

// ParseNative parses a language-level PHP type. Unlike PHPDoc parsing, names
// such as T or TValue are treated as class names unless a later binder maps
// them to a declared template.
func ParseNative(source string) (Type, error) {
	return parse(source, false)
}

func parse(source string, allowTemplates bool) (Type, error) {
	parser := typeParser{
		source:         strings.TrimSpace(source),
		allowTemplates: allowTemplates,
	}
	if parser.source == "" {
		return Unknown(), nil
	}
	result, err := parser.parseConditional()
	if err != nil {
		return Error(), err
	}
	parser.skipSpace()
	if !parser.atEnd() {
		return Error(), parser.errorf("unexpected %q", parser.source[parser.position:])
	}
	return result, nil
}

// MustParse is intended for declarations and tests where invalid type syntax is
// a programmer error.
func MustParse(source string) Type {
	result, err := Parse(source)
	if err != nil {
		panic(err)
	}
	return result
}

type typeParser struct {
	source         string
	position       int
	allowTemplates bool
}

func (p *typeParser) parseConditional() (Type, error) {
	subject, err := p.parseUnion()
	if err != nil {
		return Error(), err
	}
	if !p.consumeWord("is") {
		return subject, nil
	}
	negated := p.consumeWord("not")
	target, err := p.parseUnion()
	if err != nil {
		return Error(), err
	}
	if !p.consume("?") {
		return Error(), p.errorf("expected ?")
	}
	ifTrue, err := p.parseConditional()
	if err != nil {
		return Error(), err
	}
	if !p.consume(":") {
		return Error(), p.errorf("expected :")
	}
	ifFalse, err := p.parseConditional()
	if err != nil {
		return Error(), err
	}
	if negated {
		ifTrue, ifFalse = ifFalse, ifTrue
	}
	return Conditional(subject, target, ifTrue, ifFalse), nil
}

func (p *typeParser) parseUnion() (Type, error) {
	left, err := p.parseIntersection()
	if err != nil {
		return Error(), err
	}
	if !p.consume("|") {
		return left, nil
	}
	values := []Type{left}
	for {
		right, rightErr := p.parseIntersection()
		if rightErr != nil {
			return Error(), rightErr
		}
		values = append(values, right)
		if !p.consume("|") {
			break
		}
	}
	return Union(values...), nil
}

func (p *typeParser) parseIntersection() (Type, error) {
	left, err := p.parsePostfix()
	if err != nil {
		return Error(), err
	}
	if !p.consume("&") {
		return left, nil
	}
	values := []Type{left}
	for {
		right, rightErr := p.parsePostfix()
		if rightErr != nil {
			return Error(), rightErr
		}
		values = append(values, right)
		if !p.consume("&") {
			break
		}
	}
	return Intersection(values...), nil
}

func (p *typeParser) parsePostfix() (Type, error) {
	value, err := p.parsePrimary()
	if err != nil {
		return Error(), err
	}
	for p.consume("[") {
		if !p.consume("]") {
			return Error(), p.errorf("expected ]")
		}
		value = Array(ArrayKey(), value)
	}
	return value, nil
}

func (p *typeParser) parsePrimary() (Type, error) {
	if p.consume("?") {
		value, err := p.parsePostfix()
		if err != nil {
			return Error(), err
		}
		return Nullable(value), nil
	}
	if p.consume("(") {
		value, err := p.parseConditional()
		if err != nil {
			return Error(), err
		}
		if !p.consume(")") {
			return Error(), p.errorf("expected )")
		}
		return value, nil
	}
	p.skipSpace()
	if p.atEnd() {
		return Error(), p.errorf("expected type")
	}
	if p.peek() == '\'' || p.peek() == '"' {
		value, err := p.parseQuoted()
		if err != nil {
			return Error(), err
		}
		return LiteralString(value), nil
	}
	if isNumberStart(p.peek()) {
		number, floatingPoint := p.parseNumber()
		if floatingPoint {
			return LiteralFloat(number), nil
		}
		return LiteralInt(number), nil
	}

	name := p.parseName()
	if name == "" {
		return Error(), p.errorf("expected type")
	}
	lower := strings.ToLower(name)
	normalized := strings.TrimPrefix(lower, "\\")
	if (lower == "array" || lower == "object") && p.consume("{") {
		return p.parseShape(lower == "object")
	}
	if (normalized == "callable" || normalized == "closure") &&
		p.consume("(") {
		return p.parseCallable()
	}

	var args []Type
	if p.consume("<") {
		for !p.consume(">") {
			// PHPStan call-site variance affects compatibility checks, but the
			// underlying argument still carries the same value type for member
			// substitution and inference.
			p.consumeWord("covariant")
			p.consumeWord("contravariant")
			argument := Mixed()
			if !p.consume("*") {
				var err error
				argument, err = p.parseUnion()
				if err != nil {
					return Error(), err
				}
			}
			args = append(args, argument)
			if p.consume(">") {
				break
			}
			if !p.consume(",") {
				return Error(), p.errorf("expected , or >")
			}
		}
	}
	return typeFromName(name, args, p.allowTemplates), nil
}

func (p *typeParser) parseCallable() (Type, error) {
	var parameters []CallableParameter
	for !p.consume(")") {
		parameter := CallableParameter{}
		if p.consume("&") {
			parameter.ByReference = true
		}
		if p.consume("...") {
			parameter.Variadic = true
		}
		parameterType, err := p.parseUnion()
		if err != nil {
			return Error(), err
		}
		parameter.Type = parameterType
		p.skipSpace()
		if !p.atEnd() && p.peek() == '$' {
			parameter.Name = p.parseName()
		}
		if p.consume("=") {
			parameter.Optional = true
		}
		parameters = append(parameters, parameter)
		if p.consume(")") {
			break
		}
		if !p.consume(",") {
			return Error(), p.errorf("expected , or )")
		}
	}
	result := Mixed()
	if p.consume(":") {
		var err error
		result, err = p.parseUnion()
		if err != nil {
			return Error(), err
		}
	}
	return Callable(parameters, result), nil
}

func (p *typeParser) parseShape(object bool) (Type, error) {
	var fields []ShapeField
	open := false
	for !p.consume("}") {
		if p.consume("...") {
			open = true
			if !p.consume("}") {
				return Error(), p.errorf("expected }")
			}
			break
		}
		name := p.parseShapeKey()
		if name == "" {
			return Error(), p.errorf("expected shape key")
		}
		optional := p.consume("?")
		if !p.consume(":") {
			return Error(), p.errorf("expected :")
		}
		fieldType, err := p.parseUnion()
		if err != nil {
			return Error(), err
		}
		fields = append(fields, ShapeField{Name: name, Type: fieldType, Optional: optional})
		if p.consume("}") {
			break
		}
		if !p.consume(",") {
			return Error(), p.errorf("expected , or }")
		}
	}
	if object {
		return ObjectShape(fields, open), nil
	}
	return ArrayShape(fields, open), nil
}

func (p *typeParser) parseShapeKey() string {
	p.skipSpace()
	if p.atEnd() {
		return ""
	}
	if p.peek() == '\'' || p.peek() == '"' {
		value, err := p.parseQuoted()
		if err == nil {
			return strconv.Quote(value)
		}
		return ""
	}
	start := p.position
	name := p.parseName()
	// Older persisted inferred shapes rendered dotted string keys without
	// quotes. Dots are not valid in an ordinary PHPDoc identifier, but they
	// are unambiguous here because a shape key ends at ? or :. Keep accepting
	// that legacy representation while the renderer emits quoted keys.
	for !p.atEnd() && p.peek() == '.' {
		p.position++
		if p.parseName() == "" {
			p.position = start
			return ""
		}
	}
	if p.position > start {
		return p.source[start:p.position]
	}
	return name
}

func typeFromName(name string, args []Type, allowTemplates bool) Type {
	lower := builtinTypeName(strings.TrimPrefix(name, "\\"))
	if allowTemplates && strings.HasPrefix(name, "$") &&
		!strings.EqualFold(name, "$this") {
		return Template(name)
	}
	switch lower {
	case "unknown":
		return Unknown()
	case "error":
		return Error()
	case "never":
		return Never()
	case "mixed":
		return Mixed()
	case "void":
		return Void()
	case "null":
		return Null()
	case "bool", "boolean":
		return Bool()
	case "true":
		return True()
	case "false":
		return False()
	case "int", "integer", "positive-int", "negative-int", "non-negative-int":
		return Int()
	case "float", "double", "number":
		return Float()
	case "scalar":
		return Union(Bool(), Int(), Float(), String())
	case "string", "non-empty-string", "numeric-string", "literal-string":
		return String()
	case "object":
		return Object()
	case "resource":
		return Resource()
	case "array-key":
		return ArrayKey()
	case "array":
		switch len(args) {
		case 0:
			return Array(Mixed(), Mixed())
		case 1:
			return Array(ArrayKey(), args[0])
		default:
			return Array(args[0], args[1])
		}
	case "non-empty-array":
		switch len(args) {
		case 0:
			return NonEmptyArray(Mixed(), Mixed())
		case 1:
			return NonEmptyArray(ArrayKey(), args[0])
		default:
			return NonEmptyArray(args[0], args[1])
		}
	case "list":
		if len(args) == 0 {
			return List(Mixed())
		}
		return List(args[0])
	case "non-empty-list":
		if len(args) == 0 {
			return NonEmptyList(Mixed())
		}
		return NonEmptyList(args[0])
	case "iterable":
		switch len(args) {
		case 0:
			return Iterable(Mixed(), Mixed())
		case 1:
			return Iterable(ArrayKey(), args[0])
		default:
			return Iterable(args[0], args[1])
		}
	case "callable", "closure":
		return Callable(nil, Mixed())
	case "class-string", "interface-string", "trait-string", "enum-string":
		if len(args) == 0 {
			return ClassString(Object())
		}
		return ClassString(args[0])
	case "self":
		return Self(args...)
	case "static", "$this":
		return Static(args...)
	case "parent":
		return Parent(args...)
	default:
		if allowTemplates && isTemplateName(name) {
			return Template(name)
		}
		return Named(name, args...)
	}
}

func builtinTypeName(name string) string {
	switch len(name) {
	case 3:
		if strings.EqualFold(name, "int") {
			return "int"
		}
	case 4:
		switch {
		case strings.EqualFold(name, "void"):
			return "void"
		case strings.EqualFold(name, "null"):
			return "null"
		case strings.EqualFold(name, "bool"):
			return "bool"
		case strings.EqualFold(name, "true"):
			return "true"
		case strings.EqualFold(name, "list"):
			return "list"
		case strings.EqualFold(name, "self"):
			return "self"
		}
	case 5:
		switch {
		case strings.EqualFold(name, "error"):
			return "error"
		case strings.EqualFold(name, "never"):
			return "never"
		case strings.EqualFold(name, "mixed"):
			return "mixed"
		case strings.EqualFold(name, "false"):
			return "false"
		case strings.EqualFold(name, "float"):
			return "float"
		case strings.EqualFold(name, "array"):
			return "array"
		case strings.EqualFold(name, "$this"):
			return "$this"
		}
	case 6:
		switch {
		case strings.EqualFold(name, "double"):
			return "double"
		case strings.EqualFold(name, "number"):
			return "number"
		case strings.EqualFold(name, "scalar"):
			return "scalar"
		case strings.EqualFold(name, "string"):
			return "string"
		case strings.EqualFold(name, "object"):
			return "object"
		case strings.EqualFold(name, "static"):
			return "static"
		case strings.EqualFold(name, "parent"):
			return "parent"
		}
	case 7:
		switch {
		case strings.EqualFold(name, "unknown"):
			return "unknown"
		case strings.EqualFold(name, "boolean"):
			return "boolean"
		case strings.EqualFold(name, "integer"):
			return "integer"
		case strings.EqualFold(name, "closure"):
			return "closure"
		}
	case 8:
		switch {
		case strings.EqualFold(name, "resource"):
			return "resource"
		case strings.EqualFold(name, "iterable"):
			return "iterable"
		case strings.EqualFold(name, "callable"):
			return "callable"
		}
	case 9:
		if strings.EqualFold(name, "array-key") {
			return "array-key"
		}
	case 11:
		if strings.EqualFold(name, "enum-string") {
			return "enum-string"
		}
	case 12:
		switch {
		case strings.EqualFold(name, "positive-int"):
			return "positive-int"
		case strings.EqualFold(name, "negative-int"):
			return "negative-int"
		case strings.EqualFold(name, "class-string"):
			return "class-string"
		case strings.EqualFold(name, "trait-string"):
			return "trait-string"
		}
	case 14:
		switch {
		case strings.EqualFold(name, "numeric-string"):
			return "numeric-string"
		case strings.EqualFold(name, "literal-string"):
			return "literal-string"
		case strings.EqualFold(name, "non-empty-list"):
			return "non-empty-list"
		}
	case 15:
		if strings.EqualFold(name, "non-empty-array") {
			return "non-empty-array"
		}
	case 16:
		switch {
		case strings.EqualFold(name, "non-negative-int"):
			return "non-negative-int"
		case strings.EqualFold(name, "non-empty-string"):
			return "non-empty-string"
		case strings.EqualFold(name, "interface-string"):
			return "interface-string"
		}
	}
	return ""
}

func isTemplateName(name string) bool {
	if strings.Contains(name, "\\") || strings.HasPrefix(name, "$") {
		return false
	}
	if len(name) == 1 {
		return name[0] >= 'A' && name[0] <= 'Z'
	}
	return strings.HasPrefix(name, "T") &&
		len(name) > 1 && name[1] >= 'A' && name[1] <= 'Z'
}

func (p *typeParser) parseName() string {
	p.skipSpace()
	start := p.position
	if !p.atEnd() && p.peek() == '\\' {
		p.position++
	}
	if !p.atEnd() && p.peek() == '$' {
		p.position++
	}
	for !p.atEnd() {
		if isTypeNameByte(p.peek()) {
			p.position++
			continue
		}
		break
	}
	return p.source[start:p.position]
}

func (p *typeParser) parseNumber() (string, bool) {
	start := p.position
	if !p.atEnd() && (p.peek() == '+' || p.peek() == '-') {
		p.position++
	}
	if p.position+2 <= len(p.source) && p.source[p.position] == '0' {
		prefix := p.source[p.position+1]
		var validDigit func(byte) bool
		switch prefix {
		case 'x', 'X':
			validDigit = func(value byte) bool {
				return value >= '0' && value <= '9' ||
					value >= 'a' && value <= 'f' ||
					value >= 'A' && value <= 'F'
			}
		case 'b', 'B':
			validDigit = func(value byte) bool { return value == '0' || value == '1' }
		case 'o', 'O':
			validDigit = func(value byte) bool { return value >= '0' && value <= '7' }
		}
		if validDigit != nil {
			p.position += 2
			for !p.atEnd() && (validDigit(p.peek()) || p.peek() == '_') {
				p.position++
			}
			return p.source[start:p.position], false
		}
	}

	floatingPoint := false
	for !p.atEnd() && (isDecimalDigit(p.peek()) || p.peek() == '_') {
		p.position++
	}
	if !p.atEnd() && p.peek() == '.' {
		floatingPoint = true
		p.position++
		for !p.atEnd() && (isDecimalDigit(p.peek()) || p.peek() == '_') {
			p.position++
		}
	}
	if !p.atEnd() && (p.peek() == 'e' || p.peek() == 'E') {
		floatingPoint = true
		p.position++
		if !p.atEnd() && (p.peek() == '+' || p.peek() == '-') {
			p.position++
		}
		for !p.atEnd() && (isDecimalDigit(p.peek()) || p.peek() == '_') {
			p.position++
		}
	}
	return p.source[start:p.position], floatingPoint
}

func (p *typeParser) parseQuoted() (string, error) {
	quote := p.peek()
	start := p.position
	p.position++
	escaped := false
	for !p.atEnd() {
		value := p.peek()
		p.position++
		if escaped {
			escaped = false
			continue
		}
		if value == '\\' {
			escaped = true
			continue
		}
		if value == quote {
			raw := p.source[start:p.position]
			decoded, err := strconv.Unquote(raw)
			if err != nil {
				return raw[1 : len(raw)-1], nil
			}
			return decoded, nil
		}
	}
	return "", p.errorf("unterminated string")
}

func (p *typeParser) consume(text string) bool {
	p.skipSpace()
	if !strings.HasPrefix(p.source[p.position:], text) {
		return false
	}
	p.position += len(text)
	return true
}

func (p *typeParser) consumeWord(word string) bool {
	p.skipSpace()
	rest := p.source[p.position:]
	if len(rest) < len(word) ||
		!strings.EqualFold(rest[:len(word)], word) {
		return false
	}
	if len(rest) > len(word) {
		next := rest[len(word)]
		if isTypeNameByte(next) && next != '-' && next != '$' {
			return false
		}
	}
	p.position += len(word)
	return true
}

func (p *typeParser) skipSpace() {
	for !p.atEnd() && unicode.IsSpace(rune(p.peek())) {
		p.position++
	}
}

func (p *typeParser) atEnd() bool {
	return p.position >= len(p.source)
}

func (p *typeParser) peek() byte {
	return p.source[p.position]
}

func (p *typeParser) errorf(format string, values ...any) error {
	return fmt.Errorf("parse PHP type at byte %d: %s", p.position, fmt.Sprintf(format, values...))
}

func isNumberStart(value byte) bool {
	return value >= '0' && value <= '9' || value == '+' || value == '-'
}

func isDecimalDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

func isTypeNameByte(value byte) bool {
	return value >= 0x80 ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' ||
		value == '_' || value == '\\' || value == '-' || value == '$'
}
