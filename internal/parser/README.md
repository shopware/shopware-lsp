# Pure-Go parser foundation

This directory is the in-repository parser foundation derived from the
`twig-go` prototype.

The packages are intentionally split by responsibility:

- `cst` owns the language-neutral lossless tree, source ranges, position
  mapping, and the global language kind registry.
- `parsekit` owns the language-neutral token cursor, event/marker parser,
  recovery, diagnostics, and CST sink.
- `twig/lexer`, `twig/parser`, `twig/syntax`, `twig/ast`, and `twig/query`
  implement Twig and HTML on top of that foundation.
- `json/lexer`, `json/parser`, `json/syntax`, and `json/query` implement JSON
  as the second backend on the same foundation.
- `yaml/lexer`, `yaml/parser`, `yaml/syntax`, and `yaml/query` implement YAML,
  including block and flow collections, on the same foundation.
- `scss/lexer`, `scss/parser`, `scss/syntax`, and `scss/query` implement
  stylesheet structure and Sass expressions on the same foundation.
- `xml/lexer`, `xml/parser`, `xml/syntax`, and `xml/query` implement XML
  documents, elements, attributes, and tolerant editor-time recovery.

Adding a language does not require changing `cst` or `parsekit`. A language
reserves a non-overlapping `cst.Kind` range with `cst.RegisterLanguage`, emits
`[]parsekit.Token` from its lexer, and implements its grammar using
`parsekit.Parser`.

The important parser guarantees are:

- every input produces a usable tree;
- the tree is lossless (`result.Tree.Root.Text() == source`);
- ranges are byte offsets into the original source;
- trivia and malformed input remain represented in the tree.

PHP, Twig/HTML, JSON, YAML, SCSS, XML, JavaScript/TypeScript, Vue, and XPath all
run on this foundation. Further language backends can be added incrementally
without coupling their consumers to language-specific kinds.

For the detailed walkthrough — the kind registry, the event/marker parser, the
sink, the CST API, positions, embedded languages, the query packages, and how to
add a language — see
[`docs/parser-architecture.md`](../../docs/parser-architecture.md).
