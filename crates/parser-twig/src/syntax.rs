//! Twig/HTML syntax kinds. Kind values, names and texts are persisted in
//! caches and CST dumps; do not change them.
//!
//! The kinds are registered manually rather than with `swlsp_syntax::language!`
//! because the `ERROR` node kind carries the display text "error".

use swlsp_syntax::{Kind, LanguageSpec};

pub const TK_WHITESPACE: Kind = Kind(0);
pub const TK_LINE_BREAK: Kind = Kind(1);
pub const TK_WORD: Kind = Kind(2);
pub const TK_TWIG_COMPONENT_NAME: Kind = Kind(3);
pub const TK_NUMBER: Kind = Kind(4);
pub const TK_HTML_ESCAPE_CHARACTER: Kind = Kind(5);
pub const TK_DOT: Kind = Kind(6);
pub const TK_DOUBLE_DOT: Kind = Kind(7);
pub const TK_TRIPLE_DOT: Kind = Kind(8);
pub const TK_COMMA: Kind = Kind(9);
pub const TK_COLON: Kind = Kind(10);
pub const TK_SEMICOLON: Kind = Kind(11);
pub const TK_EXCLAMATION_MARK: Kind = Kind(12);
pub const TK_EXCLAMATION_MARK_EQUALS: Kind = Kind(13);
pub const TK_EXCLAMATION_MARK_DOUBLE_EQUALS: Kind = Kind(14);
pub const TK_QUESTION_MARK: Kind = Kind(15);
pub const TK_DOUBLE_QUESTION_MARK: Kind = Kind(16);
pub const TK_PERCENT: Kind = Kind(17);
pub const TK_TILDE: Kind = Kind(18);
pub const TK_SINGLE_PIPE: Kind = Kind(19);
pub const TK_DOUBLE_PIPE: Kind = Kind(20);
pub const TK_AMPERSAND: Kind = Kind(21);
pub const TK_DOUBLE_AMPERSAND: Kind = Kind(22);
pub const TK_FORWARD_SLASH: Kind = Kind(23);
pub const TK_DOUBLE_FORWARD_SLASH: Kind = Kind(24);
pub const TK_BACKWARD_SLASH: Kind = Kind(25);
pub const TK_OPEN_PARENTHESIS: Kind = Kind(26);
pub const TK_CLOSE_PARENTHESIS: Kind = Kind(27);
pub const TK_OPEN_CURLY: Kind = Kind(28);
pub const TK_CLOSE_CURLY: Kind = Kind(29);
pub const TK_OPEN_SQUARE: Kind = Kind(30);
pub const TK_CLOSE_SQUARE: Kind = Kind(31);
pub const TK_LESS_THAN: Kind = Kind(32);
pub const TK_LESS_THAN_EQUAL: Kind = Kind(33);
pub const TK_LESS_THAN_EQUAL_GREATER_THAN: Kind = Kind(34);
pub const TK_LESS_THAN_SLASH: Kind = Kind(35);
pub const TK_LESS_THAN_EXCLAMATION_MARK: Kind = Kind(36);
pub const TK_DOCTYPE: Kind = Kind(37);
pub const TK_GREATER_THAN: Kind = Kind(38);
pub const TK_GREATER_THAN_EQUAL: Kind = Kind(39);
pub const TK_EQUAL_GREATER_THAN: Kind = Kind(40);
pub const TK_SLASH_GREATER_THAN: Kind = Kind(41);
pub const TK_LESS_THAN_EXCLAMATION_MARK_MINUS_MINUS: Kind = Kind(42);
pub const TK_MINUS_MINUS_GREATER_THAN: Kind = Kind(43);
pub const TK_EQUAL: Kind = Kind(44);
pub const TK_DOUBLE_EQUAL: Kind = Kind(45);
pub const TK_TRIPLE_EQUAL: Kind = Kind(46);
pub const TK_PLUS: Kind = Kind(47);
pub const TK_MINUS: Kind = Kind(48);
pub const TK_STAR: Kind = Kind(49);
pub const TK_DOUBLE_STAR: Kind = Kind(50);
pub const TK_DOUBLE_QUOTES: Kind = Kind(51);
pub const TK_SINGLE_QUOTES: Kind = Kind(52);
pub const TK_GRAVE_ACCENT_QUOTES: Kind = Kind(53);
pub const TK_CURLY_PERCENT: Kind = Kind(54);
pub const TK_CURLY_PERCENT_MINUS: Kind = Kind(55);
pub const TK_CURLY_PERCENT_TILDE: Kind = Kind(56);
pub const TK_PERCENT_CURLY: Kind = Kind(57);
pub const TK_MINUS_PERCENT_CURLY: Kind = Kind(58);
pub const TK_TILDE_PERCENT_CURLY: Kind = Kind(59);
pub const TK_OPEN_CURLY_CURLY: Kind = Kind(60);
pub const TK_OPEN_CURLY_CURLY_MINUS: Kind = Kind(61);
pub const TK_OPEN_CURLY_CURLY_TILDE: Kind = Kind(62);
pub const TK_CLOSE_CURLY_CURLY: Kind = Kind(63);
pub const TK_MINUS_CLOSE_CURLY_CURLY: Kind = Kind(64);
pub const TK_TILDE_CLOSE_CURLY_CURLY: Kind = Kind(65);
pub const TK_OPEN_CURLY_HASHTAG: Kind = Kind(66);
pub const TK_OPEN_CURLY_HASHTAG_MINUS: Kind = Kind(67);
pub const TK_OPEN_CURLY_HASHTAG_TILDE: Kind = Kind(68);
pub const TK_HASHTAG_CLOSE_CURLY: Kind = Kind(69);
pub const TK_MINUS_HASHTAG_CLOSE_CURLY: Kind = Kind(70);
pub const TK_TILDE_HASHTAG_CLOSE_CURLY: Kind = Kind(71);
pub const TK_HASHTAG: Kind = Kind(72);
pub const TK_TRUE: Kind = Kind(73);
pub const TK_FALSE: Kind = Kind(74);
pub const TK_BLOCK: Kind = Kind(75);
pub const TK_ENDBLOCK: Kind = Kind(76);
pub const TK_IF: Kind = Kind(77);
pub const TK_ELSE_IF: Kind = Kind(78);
pub const TK_ELSE: Kind = Kind(79);
pub const TK_ENDIF: Kind = Kind(80);
pub const TK_APPLY: Kind = Kind(81);
pub const TK_ENDAPPLY: Kind = Kind(82);
pub const TK_AUTOESCAPE: Kind = Kind(83);
pub const TK_ENDAUTOESCAPE: Kind = Kind(84);
pub const TK_CACHE: Kind = Kind(85);
pub const TK_ENDCACHE: Kind = Kind(86);
pub const TK_DEPRECATED: Kind = Kind(87);
pub const TK_DO: Kind = Kind(88);
pub const TK_EMBED: Kind = Kind(89);
pub const TK_ENDEMBED: Kind = Kind(90);
pub const TK_EXTENDS: Kind = Kind(91);
pub const TK_FLUSH: Kind = Kind(92);
pub const TK_FOR: Kind = Kind(93);
pub const TK_ENDFOR: Kind = Kind(94);
pub const TK_FROM: Kind = Kind(95);
pub const TK_IMPORT: Kind = Kind(96);
pub const TK_MACRO: Kind = Kind(97);
pub const TK_ENDMACRO: Kind = Kind(98);
pub const TK_SANDBOX: Kind = Kind(99);
pub const TK_ENDSANDBOX: Kind = Kind(100);
pub const TK_SET: Kind = Kind(101);
pub const TK_ENDSET: Kind = Kind(102);
pub const TK_USE: Kind = Kind(103);
pub const TK_VERBATIM: Kind = Kind(104);
pub const TK_ENDVERBATIM: Kind = Kind(105);
pub const TK_ONLY: Kind = Kind(106);
pub const TK_IGNORE_MISSING: Kind = Kind(107);
pub const TK_WITH: Kind = Kind(108);
pub const TK_ENDWITH: Kind = Kind(109);
pub const TK_TTL: Kind = Kind(110);
pub const TK_TAGS: Kind = Kind(111);
pub const TK_PROPS: Kind = Kind(112);
pub const TK_COMPONENT: Kind = Kind(113);
pub const TK_ENDCOMPONENT: Kind = Kind(114);
pub const TK_NOT: Kind = Kind(115);
pub const TK_NOT_IN: Kind = Kind(116);
pub const TK_OR: Kind = Kind(117);
pub const TK_AND: Kind = Kind(118);
pub const TK_BINARY_OR: Kind = Kind(119);
pub const TK_BINARY_XOR: Kind = Kind(120);
pub const TK_BINARY_AND: Kind = Kind(121);
pub const TK_IN: Kind = Kind(122);
pub const TK_MATCHES: Kind = Kind(123);
pub const TK_STARTS_WITH: Kind = Kind(124);
pub const TK_ENDS_WITH: Kind = Kind(125);
pub const TK_IS: Kind = Kind(126);
pub const TK_IS_NOT: Kind = Kind(127);
pub const TK_EVEN: Kind = Kind(128);
pub const TK_ODD: Kind = Kind(129);
pub const TK_DEFINED: Kind = Kind(130);
pub const TK_SAME_AS: Kind = Kind(131);
pub const TK_AS: Kind = Kind(132);
pub const TK_NONE: Kind = Kind(133);
pub const TK_NULL: Kind = Kind(134);
pub const TK_DIVISIBLE_BY: Kind = Kind(135);
pub const TK_CONSTANT: Kind = Kind(136);
pub const TK_EMPTY: Kind = Kind(137);
pub const TK_ITERABLE: Kind = Kind(138);
pub const TK_MAX: Kind = Kind(139);
pub const TK_MIN: Kind = Kind(140);
pub const TK_RANGE: Kind = Kind(141);
pub const TK_CYCLE: Kind = Kind(142);
pub const TK_RANDOM: Kind = Kind(143);
pub const TK_DATE: Kind = Kind(144);
pub const TK_INCLUDE: Kind = Kind(145);
pub const TK_SOURCE: Kind = Kind(146);
pub const TK_TRANS: Kind = Kind(147);
pub const TK_ENDTRANS: Kind = Kind(148);
pub const TK_SW_EXTENDS: Kind = Kind(149);
pub const TK_SW_SILENT_FEATURE_CALL: Kind = Kind(150);
pub const TK_ENDSW_SILENT_FEATURE_CALL: Kind = Kind(151);
pub const TK_SW_INCLUDE: Kind = Kind(152);
pub const TK_RETURN: Kind = Kind(153);
pub const TK_SW_ICON: Kind = Kind(154);
pub const TK_SW_THUMBNAILS: Kind = Kind(155);
pub const TK_STYLE: Kind = Kind(156);
pub const TK_SW_EMBED: Kind = Kind(157);
pub const TK_SW_END_EMBED: Kind = Kind(158);
pub const TK_SW_USE: Kind = Kind(159);
pub const TK_SW_IMPORT: Kind = Kind(160);
pub const TK_SW_FROM: Kind = Kind(161);
pub const TK_LUDTWIG_IGNORE_FILE: Kind = Kind(162);
pub const TK_LUDTWIG_IGNORE: Kind = Kind(163);
pub const TK_UNKNOWN: Kind = Kind(164);
pub const BODY: Kind = Kind(165);
pub const TWIG_VAR: Kind = Kind(166);
pub const TWIG_EXPRESSION: Kind = Kind(167);
pub const TWIG_BINARY_EXPRESSION: Kind = Kind(168);
pub const TWIG_UNARY_EXPRESSION: Kind = Kind(169);
pub const TWIG_PARENTHESES_EXPRESSION: Kind = Kind(170);
pub const TWIG_CONDITIONAL_EXPRESSION: Kind = Kind(171);
pub const TWIG_OPERAND: Kind = Kind(172);
pub const TWIG_ACCESSOR: Kind = Kind(173);
pub const TWIG_FILTER: Kind = Kind(174);
pub const TWIG_INDEX_LOOKUP: Kind = Kind(175);
pub const TWIG_INDEX: Kind = Kind(176);
pub const TWIG_INDEX_RANGE: Kind = Kind(177);
pub const TWIG_FUNCTION_CALL: Kind = Kind(178);
pub const TWIG_ARROW_FUNCTION: Kind = Kind(179);
pub const TWIG_ARGUMENTS: Kind = Kind(180);
pub const TWIG_NAMED_ARGUMENT: Kind = Kind(181);
pub const TWIG_LITERAL_STRING: Kind = Kind(182);
pub const TWIG_LITERAL_STRING_INNER: Kind = Kind(183);
pub const TWIG_LITERAL_STRING_INTERPOLATION: Kind = Kind(184);
pub const TWIG_LITERAL_NUMBER: Kind = Kind(185);
pub const TWIG_LITERAL_ARRAY: Kind = Kind(186);
pub const TWIG_LITERAL_ARRAY_INNER: Kind = Kind(187);
pub const TWIG_LITERAL_NULL: Kind = Kind(188);
pub const TWIG_LITERAL_BOOLEAN: Kind = Kind(189);
pub const TWIG_LITERAL_HASH: Kind = Kind(190);
pub const TWIG_LITERAL_HASH_ITEMS: Kind = Kind(191);
pub const TWIG_LITERAL_HASH_PAIR: Kind = Kind(192);
pub const TWIG_LITERAL_HASH_KEY: Kind = Kind(193);
pub const TWIG_LITERAL_HASH_VALUE: Kind = Kind(194);
pub const TWIG_LITERAL_NAME: Kind = Kind(195);
pub const TWIG_COMMENT: Kind = Kind(196);
pub const TWIG_BLOCK: Kind = Kind(197);
pub const TWIG_STARTING_BLOCK: Kind = Kind(198);
pub const TWIG_ENDING_BLOCK: Kind = Kind(199);
pub const TWIG_IF: Kind = Kind(200);
pub const TWIG_IF_BLOCK: Kind = Kind(201);
pub const TWIG_ELSE_IF_BLOCK: Kind = Kind(202);
pub const TWIG_ELSE_BLOCK: Kind = Kind(203);
pub const TWIG_ENDIF_BLOCK: Kind = Kind(204);
pub const TWIG_SET: Kind = Kind(205);
pub const TWIG_SET_BLOCK: Kind = Kind(206);
pub const TWIG_ENDSET_BLOCK: Kind = Kind(207);
pub const TWIG_ASSIGNMENT: Kind = Kind(208);
pub const TWIG_FOR: Kind = Kind(209);
pub const TWIG_FOR_BLOCK: Kind = Kind(210);
pub const TWIG_FOR_ELSE_BLOCK: Kind = Kind(211);
pub const TWIG_ENDFOR_BLOCK: Kind = Kind(212);
pub const TWIG_EXTENDS: Kind = Kind(213);
pub const TWIG_INCLUDE: Kind = Kind(214);
pub const TWIG_INCLUDE_WITH: Kind = Kind(215);
pub const TWIG_USE: Kind = Kind(216);
pub const TWIG_OVERRIDE: Kind = Kind(217);
pub const TWIG_APPLY: Kind = Kind(218);
pub const TWIG_APPLY_STARTING_BLOCK: Kind = Kind(219);
pub const TWIG_APPLY_ENDING_BLOCK: Kind = Kind(220);
pub const TWIG_AUTOESCAPE: Kind = Kind(221);
pub const TWIG_AUTOESCAPE_STARTING_BLOCK: Kind = Kind(222);
pub const TWIG_AUTOESCAPE_ENDING_BLOCK: Kind = Kind(223);
pub const TWIG_DEPRECATED: Kind = Kind(224);
pub const TWIG_DO: Kind = Kind(225);
pub const TWIG_EMBED: Kind = Kind(226);
pub const TWIG_EMBED_STARTING_BLOCK: Kind = Kind(227);
pub const TWIG_EMBED_ENDING_BLOCK: Kind = Kind(228);
pub const TWIG_FLUSH: Kind = Kind(229);
pub const TWIG_FROM: Kind = Kind(230);
pub const TWIG_IMPORT: Kind = Kind(231);
pub const TWIG_SANDBOX: Kind = Kind(232);
pub const TWIG_SANDBOX_STARTING_BLOCK: Kind = Kind(233);
pub const TWIG_SANDBOX_ENDING_BLOCK: Kind = Kind(234);
pub const TWIG_VERBATIM: Kind = Kind(235);
pub const TWIG_VERBATIM_STARTING_BLOCK: Kind = Kind(236);
pub const TWIG_VERBATIM_ENDING_BLOCK: Kind = Kind(237);
pub const TWIG_MACRO: Kind = Kind(238);
pub const TWIG_MACRO_STARTING_BLOCK: Kind = Kind(239);
pub const TWIG_MACRO_ENDING_BLOCK: Kind = Kind(240);
pub const TWIG_WITH: Kind = Kind(241);
pub const TWIG_WITH_STARTING_BLOCK: Kind = Kind(242);
pub const TWIG_WITH_ENDING_BLOCK: Kind = Kind(243);
pub const TWIG_CACHE: Kind = Kind(244);
pub const TWIG_CACHE_TTL: Kind = Kind(245);
pub const TWIG_CACHE_TAGS: Kind = Kind(246);
pub const TWIG_CACHE_STARTING_BLOCK: Kind = Kind(247);
pub const TWIG_CACHE_ENDING_BLOCK: Kind = Kind(248);
pub const TWIG_PROPS: Kind = Kind(249);
pub const TWIG_PROP_DECLARATION: Kind = Kind(250);
pub const TWIG_COMPONENT: Kind = Kind(251);
pub const TWIG_COMPONENT_STARTING_BLOCK: Kind = Kind(252);
pub const TWIG_COMPONENT_ENDING_BLOCK: Kind = Kind(253);
pub const TWIG_FORM_THEME: Kind = Kind(254);
pub const TWIG_TRANS: Kind = Kind(255);
pub const TWIG_TRANS_STARTING_BLOCK: Kind = Kind(256);
pub const TWIG_TRANS_ENDING_BLOCK: Kind = Kind(257);
pub const SHOPWARE_TWIG_SW_EXTENDS: Kind = Kind(258);
pub const SHOPWARE_TWIG_SW_INCLUDE: Kind = Kind(259);
pub const SHOPWARE_SILENT_FEATURE_CALL: Kind = Kind(260);
pub const SHOPWARE_SILENT_FEATURE_CALL_STARTING_BLOCK: Kind = Kind(261);
pub const SHOPWARE_SILENT_FEATURE_CALL_ENDING_BLOCK: Kind = Kind(262);
pub const SHOPWARE_RETURN: Kind = Kind(263);
pub const SHOPWARE_ICON: Kind = Kind(264);
pub const SHOPWARE_ICON_STYLE: Kind = Kind(265);
pub const SHOPWARE_THUMBNAILS: Kind = Kind(266);
pub const SHOPWARE_THUMBNAILS_WITH: Kind = Kind(267);
pub const HTML_DOCTYPE: Kind = Kind(268);
pub const HTML_ATTRIBUTE_LIST: Kind = Kind(269);
pub const HTML_ATTRIBUTE: Kind = Kind(270);
pub const HTML_STRING: Kind = Kind(271);
pub const HTML_STRING_INNER: Kind = Kind(272);
pub const HTML_TEXT: Kind = Kind(273);
pub const HTML_RAW_TEXT: Kind = Kind(274);
pub const HTML_COMMENT: Kind = Kind(275);
pub const HTML_TAG: Kind = Kind(276);
pub const HTML_STARTING_TAG: Kind = Kind(277);
pub const HTML_ENDING_TAG: Kind = Kind(278);
pub const LUDTWIG_DIRECTIVE_FILE_IGNORE: Kind = Kind(279);
pub const LUDTWIG_DIRECTIVE_IGNORE: Kind = Kind(280);
pub const LUDTWIG_DIRECTIVE_RULE_LIST: Kind = Kind(281);
pub const ERROR: Kind = Kind(282);
pub const ROOT: Kind = Kind(283);
pub const TWIG_ASSETIC: Kind = Kind(284);
pub const TWIG_ASSETIC_STARTING_BLOCK: Kind = Kind(285);
pub const TWIG_ASSETIC_ENDING_BLOCK: Kind = Kind(286);
pub const TWIG_TYPES: Kind = Kind(287);

pub const KIND_BASE: u16 = 0;
pub const KIND_COUNT: u16 = 288;
/// First composite node kind; every kind below it is a token.
pub const FIRST_NODE: Kind = BODY;

static NAMES: &[&str] = &[
    "TK_WHITESPACE",
    "TK_LINE_BREAK",
    "TK_WORD",
    "TK_TWIG_COMPONENT_NAME",
    "TK_NUMBER",
    "TK_HTML_ESCAPE_CHARACTER",
    "TK_DOT",
    "TK_DOUBLE_DOT",
    "TK_TRIPLE_DOT",
    "TK_COMMA",
    "TK_COLON",
    "TK_SEMICOLON",
    "TK_EXCLAMATION_MARK",
    "TK_EXCLAMATION_MARK_EQUALS",
    "TK_EXCLAMATION_MARK_DOUBLE_EQUALS",
    "TK_QUESTION_MARK",
    "TK_DOUBLE_QUESTION_MARK",
    "TK_PERCENT",
    "TK_TILDE",
    "TK_SINGLE_PIPE",
    "TK_DOUBLE_PIPE",
    "TK_AMPERSAND",
    "TK_DOUBLE_AMPERSAND",
    "TK_FORWARD_SLASH",
    "TK_DOUBLE_FORWARD_SLASH",
    "TK_BACKWARD_SLASH",
    "TK_OPEN_PARENTHESIS",
    "TK_CLOSE_PARENTHESIS",
    "TK_OPEN_CURLY",
    "TK_CLOSE_CURLY",
    "TK_OPEN_SQUARE",
    "TK_CLOSE_SQUARE",
    "TK_LESS_THAN",
    "TK_LESS_THAN_EQUAL",
    "TK_LESS_THAN_EQUAL_GREATER_THAN",
    "TK_LESS_THAN_SLASH",
    "TK_LESS_THAN_EXCLAMATION_MARK",
    "TK_DOCTYPE",
    "TK_GREATER_THAN",
    "TK_GREATER_THAN_EQUAL",
    "TK_EQUAL_GREATER_THAN",
    "TK_SLASH_GREATER_THAN",
    "TK_LESS_THAN_EXCLAMATION_MARK_MINUS_MINUS",
    "TK_MINUS_MINUS_GREATER_THAN",
    "TK_EQUAL",
    "TK_DOUBLE_EQUAL",
    "TK_TRIPLE_EQUAL",
    "TK_PLUS",
    "TK_MINUS",
    "TK_STAR",
    "TK_DOUBLE_STAR",
    "TK_DOUBLE_QUOTES",
    "TK_SINGLE_QUOTES",
    "TK_GRAVE_ACCENT_QUOTES",
    "TK_CURLY_PERCENT",
    "TK_CURLY_PERCENT_MINUS",
    "TK_CURLY_PERCENT_TILDE",
    "TK_PERCENT_CURLY",
    "TK_MINUS_PERCENT_CURLY",
    "TK_TILDE_PERCENT_CURLY",
    "TK_OPEN_CURLY_CURLY",
    "TK_OPEN_CURLY_CURLY_MINUS",
    "TK_OPEN_CURLY_CURLY_TILDE",
    "TK_CLOSE_CURLY_CURLY",
    "TK_MINUS_CLOSE_CURLY_CURLY",
    "TK_TILDE_CLOSE_CURLY_CURLY",
    "TK_OPEN_CURLY_HASHTAG",
    "TK_OPEN_CURLY_HASHTAG_MINUS",
    "TK_OPEN_CURLY_HASHTAG_TILDE",
    "TK_HASHTAG_CLOSE_CURLY",
    "TK_MINUS_HASHTAG_CLOSE_CURLY",
    "TK_TILDE_HASHTAG_CLOSE_CURLY",
    "TK_HASHTAG",
    "TK_TRUE",
    "TK_FALSE",
    "TK_BLOCK",
    "TK_ENDBLOCK",
    "TK_IF",
    "TK_ELSE_IF",
    "TK_ELSE",
    "TK_ENDIF",
    "TK_APPLY",
    "TK_ENDAPPLY",
    "TK_AUTOESCAPE",
    "TK_ENDAUTOESCAPE",
    "TK_CACHE",
    "TK_ENDCACHE",
    "TK_DEPRECATED",
    "TK_DO",
    "TK_EMBED",
    "TK_ENDEMBED",
    "TK_EXTENDS",
    "TK_FLUSH",
    "TK_FOR",
    "TK_ENDFOR",
    "TK_FROM",
    "TK_IMPORT",
    "TK_MACRO",
    "TK_ENDMACRO",
    "TK_SANDBOX",
    "TK_ENDSANDBOX",
    "TK_SET",
    "TK_ENDSET",
    "TK_USE",
    "TK_VERBATIM",
    "TK_ENDVERBATIM",
    "TK_ONLY",
    "TK_IGNORE_MISSING",
    "TK_WITH",
    "TK_ENDWITH",
    "TK_TTL",
    "TK_TAGS",
    "TK_PROPS",
    "TK_COMPONENT",
    "TK_ENDCOMPONENT",
    "TK_NOT",
    "TK_NOT_IN",
    "TK_OR",
    "TK_AND",
    "TK_BINARY_OR",
    "TK_BINARY_XOR",
    "TK_BINARY_AND",
    "TK_IN",
    "TK_MATCHES",
    "TK_STARTS_WITH",
    "TK_ENDS_WITH",
    "TK_IS",
    "TK_IS_NOT",
    "TK_EVEN",
    "TK_ODD",
    "TK_DEFINED",
    "TK_SAME_AS",
    "TK_AS",
    "TK_NONE",
    "TK_NULL",
    "TK_DIVISIBLE_BY",
    "TK_CONSTANT",
    "TK_EMPTY",
    "TK_ITERABLE",
    "TK_MAX",
    "TK_MIN",
    "TK_RANGE",
    "TK_CYCLE",
    "TK_RANDOM",
    "TK_DATE",
    "TK_INCLUDE",
    "TK_SOURCE",
    "TK_TRANS",
    "TK_ENDTRANS",
    "TK_SW_EXTENDS",
    "TK_SW_SILENT_FEATURE_CALL",
    "TK_ENDSW_SILENT_FEATURE_CALL",
    "TK_SW_INCLUDE",
    "TK_RETURN",
    "TK_SW_ICON",
    "TK_SW_THUMBNAILS",
    "TK_STYLE",
    "TK_SW_EMBED",
    "TK_SW_END_EMBED",
    "TK_SW_USE",
    "TK_SW_IMPORT",
    "TK_SW_FROM",
    "TK_LUDTWIG_IGNORE_FILE",
    "TK_LUDTWIG_IGNORE",
    "TK_UNKNOWN",
    "BODY",
    "TWIG_VAR",
    "TWIG_EXPRESSION",
    "TWIG_BINARY_EXPRESSION",
    "TWIG_UNARY_EXPRESSION",
    "TWIG_PARENTHESES_EXPRESSION",
    "TWIG_CONDITIONAL_EXPRESSION",
    "TWIG_OPERAND",
    "TWIG_ACCESSOR",
    "TWIG_FILTER",
    "TWIG_INDEX_LOOKUP",
    "TWIG_INDEX",
    "TWIG_INDEX_RANGE",
    "TWIG_FUNCTION_CALL",
    "TWIG_ARROW_FUNCTION",
    "TWIG_ARGUMENTS",
    "TWIG_NAMED_ARGUMENT",
    "TWIG_LITERAL_STRING",
    "TWIG_LITERAL_STRING_INNER",
    "TWIG_LITERAL_STRING_INTERPOLATION",
    "TWIG_LITERAL_NUMBER",
    "TWIG_LITERAL_ARRAY",
    "TWIG_LITERAL_ARRAY_INNER",
    "TWIG_LITERAL_NULL",
    "TWIG_LITERAL_BOOLEAN",
    "TWIG_LITERAL_HASH",
    "TWIG_LITERAL_HASH_ITEMS",
    "TWIG_LITERAL_HASH_PAIR",
    "TWIG_LITERAL_HASH_KEY",
    "TWIG_LITERAL_HASH_VALUE",
    "TWIG_LITERAL_NAME",
    "TWIG_COMMENT",
    "TWIG_BLOCK",
    "TWIG_STARTING_BLOCK",
    "TWIG_ENDING_BLOCK",
    "TWIG_IF",
    "TWIG_IF_BLOCK",
    "TWIG_ELSE_IF_BLOCK",
    "TWIG_ELSE_BLOCK",
    "TWIG_ENDIF_BLOCK",
    "TWIG_SET",
    "TWIG_SET_BLOCK",
    "TWIG_ENDSET_BLOCK",
    "TWIG_ASSIGNMENT",
    "TWIG_FOR",
    "TWIG_FOR_BLOCK",
    "TWIG_FOR_ELSE_BLOCK",
    "TWIG_ENDFOR_BLOCK",
    "TWIG_EXTENDS",
    "TWIG_INCLUDE",
    "TWIG_INCLUDE_WITH",
    "TWIG_USE",
    "TWIG_OVERRIDE",
    "TWIG_APPLY",
    "TWIG_APPLY_STARTING_BLOCK",
    "TWIG_APPLY_ENDING_BLOCK",
    "TWIG_AUTOESCAPE",
    "TWIG_AUTOESCAPE_STARTING_BLOCK",
    "TWIG_AUTOESCAPE_ENDING_BLOCK",
    "TWIG_DEPRECATED",
    "TWIG_DO",
    "TWIG_EMBED",
    "TWIG_EMBED_STARTING_BLOCK",
    "TWIG_EMBED_ENDING_BLOCK",
    "TWIG_FLUSH",
    "TWIG_FROM",
    "TWIG_IMPORT",
    "TWIG_SANDBOX",
    "TWIG_SANDBOX_STARTING_BLOCK",
    "TWIG_SANDBOX_ENDING_BLOCK",
    "TWIG_VERBATIM",
    "TWIG_VERBATIM_STARTING_BLOCK",
    "TWIG_VERBATIM_ENDING_BLOCK",
    "TWIG_MACRO",
    "TWIG_MACRO_STARTING_BLOCK",
    "TWIG_MACRO_ENDING_BLOCK",
    "TWIG_WITH",
    "TWIG_WITH_STARTING_BLOCK",
    "TWIG_WITH_ENDING_BLOCK",
    "TWIG_CACHE",
    "TWIG_CACHE_TTL",
    "TWIG_CACHE_TAGS",
    "TWIG_CACHE_STARTING_BLOCK",
    "TWIG_CACHE_ENDING_BLOCK",
    "TWIG_PROPS",
    "TWIG_PROP_DECLARATION",
    "TWIG_COMPONENT",
    "TWIG_COMPONENT_STARTING_BLOCK",
    "TWIG_COMPONENT_ENDING_BLOCK",
    "TWIG_FORM_THEME",
    "TWIG_TRANS",
    "TWIG_TRANS_STARTING_BLOCK",
    "TWIG_TRANS_ENDING_BLOCK",
    "SHOPWARE_TWIG_SW_EXTENDS",
    "SHOPWARE_TWIG_SW_INCLUDE",
    "SHOPWARE_SILENT_FEATURE_CALL",
    "SHOPWARE_SILENT_FEATURE_CALL_STARTING_BLOCK",
    "SHOPWARE_SILENT_FEATURE_CALL_ENDING_BLOCK",
    "SHOPWARE_RETURN",
    "SHOPWARE_ICON",
    "SHOPWARE_ICON_STYLE",
    "SHOPWARE_THUMBNAILS",
    "SHOPWARE_THUMBNAILS_WITH",
    "HTML_DOCTYPE",
    "HTML_ATTRIBUTE_LIST",
    "HTML_ATTRIBUTE",
    "HTML_STRING",
    "HTML_STRING_INNER",
    "HTML_TEXT",
    "HTML_RAW_TEXT",
    "HTML_COMMENT",
    "HTML_TAG",
    "HTML_STARTING_TAG",
    "HTML_ENDING_TAG",
    "LUDTWIG_DIRECTIVE_FILE_IGNORE",
    "LUDTWIG_DIRECTIVE_IGNORE",
    "LUDTWIG_DIRECTIVE_RULE_LIST",
    "ERROR",
    "ROOT",
    "TWIG_ASSETIC",
    "TWIG_ASSETIC_STARTING_BLOCK",
    "TWIG_ASSETIC_ENDING_BLOCK",
    "TWIG_TYPES",
];

static TEXTS: &[&str] = &[
    "whitespace",
    "line break",
    "word",
    "twig component name",
    "number",
    "html escape character",
    ".",
    "..",
    "...",
    ",",
    ":",
    ";",
    "!",
    "!=",
    "!==",
    "?",
    "??",
    "%",
    "~",
    "|",
    "||",
    "&",
    "&&",
    "/",
    "//",
    "\\",
    "(",
    ")",
    "{",
    "}",
    "[",
    "]",
    "<",
    "<=",
    "<=>",
    "</",
    "<!",
    "DOCTYPE",
    ">",
    ">=",
    "=>",
    "/>",
    "<!--",
    "-->",
    "=",
    "==",
    "===",
    "+",
    "-",
    "*",
    "**",
    "\"",
    "'",
    "`",
    "{%",
    "{%-",
    "{%~",
    "%}",
    "-%}",
    "~%}",
    "{{",
    "{{-",
    "{{~",
    "}}",
    "-}}",
    "~}}",
    "{#",
    "{#-",
    "{#~",
    "#}",
    "-#}",
    "~#}",
    "#",
    "true",
    "false",
    "block",
    "endblock",
    "if",
    "elseif",
    "else",
    "endif",
    "apply",
    "endapply",
    "autoescape",
    "endautoescape",
    "cache",
    "endcache",
    "deprecated",
    "do",
    "embed",
    "endembed",
    "extends",
    "flush",
    "for",
    "endfor",
    "from",
    "import",
    "macro",
    "endmacro",
    "sandbox",
    "endsandbox",
    "set",
    "endset",
    "use",
    "verbatim",
    "endverbatim",
    "only",
    "ignore missing",
    "with",
    "endwith",
    "ttl",
    "tags",
    "props",
    "component",
    "endcomponent",
    "not",
    "not in",
    "or",
    "and",
    "b-or",
    "b-xor",
    "b-and",
    "in",
    "matches",
    "starts with",
    "ends with",
    "is",
    "is not",
    "even",
    "odd",
    "defined",
    "same as",
    "as",
    "none",
    "null",
    "divisible by",
    "constant",
    "empty",
    "iterable",
    "max",
    "min",
    "range",
    "cycle",
    "random",
    "date",
    "include",
    "source",
    "trans",
    "endtrans",
    "sw_extends",
    "sw_silent_feature_call",
    "endsw_silent_feature_call",
    "sw_include",
    "return",
    "sw_icon",
    "sw_thumbnails",
    "style",
    "sw_embed",
    "sw_end_embed",
    "sw_use",
    "sw_import",
    "sw_from",
    "ludtwig-ignore-file",
    "ludtwig-ignore",
    "unknown",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "error",
    "",
    "",
    "",
    "",
    "",
];

static TRIVIA: &[u16] = &[TK_WHITESPACE.0, TK_LINE_BREAK.0];

swlsp_syntax::inventory::submit! {
    LanguageSpec {
        name: "twig",
        base: KIND_BASE,
        names: NAMES,
        texts: TEXTS,
        first_node: FIRST_NODE.0,
        trivia: TRIVIA,
    }
}
