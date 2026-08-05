package parser

import (
	"fmt"

	"github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// terminationKeywords lists all twig ending / delimiter keywords that signal a
// termination tag. Ported from at_twig_termination_tag (tags.rs).
var terminationKeywords = []syntax.Kind{
	syntax.TkEndblock,
	syntax.TkEndif,
	syntax.TkElseIf,
	syntax.TkElse,
	syntax.TkEndset,
	syntax.TkEndfor,
	syntax.TkEndembed,
	syntax.TkSwEndEmbed,
	syntax.TkEndapply,
	syntax.TkEndautoescape,
	syntax.TkEndsandbox,
	syntax.TkEndverbatim,
	syntax.TkEndmacro,
	syntax.TkEndwith,
	syntax.TkEndcache,
	syntax.TkEndswSilentFeatureCall,
	syntax.TkEndtrans,
}

// atTwigTerminationTag checks if the parser is at a twig ending / delimiter tag
// like `endblock` or `elseif` which should be caught by html body parsers to
// stop parsing early (this is helpful to spot for example missing closing html
// tags).
//
// Important: every ending twig tag or delimiter tag must be added to
// terminationKeywords for now!
func atTwigTerminationTag(p *parser) bool {
	if !p.atTwigBlockOpen() {
		return false
	}
	if p.atTwigWordTag("endstylesheets") ||
		p.atTwigWordTag("endjavascripts") {
		return true
	}
	k, ok := p.peekNextNonTriviaKind()
	if !ok {
		return false
	}
	for _, kw := range terminationKeywords {
		if kw == k {
			return true
		}
	}
	return false
}

func parseTwigBlockStatement(p *parser, childParser parseFunction) (completedMarker, bool) {
	// debug_assert!(parser.at_twig_block_open());
	m := p.start()
	p.bump()

	switch {
	case p.at(syntax.TkBlock):
		return parseTwigBlock(p, m, childParser), true
	case p.at(syntax.TkIf):
		return parseTwigIf(p, m, childParser), true
	case p.at(syntax.TkSet):
		return parseTwigSet(p, m, childParser), true
	case p.at(syntax.TkFor):
		return parseTwigFor(p, m, childParser), true
	case p.at(syntax.TkExtends):
		return parseTwigExtends(p, m), true
	case p.at(syntax.TkInclude):
		return parseTwigInclude(p, m), true
	case p.atSet([]syntax.Kind{syntax.TkEmbed, syntax.TkSwEmbed}):
		return parseTwigEmbed(p, m, childParser), true
	case p.atSet([]syntax.Kind{syntax.TkUse, syntax.TkSwUse}):
		return parseTwigUse(p, m), true
	case p.atSet([]syntax.Kind{syntax.TkFrom, syntax.TkSwFrom}):
		return parseTwigFrom(p, m), true
	case p.atSet([]syntax.Kind{syntax.TkImport, syntax.TkSwImport}):
		return parseTwigImport(p, m), true
	case p.at(syntax.TkApply):
		return parseTwigApply(p, m, childParser), true
	case p.at(syntax.TkAutoescape):
		return parseTwigAutoescape(p, m, childParser), true
	case p.at(syntax.TkDeprecated):
		return parseTwigDeprecated(p, m), true
	case p.at(syntax.TkDo):
		return parseTwigDo(p, m), true
	case p.at(syntax.TkFlush):
		return parseTwigFlush(p, m), true
	case p.at(syntax.TkSandbox):
		return parseTwigSandbox(p, m, childParser), true
	case p.at(syntax.TkVerbatim):
		return parseTwigVerbatim(p, m, childParser), true
	case p.at(syntax.TkMacro):
		return parseTwigMacro(p, m, childParser), true
	case p.at(syntax.TkWith):
		return parseTwigWith(p, m, childParser), true
	case p.at(syntax.TkCache):
		return parseTwigCache(p, m, childParser), true
	case p.at(syntax.TkTrans):
		return parseTwigTrans(p, m, childParser), true
	case p.at(syntax.TkComponent):
		return parseTwigComponent(p, m, childParser), true
	case p.at(syntax.TkProps):
		return parseTwigProps(p, m), true
	case p.at(syntax.TkWord) &&
		p.peekToken() != nil &&
		p.peekToken().Text() == "form_theme":
		return parseTwigFormTheme(p, m), true
	case p.at(syntax.TkWord) &&
		p.peekToken() != nil &&
		(p.peekToken().Text() == "stylesheets" ||
			p.peekToken().Text() == "javascripts"):
		return parseTwigAssetic(p, m, childParser), true
	default:
		res := parseShopwareTwigBlockStatement(p, m, childParser)
		if res.ok {
			return res.cm, true
		}
		// BlockParseResult::NothingFound
		p.addError(newErrorBuilder("twig tag"))
		p.complete(m, syntax.Error)
		return completedMarker{}, false
	}
}

// parseTwigAssetic parses the legacy Assetic block tags:
//
//	{% stylesheets 'css/app.css' filter='cssrewrite' %}
//	    <link href="{{ asset_url }}">
//	{% endstylesheets %}
//
// Assetic accepts adjacent expressions without commas, so each expression is
// parsed independently until the opening tag closes.
func parseTwigAssetic(
	p *parser,
	outer *marker,
	childParser parseFunction,
) completedMarker {
	tag := p.peekToken().Text()
	endTag := "end" + tag
	p.bump()
	parseMany(
		p,
		func(p *parser) bool { return p.atTwigBlockClose() },
		func(p *parser) {
			if next, ok := p.peekNextNonTriviaKind(); ok &&
				p.at(syntax.TkWord) &&
				next == syntax.TkEqual {
				option := p.start()
				p.bump()
				p.bump()
				if _, parsed := parseTwigExpression(p); !parsed {
					p.addError(newErrorBuilder("Assetic option value"))
					p.recover(twigBlockCloseSet)
				}
				p.complete(option, syntax.TwigNamedArgument)
				return
			}
			if _, ok := parseTwigExpression(p); !ok {
				p.addError(newErrorBuilder("Assetic asset or option"))
				p.recover(twigBlockCloseSet)
			}
		},
	)
	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})

	starting := p.complete(outer, syntax.TwigAsseticStartingBlock)
	wrapper := p.precede(starting)

	body := p.start()
	parseMany(
		p,
		func(p *parser) bool { return p.atTwigWordTag(endTag) },
		func(p *parser) {
			childParser(p)
		},
	)
	p.complete(body, syntax.Body)

	ending := p.start()
	p.expectAny(twigBlockOpenSet, []syntax.Kind{
		syntax.TkWord,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
	})
	if p.at(syntax.TkWord) &&
		p.peekToken() != nil &&
		p.peekToken().Text() == endTag {
		p.bump()
	} else {
		p.addError(newErrorBuilder(endTag))
	}
	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})
	p.complete(ending, syntax.TwigAsseticEndingBlock)

	return p.complete(wrapper, syntax.TwigAssetic)
}

// parseTwigFormTheme parses Symfony's extension tag:
//
//	{% form_theme form 'forms.html.twig' %}
//	{% form_theme form with ['base.html.twig', 'custom.html.twig'] %}
//
// form_theme is intentionally kept as TK_WORD because it is supplied by the
// Symfony bridge rather than Twig core. The dedicated node still gives
// semantic consumers a lossless, stable boundary.
func parseTwigFormTheme(p *parser, outer *marker) completedMarker {
	p.bump()
	if _, ok := parseTwigExpression(p); !ok {
		p.addError(newErrorBuilder("form expression"))
	}

	if p.at(syntax.TkWith) {
		p.bump()
		if _, ok := parseTwigExpression(p); !ok {
			p.addError(newErrorBuilder("form theme expression"))
		}
	} else {
		parseMany(
			p,
			func(p *parser) bool { return p.atTwigBlockClose() },
			func(p *parser) {
				if _, ok := parseTwigExpression(p); !ok {
					p.addError(newErrorBuilder("form theme expression"))
					p.recover(twigBlockCloseSet)
				}
			},
		)
	}

	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})
	return p.complete(outer, syntax.TwigFormTheme)
}

func parseTwigCache(p *parser, outer *marker, childParser parseFunction) completedMarker {
	// debug_assert!(parser.at(T!["cache"]));
	p.bump()
	if _, ok := parseTwigExpression(p); !ok {
		p.addError(newErrorBuilder("twig expression as cache key"))
		p.recover([]syntax.Kind{
			syntax.TkTtl,
			syntax.TkTags,
			syntax.TkEndcache,
			syntax.TkOpenParenthesis,
			syntax.TkCloseParenthesis,
			syntax.TkPercentCurly,
			syntax.TkMinusPercentCurly,
			syntax.TkTildePercentCurly,
			syntax.TkLessThanSlash,
		})
	}

	if p.at(syntax.TkTtl) {
		ttlM := p.start()
		p.bump()
		p.expect(syntax.TkOpenParenthesis, []syntax.Kind{
			syntax.TkCloseParenthesis,
			syntax.TkTags,
			syntax.TkEndcache,
			syntax.TkPercentCurly,
			syntax.TkMinusPercentCurly,
			syntax.TkTildePercentCurly,
			syntax.TkLessThanSlash,
		})
		if _, ok := parseTwigExpression(p); !ok {
			p.addError(newErrorBuilder("twig expression as cache time to live"))
			p.recover([]syntax.Kind{
				syntax.TkCloseParenthesis,
				syntax.TkTags,
				syntax.TkEndcache,
				syntax.TkPercentCurly,
				syntax.TkMinusPercentCurly,
				syntax.TkTildePercentCurly,
				syntax.TkLessThanSlash,
			})
		}
		p.expect(syntax.TkCloseParenthesis, []syntax.Kind{
			syntax.TkTags,
			syntax.TkEndcache,
			syntax.TkPercentCurly,
			syntax.TkMinusPercentCurly,
			syntax.TkTildePercentCurly,
			syntax.TkLessThanSlash,
		})
		p.complete(ttlM, syntax.TwigCacheTtl)
	}
	if p.at(syntax.TkTags) {
		tagsM := p.start()
		p.bump()
		p.expect(syntax.TkOpenParenthesis, []syntax.Kind{
			syntax.TkCloseParenthesis,
			syntax.TkEndcache,
			syntax.TkPercentCurly,
			syntax.TkMinusPercentCurly,
			syntax.TkTildePercentCurly,
			syntax.TkLessThanSlash,
		})
		if _, ok := parseTwigExpression(p); !ok {
			p.addError(newErrorBuilder("twig expression as cache tags"))
			p.recover([]syntax.Kind{
				syntax.TkCloseParenthesis,
				syntax.TkEndcache,
				syntax.TkPercentCurly,
				syntax.TkMinusPercentCurly,
				syntax.TkTildePercentCurly,
				syntax.TkLessThanSlash,
			})
		}
		p.expect(syntax.TkCloseParenthesis, []syntax.Kind{
			syntax.TkEndcache,
			syntax.TkPercentCurly,
			syntax.TkMinusPercentCurly,
			syntax.TkTildePercentCurly,
			syntax.TkLessThanSlash,
		})
		p.complete(tagsM, syntax.TwigCacheTags)
	}
	p.expectAny(twigBlockCloseSet, []syntax.Kind{
		syntax.TkEndcache,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})

	wrapperCm := p.complete(outer, syntax.TwigCacheStartingBlock)
	wrapperM := p.precede(wrapperCm)

	// parse all the children except endcache
	bodyM := p.start()
	parseMany(p, func(p *parser) bool { return p.atTwigTag(syntax.TkEndcache) }, func(p *parser) {
		childParser(p)
	})
	p.complete(bodyM, syntax.Body)

	endBlockM := p.start()
	p.expectAny(twigBlockOpenSet, []syntax.Kind{
		syntax.TkEndcache,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	p.expect(syntax.TkEndcache, []syntax.Kind{
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})
	p.complete(endBlockM, syntax.TwigCacheEndingBlock)

	return p.complete(wrapperM, syntax.TwigCache)
}

func parseTwigWith(p *parser, outer *marker, childParser parseFunction) completedMarker {
	// debug_assert!(parser.at(T!["with"]));
	p.bump()
	// optional expression which should resolve to a hash with variable names as keys
	parseTwigExpression(p)
	if p.at(syntax.TkOnly) {
		p.bump()
	}
	p.expectAny(twigBlockCloseSet, []syntax.Kind{
		syntax.TkEndwith,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})

	wrapperCm := p.complete(outer, syntax.TwigWithStartingBlock)
	wrapperM := p.precede(wrapperCm)

	// parse all the children except endwith
	bodyM := p.start()
	parseMany(p, func(p *parser) bool { return p.atTwigTag(syntax.TkEndwith) }, func(p *parser) {
		childParser(p)
	})
	p.complete(bodyM, syntax.Body)

	endBlockM := p.start()
	p.expectAny(twigBlockOpenSet, []syntax.Kind{
		syntax.TkEndwith,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	p.expect(syntax.TkEndwith, []syntax.Kind{
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})
	p.complete(endBlockM, syntax.TwigWithEndingBlock)

	return p.complete(wrapperM, syntax.TwigWith)
}

func parseTwigMacro(p *parser, outer *marker, childParser parseFunction) completedMarker {
	// debug_assert!(parser.at(T!["macro"]));
	p.bump()
	macroNameTok := p.expect(syntax.TkWord, []syntax.Kind{
		syntax.TkOpenParenthesis,
		syntax.TkCloseParenthesis,
		syntax.TkEndmacro,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	var macroName string
	hasMacroName := false
	if macroNameTok != nil {
		macroName = macroNameTok.Text()
		hasMacroName = true
	}

	// macro must have parentheses (arguments can be zero)
	argumentsM := p.start()
	p.expect(syntax.TkOpenParenthesis, []syntax.Kind{
		syntax.TkCloseParenthesis,
		syntax.TkEndmacro,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	parseMany(p, func(p *parser) bool {
		return p.atSet([]syntax.Kind{
			syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkCloseParenthesis,
		})
	}, func(p *parser) {
		parseTwigFunctionArgument(p)
		if p.at(syntax.TkComma) {
			p.bump()
		} else if !p.atSet([]syntax.Kind{
			syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkCloseParenthesis,
		}) {
			p.addError(newErrorBuilder(","))
		}
	})
	p.expect(syntax.TkCloseParenthesis, []syntax.Kind{
		syntax.TkEndmacro,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	p.complete(argumentsM, syntax.TwigArguments)
	p.expectAny(twigBlockCloseSet, []syntax.Kind{
		syntax.TkEndmacro,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})

	wrapperCm := p.complete(outer, syntax.TwigMacroStartingBlock)
	wrapperM := p.precede(wrapperCm)

	// parse all the children except endblock
	bodyM := p.start()
	parseMany(p, func(p *parser) bool { return p.atTwigTag(syntax.TkEndmacro) }, func(p *parser) {
		childParser(p)
	})
	p.complete(bodyM, syntax.Body)

	endBlockM := p.start()
	p.expectAny(twigBlockOpenSet, []syntax.Kind{
		syntax.TkEndmacro,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	p.expect(syntax.TkEndmacro, []syntax.Kind{
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	// check for optional name behind endmacro
	if p.at(syntax.TkWord) {
		endMacroNameToken := p.bump()
		if hasMacroName {
			if endMacroNameToken.Text() != macroName {
				parserErr := newErrorBuilder(fmt.Sprintf(
					"nothing or same twig macro name as opening (%s)", macroName,
				)).atToken(endMacroNameToken)
				p.addError(parserErr)
				p.recover([]syntax.Kind{
					syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkLessThanSlash,
				})
			}
		}
	}
	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})
	p.complete(endBlockM, syntax.TwigMacroEndingBlock)

	return p.complete(wrapperM, syntax.TwigMacro)
}

func parseTwigVerbatim(p *parser, outer *marker, childParser parseFunction) completedMarker {
	// debug_assert!(parser.at(T!["verbatim"]));
	p.bump()
	p.expectAny(twigBlockCloseSet, []syntax.Kind{
		syntax.TkEndverbatim,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})

	wrapperCm := p.complete(outer, syntax.TwigVerbatimStartingBlock)
	wrapperM := p.precede(wrapperCm)

	// parse all the children except endverbatim
	bodyM := p.start()
	parseMany(p, func(p *parser) bool { return p.atTwigTag(syntax.TkEndverbatim) }, func(p *parser) {
		childParser(p)
	})
	p.complete(bodyM, syntax.Body)

	endBlockM := p.start()
	p.expectAny(twigBlockOpenSet, []syntax.Kind{
		syntax.TkEndverbatim,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	p.expect(syntax.TkEndverbatim, []syntax.Kind{
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})
	p.complete(endBlockM, syntax.TwigVerbatimEndingBlock)

	return p.complete(wrapperM, syntax.TwigVerbatim)
}

func parseTwigSandbox(p *parser, outer *marker, childParser parseFunction) completedMarker {
	// debug_assert!(parser.at(T!["sandbox"]));
	p.bump()
	p.expectAny(twigBlockCloseSet, []syntax.Kind{
		syntax.TkEndsandbox,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})

	wrapperCm := p.complete(outer, syntax.TwigSandboxStartingBlock)
	wrapperM := p.precede(wrapperCm)

	// parse all the children except endsandbox
	bodyM := p.start()
	parseMany(p, func(p *parser) bool { return p.atTwigTag(syntax.TkEndsandbox) }, func(p *parser) {
		childParser(p)
	})
	p.complete(bodyM, syntax.Body)

	endBlockM := p.start()
	p.expectAny(twigBlockOpenSet, []syntax.Kind{
		syntax.TkEndsandbox,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	p.expect(syntax.TkEndsandbox, []syntax.Kind{
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})
	p.complete(endBlockM, syntax.TwigSandboxEndingBlock)

	return p.complete(wrapperM, syntax.TwigSandbox)
}

func parseTwigFlush(p *parser, outer *marker) completedMarker {
	// debug_assert!(parser.at(T!["flush"]));
	p.bump()
	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})
	return p.complete(outer, syntax.TwigFlush)
}

func parseTwigDo(p *parser, outer *marker) completedMarker {
	// debug_assert!(parser.at(T!["do"]));
	p.bump()

	if _, ok := parseTwigExpression(p); !ok {
		p.addError(newErrorBuilder("twig expression"))
		p.recover([]syntax.Kind{
			syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkLessThanSlash,
		})
	}

	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})
	return p.complete(outer, syntax.TwigDo)
}

func parseTwigDeprecated(p *parser, outer *marker) completedMarker {
	// debug_assert!(parser.at(T!["deprecated"]));
	p.bump()

	if p.atSet([]syntax.Kind{syntax.TkDoubleQuotes, syntax.TkSingleQuotes}) {
		parseTwigString(p, false)
	} else {
		p.addError(newErrorBuilder("twig deprecation message as string"))
		p.recover([]syntax.Kind{
			syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkLessThanSlash,
		})
	}

	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})
	return p.complete(outer, syntax.TwigDeprecated)
}

func parseTwigAutoescape(p *parser, outer *marker, childParser parseFunction) completedMarker {
	// debug_assert!(parser.at(T!["autoescape"]));
	p.bump()

	if p.atSet([]syntax.Kind{syntax.TkDoubleQuotes, syntax.TkSingleQuotes}) {
		parseTwigString(p, false)
	} else if p.at(syntax.TkFalse) {
		p.bump()
	} else if !p.atTwigBlockClose() {
		p.addError(newErrorBuilder("twig escape strategy as string or 'false'"))
		p.recover([]syntax.Kind{
			syntax.TkPercentCurly,
			syntax.TkMinusPercentCurly,
			syntax.TkTildePercentCurly,
			syntax.TkEndautoescape,
			syntax.TkLessThanSlash,
		})
	}

	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkEndautoescape, syntax.TkLessThanSlash})

	wrapperCm := p.complete(outer, syntax.TwigAutoescapeStartingBlock)
	wrapperM := p.precede(wrapperCm)

	// parse all the children except endautoescape
	bodyM := p.start()
	parseMany(p, func(p *parser) bool { return p.atTwigTag(syntax.TkEndautoescape) }, func(p *parser) {
		childParser(p)
	})
	p.complete(bodyM, syntax.Body)

	endBlockM := p.start()
	p.expectAny(twigBlockOpenSet, []syntax.Kind{
		syntax.TkEndautoescape,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	p.expect(syntax.TkEndautoescape, []syntax.Kind{
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})
	p.complete(endBlockM, syntax.TwigAutoescapeEndingBlock)

	return p.complete(wrapperM, syntax.TwigAutoescape)
}

func parseTwigApply(p *parser, outer *marker, childParser parseFunction) completedMarker {
	// debug_assert!(parser.at(T!["apply"]));
	p.bump()

	// parse any amount of filters
	if node, ok := parseTwigName(p); ok {
		// parse optional arguments
		if p.at(syntax.TkOpenParenthesis) {
			// parse any amount of arguments
			argumentsM := p.start()
			p.bump()
			parseMany(p, func(p *parser) bool {
				return p.atSet([]syntax.Kind{
					syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkCloseParenthesis,
				})
			}, func(p *parser) {
				parseTwigFunctionArgument(p)
				if p.at(syntax.TkComma) {
					p.bump()
				} else if !p.atSet([]syntax.Kind{
					syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkCloseParenthesis,
				}) {
					p.addError(newErrorBuilder(","))
				}
			})
			p.expect(syntax.TkCloseParenthesis, []syntax.Kind{
				syntax.TkEndapply,
				syntax.TkPercentCurly,
				syntax.TkMinusPercentCurly,
				syntax.TkTildePercentCurly,
				syntax.TkLessThanSlash,
			})
			p.complete(argumentsM, syntax.TwigArguments)
		}

		// parse any amount of piped filters
		parseMany(p, func(p *parser) bool { return p.atTwigBlockClose() }, func(p *parser) {
			if p.at(syntax.TkSinglePipe) {
				node = parseTwigFilter(p, node)
			}
		})
	} else {
		p.addError(newErrorBuilder("twig filter"))
		p.recover([]syntax.Kind{
			syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkEndapply, syntax.TkLessThanSlash,
		})
	}

	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkEndapply, syntax.TkLessThanSlash})

	wrapperCm := p.complete(outer, syntax.TwigApplyStartingBlock)
	wrapperM := p.precede(wrapperCm)

	// parse all the children except endapply
	bodyM := p.start()
	parseMany(p, func(p *parser) bool { return p.atTwigTag(syntax.TkEndapply) }, func(p *parser) {
		childParser(p)
	})
	p.complete(bodyM, syntax.Body)

	endBlockM := p.start()
	p.expectAny(twigBlockOpenSet, []syntax.Kind{
		syntax.TkEndapply,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	p.expect(syntax.TkEndapply, []syntax.Kind{
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})
	p.complete(endBlockM, syntax.TwigApplyEndingBlock)

	return p.complete(wrapperM, syntax.TwigApply)
}

func parseTwigImport(p *parser, outer *marker) completedMarker {
	// debug_assert!(parser.at_set(&[T!["import"], T!["sw_import"]]));
	p.bump()

	if _, ok := parseTwigExpression(p); !ok {
		p.addError(newErrorBuilder("twig expression as template"))
		p.recover([]syntax.Kind{
			syntax.TkAs, syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkWord, syntax.TkLessThanSlash,
		})
	}

	p.expect(syntax.TkAs, []syntax.Kind{
		syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkWord, syntax.TkLessThanSlash,
	})

	if _, ok := parseTwigName(p); !ok {
		p.addError(newErrorBuilder("name for twig macro"))
		p.recover([]syntax.Kind{
			syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkLessThanSlash,
		})
	}

	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})

	return p.complete(outer, syntax.TwigImport)
}

func parseTwigFrom(p *parser, outer *marker) completedMarker {
	// debug_assert!(parser.at_set(&[T!["from"], T!["sw_from"]]));
	p.bump()

	if _, ok := parseTwigExpression(p); !ok {
		p.addError(newErrorBuilder("twig expression as template"))
		p.recover([]syntax.Kind{
			syntax.TkImport,
			syntax.TkSwImport,
			syntax.TkPercentCurly,
			syntax.TkMinusPercentCurly,
			syntax.TkTildePercentCurly,
			syntax.TkLessThanSlash,
		})
	}

	if p.atSet([]syntax.Kind{syntax.TkImport, syntax.TkSwImport}) {
		p.bump()
	} else {
		p.addError(newErrorBuilder("import or sw_import"))
		p.recover([]syntax.Kind{
			syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkLessThanSlash,
		})
	}

	overrideCount := 0
	parseMany(p, func(p *parser) bool { return p.atTwigBlockClose() }, func(p *parser) {
		overrideCount++
		parseNameAsNameOverride(p, "macro name")
		if p.at(syntax.TkComma) {
			// consume optional comma
			p.bump()
		} else if !p.atTwigBlockClose() {
			p.addError(newErrorBuilder(","))
		}
	})

	if overrideCount < 1 {
		p.addError(newErrorBuilder("at least one macro name as macro name"))
	}

	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})

	return p.complete(outer, syntax.TwigFrom)
}

func parseNameAsNameOverride(p *parser, expectedDescription string) completedMarker {
	overrideM := p.start()
	if _, ok := parseTwigName(p); !ok {
		p.addError(newErrorBuilder(expectedDescription))
		p.recover([]syntax.Kind{
			syntax.TkAs, syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkLessThanSlash,
		})
	}
	if p.at(syntax.TkAs) {
		p.bump()
		if _, ok := parseTwigName(p); !ok {
			p.addError(newErrorBuilder(expectedDescription))
			p.recover([]syntax.Kind{
				syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkLessThanSlash,
			})
		}
	}
	return p.complete(overrideM, syntax.TwigOverride)
}

func parseTwigUse(p *parser, outer *marker) completedMarker {
	// debug_assert!(parser.at_set(&[T!["use"], T!["sw_use"]]));
	p.bump()

	if p.atSet([]syntax.Kind{syntax.TkDoubleQuotes, syntax.TkSingleQuotes}) {
		parseTwigString(p, false)
	} else {
		p.addError(newErrorBuilder("twig string as template"))
		p.recover([]syntax.Kind{
			syntax.TkWith,
			syntax.TkWord,
			syntax.TkPercentCurly,
			syntax.TkMinusPercentCurly,
			syntax.TkTildePercentCurly,
			syntax.TkLessThanSlash,
		})
	}

	if p.at(syntax.TkWith) {
		p.bump()

		overrideCount := 0
		parseMany(p, func(p *parser) bool { return p.atTwigBlockClose() }, func(p *parser) {
			overrideCount++
			parseNameAsNameOverride(p, "block name")
			if p.at(syntax.TkComma) {
				// consume optional comma
				p.bump()
			} else if !p.atTwigBlockClose() {
				p.addError(newErrorBuilder(","))
			}
		})

		if overrideCount < 1 {
			p.addError(newErrorBuilder("at least one block name as block name"))
		}
	}

	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})

	return p.complete(outer, syntax.TwigUse)
}

func parseTwigEmbed(p *parser, outer *marker, childParser parseFunction) completedMarker {
	// debug_assert!(parser.at_set(&[T!["embed"], T!["sw_embed"]]));
	isSwEmbed := p.at(syntax.TkSwEmbed)
	p.bump()

	expectedEndTag := syntax.TkEndembed
	if isSwEmbed {
		expectedEndTag = syntax.TkSwEndEmbed
	}

	// same arguments as include tag
	if _, ok := parseTwigExpression(p); !ok {
		p.addError(newErrorBuilder("twig expression as template name"))
		p.recover([]syntax.Kind{
			syntax.TkIgnoreMissing,
			syntax.TkWith,
			syntax.TkOnly,
			syntax.TkEndembed,
			syntax.TkSwEndEmbed,
			syntax.TkPercentCurly,
			syntax.TkMinusPercentCurly,
			syntax.TkTildePercentCurly,
			syntax.TkLessThanSlash,
		})
	}

	if p.at(syntax.TkIgnoreMissing) {
		p.bump()
	}

	if p.at(syntax.TkWith) {
		withValueM := p.start()
		p.bump()
		if _, ok := parseTwigExpression(p); !ok {
			p.addError(newErrorBuilder("twig expression as with value"))
			p.recover([]syntax.Kind{
				syntax.TkOnly,
				syntax.TkEndembed,
				syntax.TkSwEndEmbed,
				syntax.TkPercentCurly,
				syntax.TkMinusPercentCurly,
				syntax.TkTildePercentCurly,
				syntax.TkLessThanSlash,
			})
		}
		p.complete(withValueM, syntax.TwigIncludeWith)
	}

	if p.at(syntax.TkOnly) {
		p.bump()
	}

	p.expectAny(twigBlockCloseSet, []syntax.Kind{
		syntax.TkEndembed,
		syntax.TkSwEndEmbed,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})

	// but embed has a body
	wrapperCm := p.complete(outer, syntax.TwigEmbedStartingBlock)
	wrapperM := p.precede(wrapperCm)

	// parse all the children except endembed
	bodyM := p.start()
	parseMany(p, func(p *parser) bool {
		return p.atTwigTag(syntax.TkEndembed) || p.atTwigTag(syntax.TkSwEndEmbed)
	}, func(p *parser) {
		childParser(p)
	})
	p.complete(bodyM, syntax.Body)

	endBlockM := p.start()
	p.expectAny(twigBlockOpenSet, []syntax.Kind{
		syntax.TkEndembed,
		syntax.TkSwEndEmbed,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	p.expect(expectedEndTag, []syntax.Kind{
		syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkLessThanSlash,
	})
	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})
	p.complete(endBlockM, syntax.TwigEmbedEndingBlock)

	return p.complete(wrapperM, syntax.TwigEmbed)
}

func parseTwigInclude(p *parser, outer *marker) completedMarker {
	// debug_assert!(parser.at(T!["include"]));
	p.bump()

	if _, ok := parseTwigExpression(p); !ok {
		p.addError(newErrorBuilder("twig expression as template name"))
		p.recover([]syntax.Kind{
			syntax.TkIgnoreMissing,
			syntax.TkWith,
			syntax.TkOnly,
			syntax.TkPercentCurly,
			syntax.TkMinusPercentCurly,
			syntax.TkTildePercentCurly,
			syntax.TkLessThanSlash,
		})
	}

	if p.at(syntax.TkIgnoreMissing) {
		p.bump()
	}

	if p.at(syntax.TkWith) {
		withValueM := p.start()
		p.bump()
		if _, ok := parseTwigExpression(p); !ok {
			p.addError(newErrorBuilder("twig expression as with value"))
			p.recover([]syntax.Kind{
				syntax.TkOnly, syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkLessThanSlash,
			})
		}
		p.complete(withValueM, syntax.TwigIncludeWith)
	}

	if p.at(syntax.TkOnly) {
		p.bump()
	}

	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})

	return p.complete(outer, syntax.TwigInclude)
}

func parseTwigExtends(p *parser, outer *marker) completedMarker {
	// debug_assert!(parser.at(T!["extends"]));
	p.bump()

	if _, ok := parseTwigExpression(p); !ok {
		p.addError(newErrorBuilder("twig expression"))
		p.recover([]syntax.Kind{
			syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkLessThanSlash,
		})
	}

	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})

	return p.complete(outer, syntax.TwigExtends)
}

func parseTwigFor(p *parser, outer *marker, childParser parseFunction) completedMarker {
	// debug_assert!(parser.at(T!["for"]));
	p.bump()

	// parse key, value identifiers
	if _, ok := parseTwigName(p); !ok {
		p.addError(newErrorBuilder("variable name"))
		p.recover([]syntax.Kind{
			syntax.TkComma,
			syntax.TkIn,
			syntax.TkElse,
			syntax.TkEndfor,
			syntax.TkPercentCurly,
			syntax.TkMinusPercentCurly,
			syntax.TkTildePercentCurly,
			syntax.TkLessThanSlash,
		})
	}
	if p.at(syntax.TkComma) {
		p.bump()
		if _, ok := parseTwigName(p); !ok {
			p.addError(newErrorBuilder("variable name"))
			p.recover([]syntax.Kind{
				syntax.TkIn,
				syntax.TkElse,
				syntax.TkEndfor,
				syntax.TkPercentCurly,
				syntax.TkMinusPercentCurly,
				syntax.TkTildePercentCurly,
				syntax.TkLessThanSlash,
			})
		}
	}

	p.expect(syntax.TkIn, []syntax.Kind{
		syntax.TkElse,
		syntax.TkEndfor,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})

	// parse expression after in
	if _, ok := parseTwigExpression(p); !ok {
		p.addError(newErrorBuilder("twig expression"))
		p.recover([]syntax.Kind{
			syntax.TkPercentCurly,
			syntax.TkMinusPercentCurly,
			syntax.TkTildePercentCurly,
			syntax.TkElse,
			syntax.TkEndfor,
			syntax.TkLessThanSlash,
		})
	}

	p.expectAny(twigBlockCloseSet, []syntax.Kind{
		syntax.TkElse,
		syntax.TkEndfor,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})

	wrapperCm := p.complete(outer, syntax.TwigForBlock)
	wrapperM := p.precede(wrapperCm)

	// parse all the children except else or endfor
	bodyM := p.start()
	parseMany(p, func(p *parser) bool {
		return p.atTwigTag(syntax.TkEndfor) || p.atTwigTag(syntax.TkElse)
	}, func(p *parser) {
		childParser(p)
	})
	p.complete(bodyM, syntax.Body)

	// check for else block
	if p.atTwigTag(syntax.TkElse) {
		elseM := p.start()
		p.bump()
		p.bump()
		p.expectAny(twigBlockCloseSet, []syntax.Kind{
			syntax.TkEndfor,
			syntax.TkPercentCurly,
			syntax.TkMinusPercentCurly,
			syntax.TkTildePercentCurly,
			syntax.TkLessThanSlash,
		})
		p.complete(elseM, syntax.TwigForElseBlock)

		// parse all the children except endfor
		bodyM := p.start()
		parseMany(p, func(p *parser) bool { return p.atTwigTag(syntax.TkEndfor) }, func(p *parser) {
			childParser(p)
		})
		p.complete(bodyM, syntax.Body)
	}

	endBlockM := p.start()
	p.expectAny(twigBlockOpenSet, []syntax.Kind{
		syntax.TkEndfor,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	p.expect(syntax.TkEndfor, []syntax.Kind{
		syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkLessThanSlash,
	})
	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})
	p.complete(endBlockM, syntax.TwigEndforBlock)

	return p.complete(wrapperM, syntax.TwigFor)
}

func parseTwigSet(p *parser, outer *marker, childParser parseFunction) completedMarker {
	// debug_assert!(parser.at(T!["set"]));
	p.bump()

	// parse any amount of words seperated by comma
	assignmentM := p.start()
	declarationCount := 0
	parseMany(p, func(p *parser) bool {
		return p.atSet([]syntax.Kind{
			syntax.TkEqual, syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly,
		})
	}, func(p *parser) {
		if _, ok := parseTwigName(p); ok {
			declarationCount++
		} else {
			p.addError(newErrorBuilder("twig variable name"))
		}

		if p.at(syntax.TkComma) {
			p.bump()
		} else if !p.atSet([]syntax.Kind{
			syntax.TkEqual, syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly,
		}) {
			p.addError(newErrorBuilder(","))
		}
	})
	if declarationCount == 0 {
		p.addError(newErrorBuilder("twig variable name"))
	}

	// check for equal assignment
	isAssignedByEqual := false
	if p.at(syntax.TkEqual) {
		p.bump()
		isAssignedByEqual = true

		assignmentCount := 0
		parseMany(p, func(p *parser) bool { return p.atTwigBlockClose() }, func(p *parser) {
			if _, ok := parseTwigExpression(p); ok {
				assignmentCount++
			} else {
				p.addError(newErrorBuilder("twig expression"))
			}

			if p.at(syntax.TkComma) {
				p.bump()
			} else if !p.atTwigBlockClose() {
				p.addError(newErrorBuilder(","))
			}
		})

		if declarationCount != assignmentCount {
			p.addError(newErrorBuilder(fmt.Sprintf(
				"a total of %d twig expressions (same amount as declarations) instead of %d",
				declarationCount, assignmentCount,
			)))
		}
	} else if declarationCount > 1 {
		p.addError(newErrorBuilder(fmt.Sprintf(
			"= followed by %d twig expressions", declarationCount,
		)))
	}

	p.complete(assignmentM, syntax.TwigAssignment)
	p.expectAny(twigBlockCloseSet, []syntax.Kind{
		syntax.TkEndset,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})

	wrapperCm := p.complete(outer, syntax.TwigSetBlock)
	wrapperM := p.precede(wrapperCm)

	if !isAssignedByEqual {
		// children and a closing endset should be there

		// parse all the children except endset
		bodyM := p.start()
		parseMany(p, func(p *parser) bool { return p.atTwigTag(syntax.TkEndset) }, func(p *parser) {
			childParser(p)
		})
		p.complete(bodyM, syntax.Body)

		endBlockM := p.start()
		p.expectAny(twigBlockOpenSet, []syntax.Kind{
			syntax.TkEndset,
			syntax.TkPercentCurly,
			syntax.TkMinusPercentCurly,
			syntax.TkTildePercentCurly,
			syntax.TkLessThanSlash,
		})
		p.expect(syntax.TkEndset, []syntax.Kind{
			syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkLessThanSlash,
		})
		p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})
		p.complete(endBlockM, syntax.TwigEndsetBlock)
	}

	return p.complete(wrapperM, syntax.TwigSet)
}

func parseTwigBlock(p *parser, outer *marker, childParser parseFunction) completedMarker {
	// debug_assert!(parser.at(T!["block"]));
	p.bump()
	blockNameTok := p.expect(syntax.TkWord, []syntax.Kind{
		syntax.TkLessThanSlash, syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkEndblock,
	})
	var blockName string
	hasBlockName := false
	if blockNameTok != nil {
		blockName = blockNameTok.Text()
		hasBlockName = true
	}
	// look for optional shortcut
	foundShortcut := false
	if !p.atTwigBlockClose() {
		if _, ok := parseTwigExpression(p); !ok {
			p.addError(newErrorBuilder("twig expression or '%}'"))
			p.recover([]syntax.Kind{
				syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkEndblock, syntax.TkLessThanSlash,
			})
		} else {
			foundShortcut = true
		}
	}
	p.expectAny(twigBlockCloseSet, []syntax.Kind{
		syntax.TkLessThanSlash, syntax.TkEndblock, syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly,
	})

	wrapperCm := p.complete(outer, syntax.TwigStartingBlock)
	wrapperM := p.precede(wrapperCm)

	if !foundShortcut {
		// parse all the children except endblock
		bodyM := p.start()
		parseMany(p, func(p *parser) bool { return p.atTwigTag(syntax.TkEndblock) }, func(p *parser) {
			childParser(p)
		})
		p.complete(bodyM, syntax.Body)

		endBlockM := p.start()
		p.expectAny(twigBlockOpenSet, []syntax.Kind{
			syntax.TkLessThanSlash, syntax.TkEndblock, syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly,
		})
		p.expect(syntax.TkEndblock, []syntax.Kind{
			syntax.TkLessThanSlash, syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly,
		})
		// check for optional name behind endblock
		if p.at(syntax.TkWord) {
			endBlockNameToken := p.bump()
			if hasBlockName {
				if endBlockNameToken.Text() != blockName {
					parserErr := newErrorBuilder(fmt.Sprintf(
						"nothing or same twig block name as opening (%s)", blockName,
					)).atToken(endBlockNameToken)
					p.addError(parserErr)
					p.recover([]syntax.Kind{
						syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkLessThanSlash,
					})
				}
			}
		}
		p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})
		p.complete(endBlockM, syntax.TwigEndingBlock)
	}

	return p.complete(wrapperM, syntax.TwigBlock)
}

func parseTwigIf(p *parser, outer *marker, childParser parseFunction) completedMarker {
	// debug_assert!(parser.at(T!["if"]));
	p.bump()

	if _, ok := parseTwigExpression(p); !ok {
		p.addError(newErrorBuilder("twig expression"))
		p.recover([]syntax.Kind{
			syntax.TkPercentCurly,
			syntax.TkMinusPercentCurly,
			syntax.TkTildePercentCurly,
			syntax.TkElse,
			syntax.TkElseIf,
			syntax.TkEndif,
			syntax.TkLessThanSlash,
		})
	}
	p.expectAny(twigBlockCloseSet, []syntax.Kind{
		syntax.TkElse,
		syntax.TkElseIf,
		syntax.TkEndif,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})

	wrapperCm := p.complete(outer, syntax.TwigIfBlock)
	wrapperM := p.precede(wrapperCm)

	// parse branches
	for {
		// parse body (all the children)
		bodyM := p.start()
		parseMany(p, func(p *parser) bool {
			return p.atTwigTag(syntax.TkEndif) ||
				p.atTwigTag(syntax.TkElseIf) ||
				p.atTwigTag(syntax.TkElse)
		}, func(p *parser) {
			childParser(p)
		})
		p.complete(bodyM, syntax.Body)

		if p.atTwigTag(syntax.TkEndif) {
			break // no more branches
		}

		// parse next branch header
		if p.atTwigTag(syntax.TkElseIf) {
			branchM := p.start()
			p.bump()
			p.bump()
			if _, ok := parseTwigExpression(p); !ok {
				p.addError(newErrorBuilder("twig expression"))
				p.recover([]syntax.Kind{
					syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkEndif, syntax.TkLessThanSlash,
				})
			}
			p.expectAny(twigBlockCloseSet, []syntax.Kind{
				syntax.TkEndif,
				syntax.TkPercentCurly,
				syntax.TkMinusPercentCurly,
				syntax.TkTildePercentCurly,
				syntax.TkLessThanSlash,
			})
			p.complete(branchM, syntax.TwigElseIfBlock)
		} else if p.atTwigTag(syntax.TkElse) {
			branchM := p.start()
			p.bump()
			p.bump()
			p.expectAny(twigBlockCloseSet, []syntax.Kind{
				syntax.TkEndif,
				syntax.TkPercentCurly,
				syntax.TkMinusPercentCurly,
				syntax.TkTildePercentCurly,
				syntax.TkLessThanSlash,
			})
			p.complete(branchM, syntax.TwigElseBlock)
		} else {
			// not an actual branch found, the child parser has ended
			break
		}
	}

	endBlockM := p.start()
	p.expectAny(twigBlockOpenSet, []syntax.Kind{
		syntax.TkEndif,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	p.expect(syntax.TkEndif, []syntax.Kind{
		syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkLessThanSlash,
	})
	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})
	p.complete(endBlockM, syntax.TwigEndifBlock)

	return p.complete(wrapperM, syntax.TwigIf)
}

func parseTwigTrans(p *parser, outer *marker, childParser parseFunction) completedMarker {
	// debug_assert!(parser.at(T!["trans"]));
	p.bump()

	p.expectAny(twigBlockCloseSet, []syntax.Kind{
		syntax.TkEndtrans,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})

	wrapperCm := p.complete(outer, syntax.TwigTransStartingBlock)
	wrapperM := p.precede(wrapperCm)

	// parse all the children except endtrans
	bodyM := p.start()
	parseMany(p, func(p *parser) bool { return p.atTwigTag(syntax.TkEndtrans) }, func(p *parser) {
		childParser(p)
	})
	p.complete(bodyM, syntax.Body)

	endBlockM := p.start()
	p.expectAny(twigBlockOpenSet, []syntax.Kind{
		syntax.TkEndtrans,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	p.expect(syntax.TkEndtrans, []syntax.Kind{
		syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkLessThanSlash,
	})
	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})
	p.complete(endBlockM, syntax.TwigTransEndingBlock)

	return p.complete(wrapperM, syntax.TwigTrans)
}

func parseTwigProps(p *parser, outer *marker) completedMarker {
	// example:
	// {% props icon, type = 'primary' %}

	// debug_assert!(parser.at(T!["props"]));
	p.bump()

	parseMany(p, func(p *parser) bool {
		return p.atSet([]syntax.Kind{
			syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkComma, syntax.TkEqual, syntax.TkLessThanSlash,
		})
	}, func(p *parser) {
		m := p.start()
		if _, ok := parseTwigName(p); !ok {
			p.addError(newErrorBuilder("twig variable name"))
		}

		if p.at(syntax.TkEqual) {
			p.bump()

			if _, ok := parseTwigExpression(p); !ok {
				p.addError(newErrorBuilder("twig expression"))
			}
		}

		p.complete(m, syntax.TwigPropDeclaration)

		if p.at(syntax.TkComma) {
			p.bump()
		} else if !p.atTwigBlockClose() {
			p.addError(newErrorBuilder(","))
		}
	})

	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})
	return p.complete(outer, syntax.TwigProps)
}

func parseTwigComponent(p *parser, outer *marker, childParser parseFunction) completedMarker {
	// example:
	// {% component Alert with {type: 'success'} %}
	//     {% block content %}<div>Congrats!</div>{% endblock %}
	//     {% block footer %}... footer content{% endblock %}
	// {% endcomponent %}

	// debug_assert!(parser.at(T!["component"]));
	p.bump()

	if p.atSet([]syntax.Kind{syntax.TkSingleQuotes, syntax.TkDoubleQuotes}) {
		parseTwigString(p, false)
	} else if _, ok := parseTwigName(p); !ok {
		p.addError(newErrorBuilder("component name"))
	}

	if p.at(syntax.TkWith) {
		p.bump()

		if p.at(syntax.TkOpenCurly) {
			parseTwigHash(p)
		} else {
			p.addError(newErrorBuilder("twig hash/object"))
		}
	}

	p.expectAny(twigBlockCloseSet, []syntax.Kind{
		syntax.TkEndcomponent,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	wrapperCm := p.complete(outer, syntax.TwigComponentStartingBlock)
	wrapperM := p.precede(wrapperCm)

	// parse all the children except endcomponent
	bodyM := p.start()
	parseMany(p, func(p *parser) bool { return p.atTwigTag(syntax.TkEndcomponent) }, func(p *parser) {
		childParser(p)
	})
	p.complete(bodyM, syntax.Body)

	endBlockM := p.start()
	p.expectAny(twigBlockOpenSet, []syntax.Kind{
		syntax.TkEndcomponent,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
		syntax.TkLessThanSlash,
	})
	p.expect(syntax.TkEndcomponent, []syntax.Kind{
		syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly, syntax.TkLessThanSlash,
	})
	p.expectAny(twigBlockCloseSet, []syntax.Kind{syntax.TkLessThanSlash})
	p.complete(endBlockM, syntax.TwigComponentEndingBlock)

	return p.complete(wrapperM, syntax.TwigComponent)
}
