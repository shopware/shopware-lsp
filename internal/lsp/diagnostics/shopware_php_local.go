package diagnostics

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/suppression"
)

const (
	ShopwarePHPSuperglobalCode        lsp.DiagnosticID = "shopware.php.superglobal"
	ShopwarePHPDisallowedFunctionCode lsp.DiagnosticID = "shopware.php.function.disallowed"
	ShopwarePHPSessionFunctionCode    lsp.DiagnosticID = "shopware.php.session.function"
	ShopwarePHPGlobBraceCode          lsp.DiagnosticID = "shopware.php.glob_brace"
	ShopwarePHPTLSVerificationCode    lsp.DiagnosticID = "shopware.php.tls.verification_disabled"
	ShopwarePHPPredictableSaltCode    lsp.DiagnosticID = "shopware.php.crypto.predictable_salt"
	ShopwarePHPWeakKeyCode            lsp.DiagnosticID = "shopware.php.crypto.weak_key"
	ShopwarePHPInsecureCookieCode     lsp.DiagnosticID = "shopware.php.cookie.insecure"
	ShopwarePHPForeignKeyChecksCode   lsp.DiagnosticID = "shopware.php.foreign_key_checks.disabled"
)

var disabledForeignKeyChecksPattern = regexp.MustCompile(
	`(?i)\bFOREIGN_KEY_CHECKS\s*=\s*0\b`,
)

var forbiddenSuperglobals = map[string]struct{}{
	"_GET":     {},
	"_POST":    {},
	"_FILES":   {},
	"_REQUEST": {},
}

var disallowedPHPFunctions = map[string]struct{}{
	"var_dump": {},
	"exit":     {},
	"die":      {},
	"dd":       {},
	"dump":     {},
}

var disallowedPHPSessionFunctions = map[string]struct{}{
	"session_write_close": {},
	"session_start":       {},
	"session_destroy":     {},
}

// ShopwarePHPLocalAnalyzer performs cheap, syntax-local Shopware checks in a
// single CST walk. It deliberately does not construct a PHP semantic document.
type ShopwarePHPLocalAnalyzer struct{}

func NewShopwarePHPLocalAnalyzer() *ShopwarePHPLocalAnalyzer {
	return &ShopwarePHPLocalAnalyzer{}
}

func (*ShopwarePHPLocalAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if document == nil || document.SyntaxLanguage != language.PHP ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}

	root := document.SyntaxTree.Root
	result := make([]lsp.Problem, 0)
	var nameResolver *php.NameResolver
	phpquery.Visit(
		root,
		func(node *phpsyntax.Node) bool {
			if ctx.Err() != nil {
				return false
			}
			switch node.Kind() {
			case phpsyntax.PhpVariable:
				if _, forbidden := forbiddenSuperglobals[phpquery.VariableName(node)]; forbidden {
					name := phpquery.VariableKey(node)
					result = append(result, shopwarePHPLocalProblem(
						ShopwarePHPSuperglobalCode,
						node.RangeTrimmedTrivia(),
						"Do not use superglobal "+name+"; use the request object instead",
					))
				}
			case phpsyntax.PhpFunctionCall:
				result = append(result, shopwarePHPFunctionProblems(node)...)
			case phpsyntax.PhpName:
				if problem, found := shopwarePHPNameProblem(node); found {
					result = append(result, problem)
				}
			case phpsyntax.PhpString:
				value := phpquery.StringValue(node)
				if containsASCIIFoldLocal(value, "FOREIGN_KEY_CHECKS") &&
					disabledForeignKeyChecksPattern.MatchString(value) {
					if nameResolver == nil {
						nameResolver = php.NewNameResolver(root)
					}
					if problem, found := shopwarePHPForeignKeyProblem(node, nameResolver); found {
						result = append(result, problem)
					}
				}
			}
			return true
		},
		phpsyntax.PhpVariable,
		phpsyntax.PhpFunctionCall,
		phpsyntax.PhpName,
		phpsyntax.PhpString,
	)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	suppressions := suppression.Parse(document.Source)
	filtered := result[:0]
	for _, problem := range result {
		if suppressions.Suppresses(problem.Range.Start, string(problem.ID)) {
			continue
		}
		filtered = append(filtered, problem)
	}
	result = filtered
	sort.SliceStable(result, func(left, right int) bool {
		return result[left].Range.Start < result[right].Range.Start
	})
	return result, nil
}

func shopwarePHPFunctionProblems(call *phpsyntax.Node) []lsp.Problem {
	name := normalizedLocalFunctionName(call)
	if name == "" {
		return nil
	}
	nameRange := phpCallNameRange(call)
	if _, forbidden := disallowedPHPFunctions[name]; forbidden {
		return []lsp.Problem{shopwarePHPLocalProblem(
			ShopwarePHPDisallowedFunctionCode,
			nameRange,
			"Do not use "+name+"() in application code",
		)}
	}
	if _, forbidden := disallowedPHPSessionFunctions[name]; forbidden {
		return []lsp.Problem{shopwarePHPLocalProblem(
			ShopwarePHPSessionFunctionCode,
			nameRange,
			"Do not use "+name+"(); use Symfony's session from the request instead",
		)}
	}

	switch name {
	case "curl_setopt":
		if problem, found := disabledCurlOptionProblem(call); found {
			return []lsp.Problem{problem}
		}
	case "curl_setopt_array":
		if problem, found := disabledCurlOptionArrayProblem(call); found {
			return []lsp.Problem{problem}
		}
	case "stream_context_create":
		if problem, found := disabledStreamContextProblem(call); found {
			return []lsp.Problem{problem}
		}
	case "crypt":
		if salt := localCallArgument(call, "salt", 1); salt != nil &&
			unwrapPHPParentheses(salt).Kind() == phpsyntax.PhpString {
			return []lsp.Problem{shopwarePHPLocalProblem(
				ShopwarePHPPredictableSaltCode,
				unwrapPHPParentheses(salt).RangeTrimmedTrivia(),
				"Hard-coded crypt() salts are predictable; generate a random salt instead",
			)}
		}
	case "password_hash":
		if problem, found := passwordHashSaltProblem(call); found {
			return []lsp.Problem{problem}
		}
	case "openssl_pkey_new":
		if problem, found := weakOpenSSLKeyProblem(call); found {
			return []lsp.Problem{problem}
		}
	case "setcookie", "setrawcookie":
		if problem, found := insecureNativeCookieProblem(call, name); found {
			return []lsp.Problem{problem}
		}
	}
	return nil
}

func shopwarePHPNameProblem(node *phpsyntax.Node) (lsp.Problem, bool) {
	name := strings.ToLower(strings.TrimPrefix(phpquery.NameValue(node), `\`))
	if (name == "exit" || name == "die") && isStandalonePHPName(node) {
		return shopwarePHPLocalProblem(
			ShopwarePHPDisallowedFunctionCode,
			node.RangeTrimmedTrivia(),
			"Do not use "+name+" in application code",
		), true
	}
	if !strings.EqualFold(strings.TrimPrefix(phpquery.NameValue(node), `\`), "GLOB_BRACE") ||
		!isBarePHPConstantName(node) {
		return lsp.Problem{}, false
	}
	return shopwarePHPLocalProblem(
		ShopwarePHPGlobBraceCode,
		node.RangeTrimmedTrivia(),
		"GLOB_BRACE is not portable across all supported platforms",
	), true
}

func isStandalonePHPName(node *phpsyntax.Node) bool {
	return node != nil && node.Parent() != nil &&
		node.Parent().Kind() == phpsyntax.PhpExpressionStatement
}

func isBarePHPConstantName(node *phpsyntax.Node) bool {
	if node == nil || node.Parent() == nil {
		return false
	}
	switch node.Parent().Kind() {
	case phpsyntax.PhpFunctionCall,
		phpsyntax.PhpMemberCall,
		phpsyntax.PhpScopedCall,
		phpsyntax.PhpMemberAccess,
		phpsyntax.PhpScopedAccess,
		phpsyntax.PhpObjectCreation,
		phpsyntax.PhpAttribute,
		phpsyntax.PhpUseDeclaration,
		phpsyntax.PhpConstDeclaration,
		phpsyntax.PhpClassConstDeclaration,
		phpsyntax.PhpClassDeclaration,
		phpsyntax.PhpInterfaceDeclaration,
		phpsyntax.PhpTraitDeclaration,
		phpsyntax.PhpEnumDeclaration,
		phpsyntax.PhpFunctionDeclaration,
		phpsyntax.PhpMethodDeclaration,
		phpsyntax.PhpType:
		return false
	default:
		return true
	}
}

func disabledCurlOptionProblem(call *phpsyntax.Node) (lsp.Problem, bool) {
	option := localCallArgument(call, "option", 1)
	value := localCallArgument(call, "value", 2)
	if option == nil || value == nil {
		return lsp.Problem{}, false
	}
	optionName := normalizedPHPConstantName(option)
	if !disabledTLSOptionValue(optionName, value) {
		return lsp.Problem{}, false
	}
	return disabledTLSProblem(optionName, value), true
}

func disabledCurlOptionArrayProblem(call *phpsyntax.Node) (lsp.Problem, bool) {
	options := unwrapPHPParentheses(localCallArgument(call, "options", 1))
	if options == nil || options.Kind() != phpsyntax.PhpArray {
		return lsp.Problem{}, false
	}
	for _, item := range phpquery.ArrayItems(options) {
		key := phpquery.ArrayItemKey(item)
		value := phpquery.ArrayItemValue(item)
		optionName := normalizedPHPConstantName(key)
		if value != nil && disabledTLSOptionValue(optionName, value) {
			return disabledTLSProblem(optionName, value), true
		}
	}
	return lsp.Problem{}, false
}

func disabledStreamContextProblem(call *phpsyntax.Node) (lsp.Problem, bool) {
	options := unwrapPHPParentheses(localCallArgument(call, "options", 0))
	ssl, _, found := localArrayStringEntry(options, "ssl")
	ssl = unwrapPHPParentheses(ssl)
	if !found || ssl == nil || ssl.Kind() != phpsyntax.PhpArray {
		return lsp.Problem{}, false
	}
	for _, optionName := range []string{"verify_peer", "verify_peer_name"} {
		value, _, exists := localArrayStringEntry(ssl, optionName)
		if exists && isDisabledPHPBoolean(value) {
			return disabledTLSProblem(optionName, value), true
		}
	}
	return lsp.Problem{}, false
}

func disabledTLSOptionValue(optionName string, value *phpsyntax.Node) bool {
	switch optionName {
	case "CURLOPT_SSL_VERIFYPEER":
		return isDisabledPHPBoolean(value)
	case "CURLOPT_SSL_VERIFYHOST":
		value = unwrapPHPParentheses(value)
		if isDisabledPHPBoolean(value) {
			return true
		}
		integer, literal := phpIntegerLiteral(value)
		return literal && integer < 2
	default:
		return false
	}
}

func disabledTLSProblem(optionName string, value *phpsyntax.Node) lsp.Problem {
	return shopwarePHPLocalProblem(
		ShopwarePHPTLSVerificationCode,
		unwrapPHPParentheses(value).RangeTrimmedTrivia(),
		"TLS certificate verification is disabled by "+optionName+"; this permits man-in-the-middle attacks",
	)
}

func passwordHashSaltProblem(call *phpsyntax.Node) (lsp.Problem, bool) {
	options := unwrapPHPParentheses(localCallArgument(call, "options", 2))
	_, item, found := localArrayStringEntry(options, "salt")
	if !found {
		return lsp.Problem{}, false
	}
	key := phpquery.ArrayItemKey(item)
	rng := item.RangeTrimmedTrivia()
	if key != nil {
		rng = phpquery.StringContentRange(key)
	}
	return shopwarePHPLocalProblem(
		ShopwarePHPPredictableSaltCode,
		rng,
		"Do not provide a custom password_hash() salt; PHP generates a secure random salt",
	), true
}

func weakOpenSSLKeyProblem(call *phpsyntax.Node) (lsp.Problem, bool) {
	options := unwrapPHPParentheses(localCallArgument(call, "options", 0))
	value, _, found := localArrayStringEntry(options, "private_key_bits")
	bits, literal := phpIntegerLiteral(unwrapPHPParentheses(value))
	if !found || !literal || bits >= 2048 {
		return lsp.Problem{}, false
	}
	return shopwarePHPLocalProblem(
		ShopwarePHPWeakKeyCode,
		unwrapPHPParentheses(value).RangeTrimmedTrivia(),
		fmt.Sprintf("Weak cryptographic key size (%d bits); use at least 2048 bits for RSA keys", bits),
	), true
}

func insecureNativeCookieProblem(
	call *phpsyntax.Node,
	functionName string,
) (lsp.Problem, bool) {
	secure := localCallArgument(call, "secure", 5)
	if secure != nil {
		if value, literal := phpBooleanLiteral(secure); literal && !value {
			return insecureCookieProblem(functionName, secure, "explicitly disables the secure flag"), true
		}
		return lsp.Problem{}, false
	}

	options := unwrapPHPParentheses(localCallArgument(call, "expires_or_options", 2))
	if options != nil && options.Kind() == phpsyntax.PhpArray {
		value, _, found := localArrayStringEntry(options, "secure")
		if !found {
			return insecureCookieProblem(functionName, options, "omits the secure option"), true
		}
		if secureValue, literal := phpBooleanLiteral(value); literal && !secureValue {
			return insecureCookieProblem(functionName, value, "explicitly disables the secure option"), true
		}
		return lsp.Problem{}, false
	}
	if options != nil {
		// A dynamic third argument may be an options array containing secure=true.
		return lsp.Problem{}, false
	}
	return insecureCookieProblem(functionName, call, "omits the secure flag"), true
}

func insecureCookieProblem(
	functionName string,
	node *phpsyntax.Node,
	reason string,
) lsp.Problem {
	rng := node.RangeTrimmedTrivia()
	if node.Kind() == phpsyntax.PhpFunctionCall {
		rng = phpCallNameRange(node)
	}
	return shopwarePHPLocalProblem(
		ShopwarePHPInsecureCookieCode,
		rng,
		functionName+"() "+reason+"; cookies should be restricted to HTTPS",
	)
}

func shopwarePHPForeignKeyProblem(
	literal *phpsyntax.Node,
	resolver *php.NameResolver,
) (lsp.Problem, bool) {
	method := phpquery.MethodAt(literal)
	class := phpquery.ClassAt(literal)
	if method == nil || class == nil ||
		!strings.EqualFold(phpquery.MethodName(method), "update") {
		return lsp.Problem{}, false
	}
	allowedClass := false
	for _, parent := range phpquery.ClassExtends(class) {
		resolved := strings.TrimPrefix(resolver.Resolve(parent), `\`)
		if resolved == "Shopware\\Core\\Framework\\Migration\\MigrationStep" ||
			resolved == "Shopware\\Core\\Framework\\Plugin" {
			allowedClass = true
			break
		}
	}
	if !allowedClass {
		return lsp.Problem{}, false
	}
	match := disabledForeignKeyChecksPattern.FindStringIndex(phpquery.StringValue(literal))
	if len(match) != 2 {
		return lsp.Problem{}, false
	}
	rng := phpquery.StringContentRange(literal)
	rng.Start += uint32(match[0])
	rng.End = rng.Start + uint32(match[1]-match[0])
	return shopwarePHPLocalProblem(
		ShopwarePHPForeignKeyChecksCode,
		rng,
		"Do not disable foreign-key checks in migrations; delete data in dependency order",
	), true
}

func normalizedLocalFunctionName(call *phpsyntax.Node) string {
	nameNode := phpquery.DirectChild(call, phpsyntax.PhpName)
	name := strings.TrimSpace(phpquery.NameValue(nameNode))
	absolute := strings.HasPrefix(name, `\`)
	name = strings.TrimPrefix(name, `\`)
	if name == "" || strings.Contains(name, `\`) && !absolute {
		return ""
	}
	return strings.ToLower(name)
}

func normalizedPHPConstantName(node *phpsyntax.Node) string {
	node = unwrapPHPParentheses(node)
	if node == nil || node.Kind() != phpsyntax.PhpName {
		return ""
	}
	return strings.ToUpper(strings.TrimPrefix(phpquery.NameValue(node), `\`))
}

func phpCallNameRange(call *phpsyntax.Node) phpsyntax.TextRange {
	if name := phpquery.DirectChild(call, phpsyntax.PhpName); name != nil {
		return name.RangeTrimmedTrivia()
	}
	return call.RangeTrimmedTrivia()
}

func localCallArgument(
	call *phpsyntax.Node,
	parameterName string,
	position int,
) *phpsyntax.Node {
	positional := 0
	for _, argument := range phpquery.Arguments(call) {
		name := phpquery.ArgumentName(argument)
		if name != "" {
			if strings.EqualFold(name, parameterName) {
				return localArgumentExpression(argument)
			}
			continue
		}
		if positional == position {
			return localArgumentExpression(argument)
		}
		positional++
	}
	return nil
}

func localArgumentExpression(argument *phpsyntax.Node) *phpsyntax.Node {
	if argument == nil {
		return nil
	}
	skipNamedParameter := argument.Kind() == phpsyntax.PhpNamedArgument
	for index := 0; index < argument.ChildCount(); index++ {
		child, ok := argument.Child(index).(*phpsyntax.Node)
		if !ok {
			continue
		}
		if skipNamedParameter && child.Kind() == phpsyntax.PhpName {
			skipNamedParameter = false
			continue
		}
		return child
	}
	return nil
}

func localArrayStringEntry(
	array *phpsyntax.Node,
	name string,
) (value, item *phpsyntax.Node, found bool) {
	array = unwrapPHPParentheses(array)
	if array == nil || array.Kind() != phpsyntax.PhpArray {
		return nil, nil, false
	}
	for _, candidate := range phpquery.ArrayItems(array) {
		key := phpquery.ArrayItemKey(candidate)
		if key == nil || !strings.EqualFold(phpquery.StringValue(key), name) {
			continue
		}
		return phpquery.ArrayItemValue(candidate), candidate, true
	}
	return nil, nil, false
}

func unwrapPHPParentheses(node *phpsyntax.Node) *phpsyntax.Node {
	for node != nil && node.Kind() == phpsyntax.PhpParenthesized {
		var child *phpsyntax.Node
		for index := 0; index < node.ChildCount(); index++ {
			candidate, ok := node.Child(index).(*phpsyntax.Node)
			if ok {
				child = candidate
				break
			}
		}
		if child == nil {
			break
		}
		node = child
	}
	return node
}

func phpBooleanLiteral(node *phpsyntax.Node) (bool, bool) {
	node = unwrapPHPParentheses(node)
	if node == nil {
		return false, false
	}
	switch node.Kind() {
	case phpsyntax.PhpBoolean:
		value := strings.ToLower(strings.TrimSpace(node.Text()))
		return value == "true", value == "true" || value == "false"
	case phpsyntax.PhpNull:
		return false, true
	case phpsyntax.PhpNumber:
		value, found := phpIntegerLiteral(node)
		return value != 0, found
	default:
		return false, false
	}
}

func isDisabledPHPBoolean(node *phpsyntax.Node) bool {
	value, literal := phpBooleanLiteral(node)
	return literal && !value
}

func phpIntegerLiteral(node *phpsyntax.Node) (int64, bool) {
	if node == nil || node.Kind() != phpsyntax.PhpNumber {
		return 0, false
	}
	text := strings.ReplaceAll(strings.TrimSpace(node.Text()), "_", "")
	value, err := strconv.ParseInt(text, 0, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func containsASCIIFoldLocal(source, needle string) bool {
	if needle == "" {
		return true
	}
	if len(source) < len(needle) {
		return false
	}
	first := lowerASCIIByte(needle[0])
	for start := 0; start+len(needle) <= len(source); start++ {
		if lowerASCIIByte(source[start]) != first {
			continue
		}
		if strings.EqualFold(source[start:start+len(needle)], needle) {
			return true
		}
	}
	return false
}

func lowerASCIIByte(value byte) byte {
	if value >= 'A' && value <= 'Z' {
		return value + ('a' - 'A')
	}
	return value
}

func shopwarePHPLocalProblem(
	code lsp.DiagnosticID,
	rng phpsyntax.TextRange,
	message string,
) lsp.Problem {
	return lsp.Problem{
		ID:       code,
		Range:    rng,
		Message:  message,
		Severity: protocol.DiagnosticSeverityWarning,
		Source:   "shopware-lsp",
	}
}
