package syntax

import "github.com/shopware/shopware-lsp/internal/parser/cst"

// The twig syntax kinds are ported 1:1 from ludtwig's SyntaxKind enum
// (syntax/untyped.rs). The discriminants are sequential from 0 with tokens
// first and ROOT last; the exact order is ABI in the Rust code and is preserved
// here. [Kind] itself is defined in the language-agnostic cst package and
// re-exported from this package (see aliases.go); twig registers this inventory
// with the cst kind registry in init below.
const (
	// Generic lexer tokens.
	TkWhitespace Kind = iota // TK_WHITESPACE = 0
	TkLineBreak
	TkWord
	TkTwigComponentName
	TkNumber
	TkHtmlEscapeCharacter
	TkDot
	TkDoubleDot
	TkTripleDot
	TkComma
	TkColon
	TkSemicolon
	TkExclamationMark
	TkExclamationMarkEquals
	TkExclamationMarkDoubleEquals
	TkQuestionMark
	TkDoubleQuestionMark
	TkPercent
	TkTilde
	TkSinglePipe
	TkDoublePipe
	TkAmpersand
	TkDoubleAmpersand
	TkForwardSlash
	TkDoubleForwardSlash
	TkBackwardSlash
	TkOpenParenthesis
	TkCloseParenthesis
	TkOpenCurly
	TkCloseCurly
	TkOpenSquare
	TkCloseSquare
	TkLessThan
	TkLessThanEqual
	TkLessThanEqualGreaterThan
	TkLessThanSlash
	TkLessThanExclamationMark
	TkDoctype
	TkGreaterThan
	TkGreaterThanEqual
	TkEqualGreaterThan
	TkSlashGreaterThan
	TkLessThanExclamationMarkMinusMinus
	TkMinusMinusGreaterThan
	TkEqual
	TkDoubleEqual
	TkTripleEqual
	TkPlus
	TkMinus
	TkStar
	TkDoubleStar
	TkDoubleQuotes
	TkSingleQuotes
	TkGraveAccentQuotes
	TkCurlyPercent
	TkCurlyPercentMinus
	TkCurlyPercentTilde
	TkPercentCurly
	TkMinusPercentCurly
	TkTildePercentCurly
	TkOpenCurlyCurly
	TkOpenCurlyCurlyMinus
	TkOpenCurlyCurlyTilde
	TkCloseCurlyCurly
	TkMinusCloseCurlyCurly
	TkTildeCloseCurlyCurly
	TkOpenCurlyHashtag
	TkOpenCurlyHashtagMinus
	TkOpenCurlyHashtagTilde
	TkHashtagCloseCurly
	TkMinusHashtagCloseCurly
	TkTildeHashtagCloseCurly
	TkHashtag
	TkTrue
	TkFalse

	// Twig tag tokens.
	TkBlock
	TkEndblock
	TkIf
	TkElseIf
	TkElse
	TkEndif
	TkApply
	TkEndapply
	TkAutoescape
	TkEndautoescape
	TkCache
	TkEndcache
	TkDeprecated
	TkDo
	TkEmbed
	TkEndembed
	TkExtends
	TkFlush
	TkFor
	TkEndfor
	TkFrom
	TkImport
	TkMacro
	TkEndmacro
	TkSandbox
	TkEndsandbox
	TkSet
	TkEndset
	TkUse
	TkVerbatim
	TkEndverbatim
	TkOnly
	TkIgnoreMissing
	TkWith
	TkEndwith
	TkTtl
	TkTags
	TkProps
	TkComponent
	TkEndcomponent

	// Twig operators.
	TkNot
	TkNotIn
	TkOr
	TkAnd
	TkBinaryOr
	TkBinaryXor
	TkBinaryAnd
	TkIn
	TkMatches
	TkStartsWith
	TkEndsWith
	TkIs
	TkIsNot

	// Twig tests.
	TkEven
	TkOdd
	TkDefined
	TkSameAs
	TkAs
	TkNone
	TkNull
	TkDivisibleBy
	TkConstant
	TkEmpty
	TkIterable

	// Twig functions.
	TkMax
	TkMin
	TkRange
	TkCycle
	TkRandom
	TkDate
	TkInclude
	TkSource

	// Drupal Trans.
	TkTrans
	TkEndtrans

	// Shopware specific.
	TkSwExtends
	TkSwSilentFeatureCall
	TkEndswSilentFeatureCall
	TkSwInclude
	TkReturn
	TkSwIcon
	TkSwThumbnails
	TkStyle
	TkSwEmbed
	TkSwEndEmbed
	TkSwUse
	TkSwImport
	TkSwFrom

	// Special tokens.
	TkLudtwigIgnoreFile
	TkLudtwigIgnore
	TkUnknown

	// Composite nodes — Twig expressions.
	Body
	TwigVar
	TwigExpression
	TwigBinaryExpression
	TwigUnaryExpression
	TwigParenthesesExpression
	TwigConditionalExpression
	TwigOperand
	TwigAccessor
	TwigFilter
	TwigIndexLookup
	TwigIndex
	TwigIndexRange
	TwigFunctionCall
	TwigArrowFunction
	TwigArguments
	TwigNamedArgument

	// Composite nodes — Twig literals.
	TwigLiteralString
	TwigLiteralStringInner
	TwigLiteralStringInterpolation
	TwigLiteralNumber
	TwigLiteralArray
	TwigLiteralArrayInner
	TwigLiteralNull
	TwigLiteralBoolean
	TwigLiteralHash
	TwigLiteralHashItems
	TwigLiteralHashPair
	TwigLiteralHashKey
	TwigLiteralHashValue
	TwigLiteralName

	// Composite nodes — Twig block structures.
	TwigComment
	TwigBlock
	TwigStartingBlock
	TwigEndingBlock
	TwigIf
	TwigIfBlock
	TwigElseIfBlock
	TwigElseBlock
	TwigEndifBlock
	TwigSet
	TwigSetBlock
	TwigEndsetBlock
	TwigAssignment
	TwigFor
	TwigForBlock
	TwigForElseBlock
	TwigEndforBlock
	TwigExtends
	TwigInclude
	TwigIncludeWith
	TwigUse
	TwigOverride
	TwigApply
	TwigApplyStartingBlock
	TwigApplyEndingBlock
	TwigAutoescape
	TwigAutoescapeStartingBlock
	TwigAutoescapeEndingBlock
	TwigDeprecated
	TwigDo
	TwigEmbed
	TwigEmbedStartingBlock
	TwigEmbedEndingBlock
	TwigFlush
	TwigFrom
	TwigImport
	TwigSandbox
	TwigSandboxStartingBlock
	TwigSandboxEndingBlock
	TwigVerbatim
	TwigVerbatimStartingBlock
	TwigVerbatimEndingBlock
	TwigMacro
	TwigMacroStartingBlock
	TwigMacroEndingBlock
	TwigWith
	TwigWithStartingBlock
	TwigWithEndingBlock
	TwigCache
	TwigCacheTtl
	TwigCacheTags
	TwigCacheStartingBlock
	TwigCacheEndingBlock
	TwigProps
	TwigPropDeclaration
	TwigComponent
	TwigComponentStartingBlock
	TwigComponentEndingBlock
	TwigFormTheme

	// Drupal Trans nodes.
	TwigTrans
	TwigTransStartingBlock
	TwigTransEndingBlock

	// Shopware specific nodes.
	ShopwareTwigSwExtends
	ShopwareTwigSwInclude
	ShopwareSilentFeatureCall
	ShopwareSilentFeatureCallStartingBlock
	ShopwareSilentFeatureCallEndingBlock
	ShopwareReturn
	ShopwareIcon
	ShopwareIconStyle
	ShopwareThumbnails
	ShopwareThumbnailsWith

	// HTML nodes.
	HtmlDoctype
	HtmlAttributeList
	HtmlAttribute
	HtmlString
	HtmlStringInner
	HtmlText
	HtmlRawText
	HtmlComment
	HtmlTag
	HtmlStartingTag
	HtmlEndingTag

	// Ludtwig directives.
	LudtwigDirectiveFileIgnore
	LudtwigDirectiveIgnore
	LudtwigDirectiveRuleList

	// Special nodes.
	Error
	Root // last ported ludtwig variant; its ordinal is part of the CST ABI

	// Native extension nodes must be appended after Root so adding support for
	// framework-specific tags cannot renumber the ported CST inventory.
	TwigAssetic
	TwigAsseticStartingBlock
	TwigAsseticEndingBlock
	TwigTypes
)

// init registers the twig language with the cst kind registry. Twig owns the
// range starting at Base 0, preserving the historical ordinals: the flat lookup
// tables the registry materializes drive Kind.String/TokenText/IsTrivia/IsToken
// for every twig kind. Whitespace and line breaks are the only trivia; comments
// are proper nodes. Kinds below Body are tokens, the rest composite nodes.
func init() {
	cst.RegisterLanguage(cst.LanguageSpec{
		Name:        "twig",
		Base:        0,
		KindNames:   kindNames[:],
		TokenTexts:  tokenTexts[:],
		FirstNode:   Body,
		TriviaKinds: []Kind{TkWhitespace, TkLineBreak},
	})
}

// kindNames maps each Kind to its exact Rust variant name.
var kindNames = [...]string{
	TkWhitespace:                           "TK_WHITESPACE",
	TkLineBreak:                            "TK_LINE_BREAK",
	TkWord:                                 "TK_WORD",
	TkTwigComponentName:                    "TK_TWIG_COMPONENT_NAME",
	TkNumber:                               "TK_NUMBER",
	TkHtmlEscapeCharacter:                  "TK_HTML_ESCAPE_CHARACTER",
	TkDot:                                  "TK_DOT",
	TkDoubleDot:                            "TK_DOUBLE_DOT",
	TkTripleDot:                            "TK_TRIPLE_DOT",
	TkComma:                                "TK_COMMA",
	TkColon:                                "TK_COLON",
	TkSemicolon:                            "TK_SEMICOLON",
	TkExclamationMark:                      "TK_EXCLAMATION_MARK",
	TkExclamationMarkEquals:                "TK_EXCLAMATION_MARK_EQUALS",
	TkExclamationMarkDoubleEquals:          "TK_EXCLAMATION_MARK_DOUBLE_EQUALS",
	TkQuestionMark:                         "TK_QUESTION_MARK",
	TkDoubleQuestionMark:                   "TK_DOUBLE_QUESTION_MARK",
	TkPercent:                              "TK_PERCENT",
	TkTilde:                                "TK_TILDE",
	TkSinglePipe:                           "TK_SINGLE_PIPE",
	TkDoublePipe:                           "TK_DOUBLE_PIPE",
	TkAmpersand:                            "TK_AMPERSAND",
	TkDoubleAmpersand:                      "TK_DOUBLE_AMPERSAND",
	TkForwardSlash:                         "TK_FORWARD_SLASH",
	TkDoubleForwardSlash:                   "TK_DOUBLE_FORWARD_SLASH",
	TkBackwardSlash:                        "TK_BACKWARD_SLASH",
	TkOpenParenthesis:                      "TK_OPEN_PARENTHESIS",
	TkCloseParenthesis:                     "TK_CLOSE_PARENTHESIS",
	TkOpenCurly:                            "TK_OPEN_CURLY",
	TkCloseCurly:                           "TK_CLOSE_CURLY",
	TkOpenSquare:                           "TK_OPEN_SQUARE",
	TkCloseSquare:                          "TK_CLOSE_SQUARE",
	TkLessThan:                             "TK_LESS_THAN",
	TkLessThanEqual:                        "TK_LESS_THAN_EQUAL",
	TkLessThanEqualGreaterThan:             "TK_LESS_THAN_EQUAL_GREATER_THAN",
	TkLessThanSlash:                        "TK_LESS_THAN_SLASH",
	TkLessThanExclamationMark:              "TK_LESS_THAN_EXCLAMATION_MARK",
	TkDoctype:                              "TK_DOCTYPE",
	TkGreaterThan:                          "TK_GREATER_THAN",
	TkGreaterThanEqual:                     "TK_GREATER_THAN_EQUAL",
	TkEqualGreaterThan:                     "TK_EQUAL_GREATER_THAN",
	TkSlashGreaterThan:                     "TK_SLASH_GREATER_THAN",
	TkLessThanExclamationMarkMinusMinus:    "TK_LESS_THAN_EXCLAMATION_MARK_MINUS_MINUS",
	TkMinusMinusGreaterThan:                "TK_MINUS_MINUS_GREATER_THAN",
	TkEqual:                                "TK_EQUAL",
	TkDoubleEqual:                          "TK_DOUBLE_EQUAL",
	TkTripleEqual:                          "TK_TRIPLE_EQUAL",
	TkPlus:                                 "TK_PLUS",
	TkMinus:                                "TK_MINUS",
	TkStar:                                 "TK_STAR",
	TkDoubleStar:                           "TK_DOUBLE_STAR",
	TkDoubleQuotes:                         "TK_DOUBLE_QUOTES",
	TkSingleQuotes:                         "TK_SINGLE_QUOTES",
	TkGraveAccentQuotes:                    "TK_GRAVE_ACCENT_QUOTES",
	TkCurlyPercent:                         "TK_CURLY_PERCENT",
	TkCurlyPercentMinus:                    "TK_CURLY_PERCENT_MINUS",
	TkCurlyPercentTilde:                    "TK_CURLY_PERCENT_TILDE",
	TkPercentCurly:                         "TK_PERCENT_CURLY",
	TkMinusPercentCurly:                    "TK_MINUS_PERCENT_CURLY",
	TkTildePercentCurly:                    "TK_TILDE_PERCENT_CURLY",
	TkOpenCurlyCurly:                       "TK_OPEN_CURLY_CURLY",
	TkOpenCurlyCurlyMinus:                  "TK_OPEN_CURLY_CURLY_MINUS",
	TkOpenCurlyCurlyTilde:                  "TK_OPEN_CURLY_CURLY_TILDE",
	TkCloseCurlyCurly:                      "TK_CLOSE_CURLY_CURLY",
	TkMinusCloseCurlyCurly:                 "TK_MINUS_CLOSE_CURLY_CURLY",
	TkTildeCloseCurlyCurly:                 "TK_TILDE_CLOSE_CURLY_CURLY",
	TkOpenCurlyHashtag:                     "TK_OPEN_CURLY_HASHTAG",
	TkOpenCurlyHashtagMinus:                "TK_OPEN_CURLY_HASHTAG_MINUS",
	TkOpenCurlyHashtagTilde:                "TK_OPEN_CURLY_HASHTAG_TILDE",
	TkHashtagCloseCurly:                    "TK_HASHTAG_CLOSE_CURLY",
	TkMinusHashtagCloseCurly:               "TK_MINUS_HASHTAG_CLOSE_CURLY",
	TkTildeHashtagCloseCurly:               "TK_TILDE_HASHTAG_CLOSE_CURLY",
	TkHashtag:                              "TK_HASHTAG",
	TkTrue:                                 "TK_TRUE",
	TkFalse:                                "TK_FALSE",
	TkBlock:                                "TK_BLOCK",
	TkEndblock:                             "TK_ENDBLOCK",
	TkIf:                                   "TK_IF",
	TkElseIf:                               "TK_ELSE_IF",
	TkElse:                                 "TK_ELSE",
	TkEndif:                                "TK_ENDIF",
	TkApply:                                "TK_APPLY",
	TkEndapply:                             "TK_ENDAPPLY",
	TkAutoescape:                           "TK_AUTOESCAPE",
	TkEndautoescape:                        "TK_ENDAUTOESCAPE",
	TkCache:                                "TK_CACHE",
	TkEndcache:                             "TK_ENDCACHE",
	TkDeprecated:                           "TK_DEPRECATED",
	TkDo:                                   "TK_DO",
	TkEmbed:                                "TK_EMBED",
	TkEndembed:                             "TK_ENDEMBED",
	TkExtends:                              "TK_EXTENDS",
	TkFlush:                                "TK_FLUSH",
	TkFor:                                  "TK_FOR",
	TkEndfor:                               "TK_ENDFOR",
	TkFrom:                                 "TK_FROM",
	TkImport:                               "TK_IMPORT",
	TkMacro:                                "TK_MACRO",
	TkEndmacro:                             "TK_ENDMACRO",
	TkSandbox:                              "TK_SANDBOX",
	TkEndsandbox:                           "TK_ENDSANDBOX",
	TkSet:                                  "TK_SET",
	TkEndset:                               "TK_ENDSET",
	TkUse:                                  "TK_USE",
	TkVerbatim:                             "TK_VERBATIM",
	TkEndverbatim:                          "TK_ENDVERBATIM",
	TkOnly:                                 "TK_ONLY",
	TkIgnoreMissing:                        "TK_IGNORE_MISSING",
	TkWith:                                 "TK_WITH",
	TkEndwith:                              "TK_ENDWITH",
	TkTtl:                                  "TK_TTL",
	TkTags:                                 "TK_TAGS",
	TkProps:                                "TK_PROPS",
	TkComponent:                            "TK_COMPONENT",
	TkEndcomponent:                         "TK_ENDCOMPONENT",
	TkNot:                                  "TK_NOT",
	TkNotIn:                                "TK_NOT_IN",
	TkOr:                                   "TK_OR",
	TkAnd:                                  "TK_AND",
	TkBinaryOr:                             "TK_BINARY_OR",
	TkBinaryXor:                            "TK_BINARY_XOR",
	TkBinaryAnd:                            "TK_BINARY_AND",
	TkIn:                                   "TK_IN",
	TkMatches:                              "TK_MATCHES",
	TkStartsWith:                           "TK_STARTS_WITH",
	TkEndsWith:                             "TK_ENDS_WITH",
	TkIs:                                   "TK_IS",
	TkIsNot:                                "TK_IS_NOT",
	TkEven:                                 "TK_EVEN",
	TkOdd:                                  "TK_ODD",
	TkDefined:                              "TK_DEFINED",
	TkSameAs:                               "TK_SAME_AS",
	TkAs:                                   "TK_AS",
	TkNone:                                 "TK_NONE",
	TkNull:                                 "TK_NULL",
	TkDivisibleBy:                          "TK_DIVISIBLE_BY",
	TkConstant:                             "TK_CONSTANT",
	TkEmpty:                                "TK_EMPTY",
	TkIterable:                             "TK_ITERABLE",
	TkMax:                                  "TK_MAX",
	TkMin:                                  "TK_MIN",
	TkRange:                                "TK_RANGE",
	TkCycle:                                "TK_CYCLE",
	TkRandom:                               "TK_RANDOM",
	TkDate:                                 "TK_DATE",
	TkInclude:                              "TK_INCLUDE",
	TkSource:                               "TK_SOURCE",
	TkTrans:                                "TK_TRANS",
	TkEndtrans:                             "TK_ENDTRANS",
	TkSwExtends:                            "TK_SW_EXTENDS",
	TkSwSilentFeatureCall:                  "TK_SW_SILENT_FEATURE_CALL",
	TkEndswSilentFeatureCall:               "TK_ENDSW_SILENT_FEATURE_CALL",
	TkSwInclude:                            "TK_SW_INCLUDE",
	TkReturn:                               "TK_RETURN",
	TkSwIcon:                               "TK_SW_ICON",
	TkSwThumbnails:                         "TK_SW_THUMBNAILS",
	TkStyle:                                "TK_STYLE",
	TkSwEmbed:                              "TK_SW_EMBED",
	TkSwEndEmbed:                           "TK_SW_END_EMBED",
	TkSwUse:                                "TK_SW_USE",
	TkSwImport:                             "TK_SW_IMPORT",
	TkSwFrom:                               "TK_SW_FROM",
	TkLudtwigIgnoreFile:                    "TK_LUDTWIG_IGNORE_FILE",
	TkLudtwigIgnore:                        "TK_LUDTWIG_IGNORE",
	TkUnknown:                              "TK_UNKNOWN",
	Body:                                   "BODY",
	TwigVar:                                "TWIG_VAR",
	TwigExpression:                         "TWIG_EXPRESSION",
	TwigBinaryExpression:                   "TWIG_BINARY_EXPRESSION",
	TwigUnaryExpression:                    "TWIG_UNARY_EXPRESSION",
	TwigParenthesesExpression:              "TWIG_PARENTHESES_EXPRESSION",
	TwigConditionalExpression:              "TWIG_CONDITIONAL_EXPRESSION",
	TwigOperand:                            "TWIG_OPERAND",
	TwigAccessor:                           "TWIG_ACCESSOR",
	TwigFilter:                             "TWIG_FILTER",
	TwigIndexLookup:                        "TWIG_INDEX_LOOKUP",
	TwigIndex:                              "TWIG_INDEX",
	TwigIndexRange:                         "TWIG_INDEX_RANGE",
	TwigFunctionCall:                       "TWIG_FUNCTION_CALL",
	TwigArrowFunction:                      "TWIG_ARROW_FUNCTION",
	TwigArguments:                          "TWIG_ARGUMENTS",
	TwigNamedArgument:                      "TWIG_NAMED_ARGUMENT",
	TwigLiteralString:                      "TWIG_LITERAL_STRING",
	TwigLiteralStringInner:                 "TWIG_LITERAL_STRING_INNER",
	TwigLiteralStringInterpolation:         "TWIG_LITERAL_STRING_INTERPOLATION",
	TwigLiteralNumber:                      "TWIG_LITERAL_NUMBER",
	TwigLiteralArray:                       "TWIG_LITERAL_ARRAY",
	TwigLiteralArrayInner:                  "TWIG_LITERAL_ARRAY_INNER",
	TwigLiteralNull:                        "TWIG_LITERAL_NULL",
	TwigLiteralBoolean:                     "TWIG_LITERAL_BOOLEAN",
	TwigLiteralHash:                        "TWIG_LITERAL_HASH",
	TwigLiteralHashItems:                   "TWIG_LITERAL_HASH_ITEMS",
	TwigLiteralHashPair:                    "TWIG_LITERAL_HASH_PAIR",
	TwigLiteralHashKey:                     "TWIG_LITERAL_HASH_KEY",
	TwigLiteralHashValue:                   "TWIG_LITERAL_HASH_VALUE",
	TwigLiteralName:                        "TWIG_LITERAL_NAME",
	TwigComment:                            "TWIG_COMMENT",
	TwigBlock:                              "TWIG_BLOCK",
	TwigStartingBlock:                      "TWIG_STARTING_BLOCK",
	TwigEndingBlock:                        "TWIG_ENDING_BLOCK",
	TwigIf:                                 "TWIG_IF",
	TwigIfBlock:                            "TWIG_IF_BLOCK",
	TwigElseIfBlock:                        "TWIG_ELSE_IF_BLOCK",
	TwigElseBlock:                          "TWIG_ELSE_BLOCK",
	TwigEndifBlock:                         "TWIG_ENDIF_BLOCK",
	TwigSet:                                "TWIG_SET",
	TwigSetBlock:                           "TWIG_SET_BLOCK",
	TwigEndsetBlock:                        "TWIG_ENDSET_BLOCK",
	TwigAssignment:                         "TWIG_ASSIGNMENT",
	TwigFor:                                "TWIG_FOR",
	TwigForBlock:                           "TWIG_FOR_BLOCK",
	TwigForElseBlock:                       "TWIG_FOR_ELSE_BLOCK",
	TwigEndforBlock:                        "TWIG_ENDFOR_BLOCK",
	TwigExtends:                            "TWIG_EXTENDS",
	TwigInclude:                            "TWIG_INCLUDE",
	TwigIncludeWith:                        "TWIG_INCLUDE_WITH",
	TwigUse:                                "TWIG_USE",
	TwigOverride:                           "TWIG_OVERRIDE",
	TwigApply:                              "TWIG_APPLY",
	TwigApplyStartingBlock:                 "TWIG_APPLY_STARTING_BLOCK",
	TwigApplyEndingBlock:                   "TWIG_APPLY_ENDING_BLOCK",
	TwigAutoescape:                         "TWIG_AUTOESCAPE",
	TwigAutoescapeStartingBlock:            "TWIG_AUTOESCAPE_STARTING_BLOCK",
	TwigAutoescapeEndingBlock:              "TWIG_AUTOESCAPE_ENDING_BLOCK",
	TwigDeprecated:                         "TWIG_DEPRECATED",
	TwigDo:                                 "TWIG_DO",
	TwigEmbed:                              "TWIG_EMBED",
	TwigEmbedStartingBlock:                 "TWIG_EMBED_STARTING_BLOCK",
	TwigEmbedEndingBlock:                   "TWIG_EMBED_ENDING_BLOCK",
	TwigFlush:                              "TWIG_FLUSH",
	TwigFrom:                               "TWIG_FROM",
	TwigImport:                             "TWIG_IMPORT",
	TwigSandbox:                            "TWIG_SANDBOX",
	TwigSandboxStartingBlock:               "TWIG_SANDBOX_STARTING_BLOCK",
	TwigSandboxEndingBlock:                 "TWIG_SANDBOX_ENDING_BLOCK",
	TwigVerbatim:                           "TWIG_VERBATIM",
	TwigVerbatimStartingBlock:              "TWIG_VERBATIM_STARTING_BLOCK",
	TwigVerbatimEndingBlock:                "TWIG_VERBATIM_ENDING_BLOCK",
	TwigMacro:                              "TWIG_MACRO",
	TwigMacroStartingBlock:                 "TWIG_MACRO_STARTING_BLOCK",
	TwigMacroEndingBlock:                   "TWIG_MACRO_ENDING_BLOCK",
	TwigWith:                               "TWIG_WITH",
	TwigWithStartingBlock:                  "TWIG_WITH_STARTING_BLOCK",
	TwigWithEndingBlock:                    "TWIG_WITH_ENDING_BLOCK",
	TwigCache:                              "TWIG_CACHE",
	TwigCacheTtl:                           "TWIG_CACHE_TTL",
	TwigCacheTags:                          "TWIG_CACHE_TAGS",
	TwigCacheStartingBlock:                 "TWIG_CACHE_STARTING_BLOCK",
	TwigCacheEndingBlock:                   "TWIG_CACHE_ENDING_BLOCK",
	TwigProps:                              "TWIG_PROPS",
	TwigPropDeclaration:                    "TWIG_PROP_DECLARATION",
	TwigComponent:                          "TWIG_COMPONENT",
	TwigComponentStartingBlock:             "TWIG_COMPONENT_STARTING_BLOCK",
	TwigComponentEndingBlock:               "TWIG_COMPONENT_ENDING_BLOCK",
	TwigFormTheme:                          "TWIG_FORM_THEME",
	TwigAssetic:                            "TWIG_ASSETIC",
	TwigAsseticStartingBlock:               "TWIG_ASSETIC_STARTING_BLOCK",
	TwigAsseticEndingBlock:                 "TWIG_ASSETIC_ENDING_BLOCK",
	TwigTypes:                              "TWIG_TYPES",
	TwigTrans:                              "TWIG_TRANS",
	TwigTransStartingBlock:                 "TWIG_TRANS_STARTING_BLOCK",
	TwigTransEndingBlock:                   "TWIG_TRANS_ENDING_BLOCK",
	ShopwareTwigSwExtends:                  "SHOPWARE_TWIG_SW_EXTENDS",
	ShopwareTwigSwInclude:                  "SHOPWARE_TWIG_SW_INCLUDE",
	ShopwareSilentFeatureCall:              "SHOPWARE_SILENT_FEATURE_CALL",
	ShopwareSilentFeatureCallStartingBlock: "SHOPWARE_SILENT_FEATURE_CALL_STARTING_BLOCK",
	ShopwareSilentFeatureCallEndingBlock:   "SHOPWARE_SILENT_FEATURE_CALL_ENDING_BLOCK",
	ShopwareReturn:                         "SHOPWARE_RETURN",
	ShopwareIcon:                           "SHOPWARE_ICON",
	ShopwareIconStyle:                      "SHOPWARE_ICON_STYLE",
	ShopwareThumbnails:                     "SHOPWARE_THUMBNAILS",
	ShopwareThumbnailsWith:                 "SHOPWARE_THUMBNAILS_WITH",
	HtmlDoctype:                            "HTML_DOCTYPE",
	HtmlAttributeList:                      "HTML_ATTRIBUTE_LIST",
	HtmlAttribute:                          "HTML_ATTRIBUTE",
	HtmlString:                             "HTML_STRING",
	HtmlStringInner:                        "HTML_STRING_INNER",
	HtmlText:                               "HTML_TEXT",
	HtmlRawText:                            "HTML_RAW_TEXT",
	HtmlComment:                            "HTML_COMMENT",
	HtmlTag:                                "HTML_TAG",
	HtmlStartingTag:                        "HTML_STARTING_TAG",
	HtmlEndingTag:                          "HTML_ENDING_TAG",
	LudtwigDirectiveFileIgnore:             "LUDTWIG_DIRECTIVE_FILE_IGNORE",
	LudtwigDirectiveIgnore:                 "LUDTWIG_DIRECTIVE_IGNORE",
	LudtwigDirectiveRuleList:               "LUDTWIG_DIRECTIVE_RULE_LIST",
	Error:                                  "ERROR",
	Root:                                   "ROOT",
}

// tokenTexts maps token kinds to their Display text (port of the Rust Display
// impl). Node kinds are left empty (Rust hits unreachable! for them).
var tokenTexts = [...]string{
	TkWhitespace:                        "whitespace",
	TkLineBreak:                         "line break",
	TkWord:                              "word",
	TkTwigComponentName:                 "twig component name",
	TkNumber:                            "number",
	TkHtmlEscapeCharacter:               "html escape character",
	TkDot:                               ".",
	TkDoubleDot:                         "..",
	TkTripleDot:                         "...",
	TkComma:                             ",",
	TkColon:                             ":",
	TkSemicolon:                         ";",
	TkExclamationMark:                   "!",
	TkExclamationMarkEquals:             "!=",
	TkExclamationMarkDoubleEquals:       "!==",
	TkQuestionMark:                      "?",
	TkDoubleQuestionMark:                "??",
	TkPercent:                           "%",
	TkTilde:                             "~",
	TkSinglePipe:                        "|",
	TkDoublePipe:                        "||",
	TkAmpersand:                         "&",
	TkDoubleAmpersand:                   "&&",
	TkForwardSlash:                      "/",
	TkDoubleForwardSlash:                "//",
	TkBackwardSlash:                     "\\",
	TkOpenParenthesis:                   "(",
	TkCloseParenthesis:                  ")",
	TkOpenCurly:                         "{",
	TkCloseCurly:                        "}",
	TkOpenSquare:                        "[",
	TkCloseSquare:                       "]",
	TkLessThan:                          "<",
	TkLessThanEqual:                     "<=",
	TkLessThanEqualGreaterThan:          "<=>",
	TkLessThanSlash:                     "</",
	TkLessThanExclamationMark:           "<!",
	TkDoctype:                           "DOCTYPE",
	TkGreaterThan:                       ">",
	TkGreaterThanEqual:                  ">=",
	TkEqualGreaterThan:                  "=>",
	TkSlashGreaterThan:                  "/>",
	TkLessThanExclamationMarkMinusMinus: "<!--",
	TkMinusMinusGreaterThan:             "-->",
	TkEqual:                             "=",
	TkDoubleEqual:                       "==",
	TkTripleEqual:                       "===",
	TkPlus:                              "+",
	TkMinus:                             "-",
	TkStar:                              "*",
	TkDoubleStar:                        "**",
	TkDoubleQuotes:                      "\"",
	TkSingleQuotes:                      "'",
	TkGraveAccentQuotes:                 "`",
	TkCurlyPercent:                      "{%",
	TkCurlyPercentMinus:                 "{%-",
	TkCurlyPercentTilde:                 "{%~",
	TkPercentCurly:                      "%}",
	TkMinusPercentCurly:                 "-%}",
	TkTildePercentCurly:                 "~%}",
	TkOpenCurlyCurly:                    "{{",
	TkOpenCurlyCurlyMinus:               "{{-",
	TkOpenCurlyCurlyTilde:               "{{~",
	TkCloseCurlyCurly:                   "}}",
	TkMinusCloseCurlyCurly:              "-}}",
	TkTildeCloseCurlyCurly:              "~}}",
	TkOpenCurlyHashtag:                  "{#",
	TkOpenCurlyHashtagMinus:             "{#-",
	TkOpenCurlyHashtagTilde:             "{#~",
	TkHashtagCloseCurly:                 "#}",
	TkMinusHashtagCloseCurly:            "-#}",
	TkTildeHashtagCloseCurly:            "~#}",
	TkHashtag:                           "#",
	TkTrue:                              "true",
	TkFalse:                             "false",
	TkBlock:                             "block",
	TkEndblock:                          "endblock",
	TkIf:                                "if",
	TkElseIf:                            "elseif",
	TkElse:                              "else",
	TkEndif:                             "endif",
	TkApply:                             "apply",
	TkEndapply:                          "endapply",
	TkAutoescape:                        "autoescape",
	TkEndautoescape:                     "endautoescape",
	TkCache:                             "cache",
	TkEndcache:                          "endcache",
	TkDeprecated:                        "deprecated",
	TkDo:                                "do",
	TkEmbed:                             "embed",
	TkEndembed:                          "endembed",
	TkExtends:                           "extends",
	TkFlush:                             "flush",
	TkFor:                               "for",
	TkEndfor:                            "endfor",
	TkFrom:                              "from",
	TkImport:                            "import",
	TkMacro:                             "macro",
	TkEndmacro:                          "endmacro",
	TkSandbox:                           "sandbox",
	TkEndsandbox:                        "endsandbox",
	TkSet:                               "set",
	TkEndset:                            "endset",
	TkUse:                               "use",
	TkVerbatim:                          "verbatim",
	TkEndverbatim:                       "endverbatim",
	TkOnly:                              "only",
	TkIgnoreMissing:                     "ignore missing",
	TkWith:                              "with",
	TkEndwith:                           "endwith",
	TkTtl:                               "ttl",
	TkTags:                              "tags",
	TkProps:                             "props",
	TkComponent:                         "component",
	TkEndcomponent:                      "endcomponent",
	TkNot:                               "not",
	TkNotIn:                             "not in",
	TkOr:                                "or",
	TkAnd:                               "and",
	TkBinaryOr:                          "b-or",
	TkBinaryXor:                         "b-xor",
	TkBinaryAnd:                         "b-and",
	TkIn:                                "in",
	TkMatches:                           "matches",
	TkStartsWith:                        "starts with",
	TkEndsWith:                          "ends with",
	TkIs:                                "is",
	TkIsNot:                             "is not",
	TkEven:                              "even",
	TkOdd:                               "odd",
	TkDefined:                           "defined",
	TkSameAs:                            "same as",
	TkAs:                                "as",
	TkNone:                              "none",
	TkNull:                              "null",
	TkDivisibleBy:                       "divisible by",
	TkConstant:                          "constant",
	TkEmpty:                             "empty",
	TkIterable:                          "iterable",
	TkMax:                               "max",
	TkMin:                               "min",
	TkRange:                             "range",
	TkCycle:                             "cycle",
	TkRandom:                            "random",
	TkDate:                              "date",
	TkInclude:                           "include",
	TkSource:                            "source",
	TkTrans:                             "trans",
	TkEndtrans:                          "endtrans",
	TkSwExtends:                         "sw_extends",
	TkSwSilentFeatureCall:               "sw_silent_feature_call",
	TkEndswSilentFeatureCall:            "endsw_silent_feature_call",
	TkSwInclude:                         "sw_include",
	TkReturn:                            "return",
	TkSwIcon:                            "sw_icon",
	TkSwThumbnails:                      "sw_thumbnails",
	TkStyle:                             "style",
	TkSwEmbed:                           "sw_embed",
	TkSwEndEmbed:                        "sw_end_embed",
	TkSwUse:                             "sw_use",
	TkSwImport:                          "sw_import",
	TkSwFrom:                            "sw_from",
	TkLudtwigIgnoreFile:                 "ludtwig-ignore-file",
	TkLudtwigIgnore:                     "ludtwig-ignore",
	TkUnknown:                           "unknown",
	Error:                               "error",
}
