package parser

import (
	"github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// blockParseResult mirrors Rust's BlockParseResult enum.
//
// ok == true  -> Successful(cm): the outer marker was completed into cm.
// ok == false -> NothingFound: the outer marker was left untouched and must be
//
//	completed as ERROR by the caller.
type blockParseResult struct {
	cm completedMarker
	ok bool
}

// parseShopwareTwigBlockStatement dispatches the shopware-specific {% %} tags.
// The {% opener has already been consumed by the caller. Ported from
// parse_shopware_twig_block_statement (shopware.rs).
func parseShopwareTwigBlockStatement(p *parser, outer *marker, childParser parseFunction) blockParseResult {
	// {% already consumed
	switch {
	case p.at(syntax.TkSwExtends):
		return blockParseResult{cm: parseTwigSwExtends(p, outer), ok: true}
	case p.at(syntax.TkSwInclude):
		return blockParseResult{cm: parseTwigSwInclude(p, outer), ok: true}
	case p.at(syntax.TkSwSilentFeatureCall):
		return blockParseResult{cm: parseTwigSwSilentFeatureCall(p, outer, childParser), ok: true}
	case p.at(syntax.TkReturn):
		return blockParseResult{cm: parseTwigSwReturn(p, outer), ok: true}
	case p.at(syntax.TkSwIcon):
		return blockParseResult{cm: parseTwigSwIcon(p, outer), ok: true}
	case p.at(syntax.TkSwThumbnails):
		return blockParseResult{cm: parseTwigSwThumbnails(p, outer), ok: true}
	default:
		// error will be thrown by calling function
		return blockParseResult{ok: false}
	}
}

func parseTwigSwThumbnails(p *parser, outer *marker) completedMarker {
	// debug_assert!(parser.at(T!["sw_thumbnails"]));
	p.bump()

	if _, ok := parseTwigExpression(p); !ok {
		p.addError(newErrorBuilder("twig expression as thumbnail name"))
		p.recover([]syntax.Kind{
			syntax.TkWith, syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly,
		})
	}

	if p.at(syntax.TkWith) {
		styleM := p.start()
		p.bump()
		if _, ok := parseTwigExpression(p); !ok {
			p.addError(newErrorBuilder("twig expression as thumbnail variables"))
			p.recover([]syntax.Kind{
				syntax.TkWith, syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly,
			})
		}
		p.complete(styleM, syntax.ShopwareThumbnailsWith)
	}

	p.expectAny(twigBlockCloseSet, nil)
	return p.complete(outer, syntax.ShopwareThumbnails)
}

func parseTwigSwIcon(p *parser, outer *marker) completedMarker {
	// debug_assert!(parser.at(T!["sw_icon"]));
	p.bump()

	if _, ok := parseTwigExpression(p); !ok {
		p.addError(newErrorBuilder("twig expression as icon name"))
		p.recover([]syntax.Kind{
			syntax.TkStyle, syntax.TkOpenCurly,
			syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly,
		})
	}

	// Shopware templates exist in both forms:
	//   {% sw_icon 'name' style {'pack': 'custom'} %}
	//   {% sw_icon 'name' {'pack': 'custom'} %}
	// Keep both losslessly represented as SHOPWARE_ICON_STYLE.
	if p.at(syntax.TkStyle) || p.at(syntax.TkOpenCurly) {
		styleM := p.start()
		if p.at(syntax.TkStyle) {
			p.bump()
		}
		if _, ok := parseTwigExpression(p); !ok {
			p.addError(newErrorBuilder("twig expression as icon style variables"))
			p.recover([]syntax.Kind{
				syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly,
			})
		}
		p.complete(styleM, syntax.ShopwareIconStyle)
	}

	p.expectAny(twigBlockCloseSet, nil)
	return p.complete(outer, syntax.ShopwareIcon)
}

func parseTwigSwReturn(p *parser, outer *marker) completedMarker {
	// debug_assert!(parser.at(T!["return"]));
	p.bump()

	parseTwigExpression(p)

	p.expectAny(twigBlockCloseSet, nil)
	return p.complete(outer, syntax.ShopwareReturn)
}

func parseTwigSwSilentFeatureCall(p *parser, outer *marker, childParser parseFunction) completedMarker {
	// debug_assert!(parser.at(T!["sw_silent_feature_call"]));
	p.bump()
	if p.atSet([]syntax.Kind{syntax.TkDoubleQuotes, syntax.TkSingleQuotes}) {
		parseTwigString(p, false)
	} else {
		p.addError(newErrorBuilder("twig string as feature flag (shopware doesn't allow expressions here)"))
		p.recover([]syntax.Kind{
			syntax.TkEndswSilentFeatureCall,
			syntax.TkPercentCurly,
			syntax.TkMinusPercentCurly,
			syntax.TkTildePercentCurly,
		})
	}
	p.expectAny(twigBlockCloseSet, []syntax.Kind{
		syntax.TkEndswSilentFeatureCall,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
	})
	wrapperCm := p.complete(outer, syntax.ShopwareSilentFeatureCallStartingBlock)
	wrapperM := p.precede(wrapperCm)

	// parse all the children except endsw_silent_feature_call
	bodyM := p.start()
	parseMany(p, func(p *parser) bool { return p.atTwigTag(syntax.TkEndswSilentFeatureCall) }, func(p *parser) {
		childParser(p)
	})
	p.complete(bodyM, syntax.Body)

	endBlockM := p.start()
	p.expectAny(twigBlockOpenSet, []syntax.Kind{
		syntax.TkEndswSilentFeatureCall,
		syntax.TkPercentCurly,
		syntax.TkMinusPercentCurly,
		syntax.TkTildePercentCurly,
	})
	p.expect(syntax.TkEndswSilentFeatureCall, []syntax.Kind{
		syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly,
	})
	p.expectAny(twigBlockCloseSet, nil)
	p.complete(endBlockM, syntax.ShopwareSilentFeatureCallEndingBlock)

	return p.complete(wrapperM, syntax.ShopwareSilentFeatureCall)
}

func parseTwigSwExtends(p *parser, outer *marker) completedMarker {
	// debug_assert!(parser.at(T!["sw_extends"]));
	p.bump()

	// Modern Shopware also accepts a hash carrying template and scopes:
	// {% sw_extends { template: 'base.html.twig', scopes: ['default'] } %}
	if _, ok := parseTwigExpression(p); !ok {
		p.addError(newErrorBuilder("twig expression as template"))
		p.recover([]syntax.Kind{
			syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly,
		})
	}

	p.expectAny(twigBlockCloseSet, nil)
	return p.complete(outer, syntax.ShopwareTwigSwExtends)
}

func parseTwigSwInclude(p *parser, outer *marker) completedMarker {
	// debug_assert!(parser.at(T!["sw_include"]));
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
				syntax.TkOnly, syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly,
			})
		}
		p.complete(withValueM, syntax.TwigIncludeWith)
	}

	if p.at(syntax.TkOnly) {
		p.bump()
	}

	p.expectAny(twigBlockCloseSet, nil)

	return p.complete(outer, syntax.ShopwareTwigSwInclude)
}
