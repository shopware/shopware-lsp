package color

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/parser/scss/query"
	"github.com/shopware/shopware-lsp/internal/parser/scss/syntax"
)

// AdminSCSSColorProvider supplies editor swatches and color-picker edits for
// statically known SCSS colors. Dynamic Sass expressions are intentionally
// omitted because reporting a guessed color is worse than reporting none.
type AdminSCSSColorProvider struct{}

func NewAdminSCSSColorProvider() *AdminSCSSColorProvider {
	return &AdminSCSSColorProvider{}
}

func (p *AdminSCSSColorProvider) GetDocumentColors(
	ctx context.Context,
	request *lsp.DocumentColorRequest,
) ([]protocol.ColorInformation, error) {
	if ctx.Err() != nil || p == nil || request == nil || request.Document == nil ||
		(request.Document.SyntaxLanguage != language.SCSS &&
			request.Document.SyntaxLanguage != language.Vue) ||
		request.Document.SyntaxTree == nil || request.Document.SyntaxTree.Root == nil ||
		request.Document.LineIndex == nil {
		return nil, ctx.Err()
	}
	root := request.Document.SyntaxTree.Root
	var result []protocol.ColorInformation
	for element := range root.Descendants() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		token, ok := element.(*cst.Token)
		if !ok || token.Kind() != syntax.TkHash || !inColorValue(token.Parent()) {
			continue
		}
		next, ok := token.NextSibling().(*cst.Token)
		if !ok || next.Range().Start != token.Range().End ||
			(next.Kind() != syntax.TkIdentifier && next.Kind() != syntax.TkNumber) {
			continue
		}
		parsed, ok := parseHexColor(next.Text())
		if !ok {
			continue
		}
		result = append(result, protocol.ColorInformation{
			Range: scssProtocolRange(cst.TextRange{
				Start: token.Range().Start,
				End:   next.Range().End,
			}, request.Document.LineIndex),
			Color: parsed,
		})
	}
	for _, call := range query.Nodes(root, syntax.ScssFunctionCall) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !inColorValue(call.Parent()) {
			continue
		}
		rangeValue, text, ok := colorFunctionText(call, request.Document.Text)
		if !ok {
			continue
		}
		parsed, ok := parseColorFunction(text)
		if !ok {
			continue
		}
		result = append(result, protocol.ColorInformation{
			Range: scssProtocolRange(rangeValue, request.Document.LineIndex),
			Color: parsed,
		})
	}
	return result, nil
}

func (p *AdminSCSSColorProvider) GetColorPresentations(
	ctx context.Context,
	request *lsp.ColorPresentationRequest,
) ([]protocol.ColorPresentation, error) {
	if ctx.Err() != nil || p == nil || request == nil || request.Document == nil ||
		(request.Document.SyntaxLanguage != language.SCSS &&
			request.Document.SyntaxLanguage != language.Vue) {
		return nil, ctx.Err()
	}
	color := protocol.Color{
		Red: clamp(request.Color.Red, 0, 1), Green: clamp(request.Color.Green, 0, 1),
		Blue: clamp(request.Color.Blue, 0, 1), Alpha: clamp(request.Color.Alpha, 0, 1),
	}
	red := int(math.Round(color.Red * 255))
	green := int(math.Round(color.Green * 255))
	blue := int(math.Round(color.Blue * 255))
	opaque := math.Abs(color.Alpha-1) < 0.0005
	hex := fmt.Sprintf("#%02x%02x%02x", red, green, blue)
	if !opaque {
		hex += fmt.Sprintf("%02x", int(math.Round(color.Alpha*255)))
	}
	rgb := fmt.Sprintf("rgb(%d, %d, %d)", red, green, blue)
	if !opaque {
		rgb = fmt.Sprintf("rgba(%d, %d, %d, %s)", red, green, blue, formatNumber(color.Alpha*100)+"%")
	}
	hue, saturation, lightness := rgbToHSL(color.Red, color.Green, color.Blue)
	hsl := fmt.Sprintf("hsl(%s, %s%%, %s%%)",
		formatNumber(hue), formatNumber(saturation*100), formatNumber(lightness*100))
	if !opaque {
		hsl = fmt.Sprintf("hsla(%s, %s%%, %s%%, %s%%)",
			formatNumber(hue), formatNumber(saturation*100),
			formatNumber(lightness*100), formatNumber(color.Alpha*100))
	}
	labels := []string{hex, rgb, hsl}
	result := make([]protocol.ColorPresentation, 0, len(labels))
	for _, label := range labels {
		result = append(result, protocol.ColorPresentation{
			Label: label,
			TextEdit: &protocol.TextEdit{
				Range: request.Range, NewText: label,
			},
		})
	}
	return result, nil
}

func inColorValue(node *cst.Node) bool {
	for current := node; current != nil; current = current.Parent() {
		switch current.Kind() {
		case syntax.ScssDeclaration, syntax.ScssVariableDeclaration:
			return true
		case syntax.ScssRule, syntax.ScssStylesheet:
			return false
		}
	}
	return false
}

func colorFunctionText(node *cst.Node, source []byte) (cst.TextRange, string, bool) {
	var name *cst.Token
	var closeParen *cst.Token
	for element := range node.Descendants() {
		token, ok := element.(*cst.Token)
		if !ok {
			continue
		}
		if name == nil && token.Kind() == syntax.TkIdentifier {
			name = token
		}
		if token.Kind() == syntax.TkCloseParen {
			closeParen = token
		}
	}
	if name == nil || closeParen == nil || closeParen.Range().End <= name.Range().Start ||
		int(closeParen.Range().End) > len(source) {
		return cst.TextRange{}, "", false
	}
	rangeValue := cst.TextRange{Start: name.Range().Start, End: closeParen.Range().End}
	return rangeValue, string(source[rangeValue.Start:rangeValue.End]), true
}

func parseHexColor(value string) (protocol.Color, bool) {
	if len(value) != 3 && len(value) != 4 && len(value) != 6 && len(value) != 8 {
		return protocol.Color{}, false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return protocol.Color{}, false
		}
	}
	expanded := value
	if len(value) <= 4 {
		var builder strings.Builder
		builder.Grow(len(value) * 2)
		for _, character := range value {
			builder.WriteRune(character)
			builder.WriteRune(character)
		}
		expanded = builder.String()
	}
	channels := []uint64{0, 0, 0, 255}
	for index := 0; index < len(expanded)/2; index++ {
		parsed, err := strconv.ParseUint(expanded[index*2:index*2+2], 16, 8)
		if err != nil {
			return protocol.Color{}, false
		}
		channels[index] = parsed
	}
	return protocol.Color{
		Red: float64(channels[0]) / 255, Green: float64(channels[1]) / 255,
		Blue: float64(channels[2]) / 255, Alpha: float64(channels[3]) / 255,
	}, true
}

func parseColorFunction(text string) (protocol.Color, bool) {
	open := strings.IndexByte(text, '(')
	if open <= 0 || !strings.HasSuffix(strings.TrimSpace(text), ")") {
		return protocol.Color{}, false
	}
	name := strings.ToLower(strings.TrimSpace(text[:open]))
	body := strings.TrimSpace(text[open+1 : strings.LastIndex(text, ")")])
	if body == "" || strings.ContainsAny(body, "()${}") {
		return protocol.Color{}, false
	}
	parts, alpha, ok := colorFunctionParts(body)
	if !ok || len(parts) != 3 {
		return protocol.Color{}, false
	}
	switch name {
	case "rgb", "rgba":
		red, okRed := parseRGBChannel(parts[0])
		green, okGreen := parseRGBChannel(parts[1])
		blue, okBlue := parseRGBChannel(parts[2])
		if !okRed || !okGreen || !okBlue {
			return protocol.Color{}, false
		}
		return protocol.Color{Red: red, Green: green, Blue: blue, Alpha: alpha}, true
	case "hsl", "hsla":
		hue, okHue := parseHue(parts[0])
		saturation, okSaturation := parsePercentage(parts[1])
		lightness, okLightness := parsePercentage(parts[2])
		if !okHue || !okSaturation || !okLightness {
			return protocol.Color{}, false
		}
		red, green, blue := hslToRGB(hue, saturation, lightness)
		return protocol.Color{Red: red, Green: green, Blue: blue, Alpha: alpha}, true
	default:
		return protocol.Color{}, false
	}
}

func colorFunctionParts(body string) ([]string, float64, bool) {
	alpha := 1.0
	if strings.Contains(body, ",") {
		parts := strings.Split(body, ",")
		for index := range parts {
			parts[index] = strings.TrimSpace(parts[index])
		}
		if len(parts) == 4 {
			var ok bool
			alpha, ok = parseAlpha(parts[3])
			if !ok {
				return nil, 0, false
			}
			parts = parts[:3]
		}
		return parts, alpha, len(parts) == 3
	}
	fields := strings.Fields(strings.ReplaceAll(body, "/", " / "))
	if len(fields) == 5 && fields[3] == "/" {
		var ok bool
		alpha, ok = parseAlpha(fields[4])
		if !ok {
			return nil, 0, false
		}
		fields = fields[:3]
	}
	return fields, alpha, len(fields) == 3
}

func parseRGBChannel(value string) (float64, bool) {
	if strings.HasSuffix(value, "%") {
		return parsePercentage(value)
	}
	parsed, ok := parseBounded(strings.TrimSpace(value), 0, 255)
	return parsed / 255, ok
}

func parsePercentage(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasSuffix(value, "%") {
		return 0, false
	}
	parsed, ok := parseBounded(strings.TrimSpace(strings.TrimSuffix(value, "%")), 0, 100)
	return parsed / 100, ok
}

func parseAlpha(value string) (float64, bool) {
	if strings.HasSuffix(strings.TrimSpace(value), "%") {
		return parsePercentage(value)
	}
	return parseBounded(strings.TrimSpace(value), 0, 1)
}

func parseHue(value string) (float64, bool) {
	value = strings.TrimSpace(strings.ToLower(value))
	multiplier := 1.0
	switch {
	case strings.HasSuffix(value, "turn"):
		value, multiplier = strings.TrimSuffix(value, "turn"), 360
	case strings.HasSuffix(value, "grad"):
		value, multiplier = strings.TrimSuffix(value, "grad"), 0.9
	case strings.HasSuffix(value, "rad"):
		value, multiplier = strings.TrimSuffix(value, "rad"), 180/math.Pi
	case strings.HasSuffix(value, "deg"):
		value = strings.TrimSuffix(value, "deg")
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, false
	}
	parsed = math.Mod(parsed*multiplier, 360)
	if parsed < 0 {
		parsed += 360
	}
	return parsed, true
}

func parseBounded(value string, minimum, maximum float64) (float64, bool) {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) ||
		parsed < minimum || parsed > maximum {
		return 0, false
	}
	return parsed, true
}

func hslToRGB(hue, saturation, lightness float64) (float64, float64, float64) {
	chroma := (1 - math.Abs(2*lightness-1)) * saturation
	x := chroma * (1 - math.Abs(math.Mod(hue/60, 2)-1))
	red, green, blue := 0.0, 0.0, 0.0
	switch {
	case hue < 60:
		red, green = chroma, x
	case hue < 120:
		red, green = x, chroma
	case hue < 180:
		green, blue = chroma, x
	case hue < 240:
		green, blue = x, chroma
	case hue < 300:
		red, blue = x, chroma
	default:
		red, blue = chroma, x
	}
	match := lightness - chroma/2
	return red + match, green + match, blue + match
}

func rgbToHSL(red, green, blue float64) (float64, float64, float64) {
	maximum := math.Max(red, math.Max(green, blue))
	minimum := math.Min(red, math.Min(green, blue))
	delta := maximum - minimum
	lightness := (maximum + minimum) / 2
	if delta == 0 {
		return 0, 0, lightness
	}
	saturation := delta / (1 - math.Abs(2*lightness-1))
	var hue float64
	switch maximum {
	case red:
		hue = 60 * math.Mod((green-blue)/delta, 6)
	case green:
		hue = 60 * ((blue-red)/delta + 2)
	default:
		hue = 60 * ((red-green)/delta + 4)
	}
	if hue < 0 {
		hue += 360
	}
	return hue, saturation, lightness
}

func formatNumber(value float64) string {
	if math.Abs(value) < 0.0005 {
		value = 0
	}
	formatted := strconv.FormatFloat(value, 'f', 1, 64)
	return strings.TrimSuffix(formatted, ".0")
}

func clamp(value, minimum, maximum float64) float64 {
	return math.Min(maximum, math.Max(minimum, value))
}

func scssProtocolRange(rangeValue cst.TextRange, lineIndex *cst.LineIndex) protocol.Range {
	startLine, startCharacter := lineIndex.PositionUTF16(rangeValue.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rangeValue.End)
	return protocol.Range{
		Start: protocol.Position{Line: int(startLine), Character: int(startCharacter)},
		End:   protocol.Position{Line: int(endLine), Character: int(endCharacter)},
	}
}

var _ lsp.DocumentColorProvider = (*AdminSCSSColorProvider)(nil)
