package analytics

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	ListProfilerRequestsCommand = "shopware/symfony/analytics/profiler/requests"

	defaultProfilerRequestLimit = 25
	maxProfilerRequestLimit     = 100
	maxProfilerIndexBytes       = 16 << 20
	maxProfilerIndexRowBytes    = 1 << 20
	maxProfilerContentBytes     = 10 << 20
	maxProfilerScanRows         = 2_000
)

var (
	profilerHashPattern = regexp.MustCompile(`^[a-fA-F0-9]{6,64}$`)
	templatePairPattern = regexp.MustCompile(
		`s:\d+:"([^"]+)";s:\d+:"([^"]+)"`,
	)
	fallbackTemplatePattern = regexp.MustCompile(
		`"template_paths"[\w;:{]+"([^"]+)"`,
	)
	legacyTemplatePattern = regexp.MustCompile(
		`"template\.twig \(([^"]*\.html\.\w{2,4})\)"`,
	)
	formTypePattern = regexp.MustCompile(
		`type_class"[\w;:\\"{]+"value"[\w;:]+"([^"]+)"`,
	)
	frontControllerPattern = regexp.MustCompile(
		`/(?:app_[A-Za-z0-9_]{2,12}|index)\.php(?:/|$)`,
	)
)

type ProfilerCatalogProvider struct {
	root string
	php  *php.PHPIndex
	twig *twig.TwigIndexer
}

func NewProfilerCatalogProvider(
	root string,
	phpIndex *php.PHPIndex,
	twigIndex *twig.TwigIndexer,
) *ProfilerCatalogProvider {
	return &ProfilerCatalogProvider{
		root: filepath.Clean(root),
		php:  phpIndex,
		twig: twigIndex,
	}
}

type ProfilerRequestCatalogRequest struct {
	URL        string `json:"url,omitempty"`
	Hash       string `json:"hash,omitempty"`
	Controller string `json:"controller,omitempty"`
	Route      string `json:"route,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	IndexPath  string `json:"indexPath,omitempty"`
	BaseURL    string `json:"baseUrl,omitempty"`
}

type ProfilerRequestCatalogEntry struct {
	Hash              string                         `json:"hash"`
	Method            string                         `json:"method,omitempty"`
	URL               string                         `json:"url"`
	StatusCode        int                            `json:"statusCode,omitempty"`
	Timestamp         int64                          `json:"timestamp,omitempty"`
	ProfilerURL       string                         `json:"profilerUrl"`
	Controller        string                         `json:"controller,omitempty"`
	ControllerFileURI string                         `json:"controllerFileUri,omitempty"`
	ControllerLine    int                            `json:"controllerLine,omitempty"`
	Route             string                         `json:"route,omitempty"`
	EntryView         string                         `json:"entryView,omitempty"`
	StaticTemplates   []string                       `json:"staticTemplates,omitempty"`
	RenderedTemplates []string                       `json:"renderedTemplates,omitempty"`
	FormTypes         []string                       `json:"formTypes,omitempty"`
	MailMessages      []ProfilerMailMessage          `json:"mailMessages,omitempty"`
	TwigComponents    []ProfilerRuntimeTwigComponent `json:"twigComponents,omitempty"`
	IndexFileURI      string                         `json:"indexFileUri"`
}

type ProfilerMailMessage struct {
	Title string `json:"title"`
	Panel string `json:"panel"`
}

type ProfilerRuntimeTwigComponent struct {
	Name        string `json:"name"`
	Class       string `json:"class,omitempty"`
	Template    string `json:"template,omitempty"`
	RenderCount int    `json:"renderCount,omitempty"`
	FileURI     string `json:"fileUri,omitempty"`
	SourceLine  int    `json:"sourceLine,omitempty"`
}

func (p *ProfilerCatalogProvider) GetCommands(
	_ context.Context,
) map[string]lsp.CommandFunc {
	return map[string]lsp.CommandFunc{
		ListProfilerRequestsCommand: p.list,
	}
}

func (p *ProfilerCatalogProvider) list(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	var request ProfilerRequestCatalogRequest
	if raw != nil && len(*raw) != 0 && string(*raw) != "null" {
		if err := json.Unmarshal(*raw, &request); err != nil {
			return nil, fmt.Errorf(
				"invalid profiler request catalog request: %w",
				err,
			)
		}
	}
	return p.Catalog(ctx, request)
}

func (p *ProfilerCatalogProvider) Catalog(
	ctx context.Context,
	request ProfilerRequestCatalogRequest,
) ([]ProfilerRequestCatalogEntry, error) {
	if p == nil || p.root == "" {
		return nil, fmt.Errorf("symfony profiler request catalog is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	baseURL, err := normalizedProfilerBaseURL(request.BaseURL)
	if err != nil {
		return nil, err
	}
	indexPaths, err := p.profilerIndexPaths(request.IndexPath)
	if err != nil {
		return nil, err
	}
	if len(indexPaths) == 0 {
		return nil, fmt.Errorf(
			"no local Symfony profiler index was found under %s",
			p.root,
		)
	}
	rows, err := readProfilerRows(ctx, indexPaths)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf(
			"no profiler requests were found in the local Symfony profiler index",
		)
	}

	limit := request.Limit
	if limit <= 0 {
		limit = defaultProfilerRequestLimit
	}
	if limit > maxProfilerRequestLimit {
		limit = maxProfilerRequestLimit
	}
	urlFilter := strings.ToLower(strings.TrimSpace(request.URL))
	hashFilter := strings.ToLower(strings.TrimSpace(request.Hash))
	controllerFilter := strings.ToLower(strings.TrimSpace(request.Controller))
	routeFilter := strings.ToLower(strings.TrimSpace(request.Route))
	templateResolver := newRouteTemplateResolver(p.php, p.twig)
	lines := newSourceLineResolver()
	result := make([]ProfilerRequestCatalogEntry, 0, limit)
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, duplicate := seen[strings.ToLower(row.entry.Hash)]; duplicate {
			continue
		}
		seen[strings.ToLower(row.entry.Hash)] = struct{}{}
		if urlFilter != "" &&
			!strings.Contains(strings.ToLower(row.entry.URL), urlFilter) {
			continue
		}
		if hashFilter != "" &&
			!strings.Contains(strings.ToLower(row.entry.Hash), hashFilter) {
			continue
		}

		entry := row.entry
		entry.ProfilerURL = profilerURL(
			baseURL,
			entry.URL,
			entry.Hash,
		)
		content, contentErr := readProfilerContent(
			row.profilerDirectory,
			entry.Hash,
		)
		if contentErr != nil {
			// Profiler data is runtime state: files can disappear while the
			// index is being read, and one corrupt profile must not hide the
			// remaining request metadata.
			content = nil
		}
		if len(content) != 0 {
			entry.Controller = profilerSerializedString(
				content,
				"_controller",
			)
			entry.Route = profilerSerializedString(content, "_route")
			renderedTemplates := profilerRenderedTemplates(content)
			entry.EntryView = profilerEntryView(content, renderedTemplates)
			entry.RenderedTemplates = renderedTemplates
			if len(entry.RenderedTemplates) > 3 {
				entry.RenderedTemplates = entry.RenderedTemplates[:3]
			}
			entry.FormTypes = profilerFormTypes(content)
			entry.MailMessages = profilerMailMessages(content)
			entry.TwigComponents = profilerTwigComponents(content)
		}
		if controllerFilter != "" &&
			!strings.Contains(
				strings.ToLower(entry.Controller),
				controllerFilter,
			) {
			continue
		}
		if routeFilter != "" &&
			!strings.Contains(strings.ToLower(entry.Route), routeFilter) {
			continue
		}
		p.addControllerDetails(&entry, templateResolver, lines)
		p.addRuntimeTwigComponentDetails(&entry, lines)
		result = append(result, entry)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (p *ProfilerCatalogProvider) addRuntimeTwigComponentDetails(
	entry *ProfilerRequestCatalogEntry,
	lines *sourceLineResolver,
) {
	if p == nil || entry == nil {
		return
	}
	for index := range entry.TwigComponents {
		component := &entry.TwigComponents[index]
		if component.Class != "" && p.php != nil &&
			!strings.EqualFold(
				component.Class,
				"Symfony\\UX\\TwigComponent\\AnonymousComponent",
			) {
			if class, found := p.php.FindClass(component.Class); found {
				component.FileURI = uriutil.FileURI(class.Path)
				offset := class.SelectionRange.Start
				if class.SelectionRange.Len() == 0 {
					offset = class.Range.Start
				}
				component.SourceLine = lines.line(class.Path, offset)
				continue
			}
		}
		if component.Template == "" || p.twig == nil {
			continue
		}
		files, err := p.twig.GetTwigFilesByRelPath(component.Template)
		if err != nil || len(files) == 0 {
			continue
		}
		sort.Slice(files, func(left, right int) bool {
			return files[left].Path < files[right].Path
		})
		component.FileURI = uriutil.FileURI(files[0].Path)
		component.SourceLine = 1
	}
}

func (p *ProfilerCatalogProvider) addControllerDetails(
	entry *ProfilerRequestCatalogEntry,
	templates *routeTemplateResolver,
	lines *sourceLineResolver,
) {
	if p == nil || p.php == nil || entry == nil {
		return
	}
	className, methodName, found := splitProfilerController(
		entry.Controller,
	)
	if !found {
		return
	}
	methods := p.php.FindMethods(className, methodName)
	templateSet := make(map[string]struct{})
	for _, method := range methods {
		if entry.ControllerFileURI == "" && method.Path != "" {
			entry.ControllerFileURI = uriutil.FileURI(method.Path)
			offset := method.SelectionRange.Start
			if method.SelectionRange.Len() == 0 {
				offset = method.Range.Start
			}
			entry.ControllerLine = lines.line(method.Path, offset)
		}
		staticTemplates, err := templates.forMethod(method)
		if err != nil {
			continue
		}
		for _, template := range staticTemplates {
			if template != "" {
				templateSet[template] = struct{}{}
			}
		}
	}
	for template := range templateSet {
		entry.StaticTemplates = append(entry.StaticTemplates, template)
	}
	sort.Strings(entry.StaticTemplates)
}

func (p *ProfilerCatalogProvider) profilerIndexPaths(
	override string,
) ([]string, error) {
	if strings.TrimSpace(override) != "" {
		path := strings.TrimSpace(override)
		if !filepath.IsAbs(path) {
			path = filepath.Join(p.root, path)
		}
		path, err := secureWorkspacePath(p.root, path)
		if err != nil {
			return nil, fmt.Errorf("invalid profiler index path: %w", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("read profiler index %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("profiler index %s is not a file", path)
		}
		return []string{path}, nil
	}

	var paths []string
	for _, pattern := range []string{
		filepath.Join(p.root, "var", "cache", "*", "profiler", "index.csv"),
		filepath.Join(p.root, "app", "cache", "*", "profiler", "index.csv"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("discover profiler indexes: %w", err)
		}
		for _, match := range matches {
			path, secureErr := secureWorkspacePath(p.root, match)
			if secureErr != nil {
				return nil, fmt.Errorf(
					"invalid discovered profiler index path: %w",
					secureErr,
				)
			}
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

type profilerRow struct {
	entry             ProfilerRequestCatalogEntry
	profilerDirectory string
	indexModified     int64
	rowNumber         int
}

func readProfilerRows(
	ctx context.Context,
	indexPaths []string,
) ([]profilerRow, error) {
	var rows []profilerRow
	for _, indexPath := range indexPaths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := os.Stat(indexPath)
		if err != nil {
			return nil, fmt.Errorf("read profiler index %s: %w", indexPath, err)
		}
		if info.Size() > maxProfilerIndexBytes {
			return nil, fmt.Errorf(
				"profiler index %s exceeds the %d MiB limit",
				indexPath,
				maxProfilerIndexBytes>>20,
			)
		}
		file, err := os.Open(indexPath)
		if err != nil {
			return nil, fmt.Errorf("open profiler index %s: %w", indexPath, err)
		}
		scanner := bufio.NewScanner(io.LimitReader(
			file,
			maxProfilerIndexBytes+1,
		))
		scanner.Buffer(make([]byte, 64<<10), maxProfilerIndexRowBytes)
		recent := make([]profilerRow, maxProfilerScanRows)
		validRows := 0
		rowNumber := 0
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			currentRow := rowNumber
			rowNumber++
			if line == "" {
				continue
			}
			reader := csv.NewReader(strings.NewReader(line))
			reader.FieldsPerRecord = -1
			record, parseErr := reader.Read()
			if parseErr != nil || len(record) < 4 {
				continue
			}
			hash := strings.TrimPrefix(
				strings.TrimSpace(record[0]),
				"\uFEFF",
			)
			if !profilerHashPattern.MatchString(hash) {
				continue
			}
			entry := ProfilerRequestCatalogEntry{
				Hash:         hash,
				Method:       strings.TrimSpace(record[2]),
				URL:          strings.TrimSpace(record[3]),
				IndexFileURI: uriutil.FileURI(indexPath),
			}
			if len(record) > 4 {
				entry.Timestamp, _ = strconv.ParseInt(
					strings.TrimSpace(record[4]),
					10,
					64,
				)
			}
			if len(record) > 6 {
				entry.StatusCode, _ = strconv.Atoi(
					strings.TrimSpace(record[6]),
				)
			}
			recent[validRows%maxProfilerScanRows] = profilerRow{
				entry:             entry,
				profilerDirectory: filepath.Dir(indexPath),
				indexModified:     info.ModTime().UnixNano(),
				rowNumber:         currentRow,
			}
			validRows++
		}
		scanErr := scanner.Err()
		closeErr := file.Close()
		if scanErr != nil {
			return nil, fmt.Errorf(
				"read profiler index %s: %w",
				indexPath,
				scanErr,
			)
		}
		if closeErr != nil {
			return nil, fmt.Errorf(
				"close profiler index %s: %w",
				indexPath,
				closeErr,
			)
		}
		if validRows <= maxProfilerScanRows {
			rows = append(rows, recent[:validRows]...)
			continue
		}
		start := validRows % maxProfilerScanRows
		rows = append(rows, recent[start:]...)
		rows = append(rows, recent[:start]...)
	}
	sort.SliceStable(rows, func(left, right int) bool {
		if rows[left].entry.Timestamp != rows[right].entry.Timestamp {
			return rows[left].entry.Timestamp >
				rows[right].entry.Timestamp
		}
		if rows[left].indexModified != rows[right].indexModified {
			return rows[left].indexModified > rows[right].indexModified
		}
		if rows[left].entry.IndexFileURI !=
			rows[right].entry.IndexFileURI {
			return rows[left].entry.IndexFileURI <
				rows[right].entry.IndexFileURI
		}
		return rows[left].rowNumber > rows[right].rowNumber
	})
	return rows, nil
}

func readProfilerContent(directory, hash string) ([]byte, error) {
	if !profilerHashPattern.MatchString(hash) {
		return nil, nil
	}
	cleanDirectory, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve profiler directory: %w", err)
	}
	profilePath := filepath.Join(
		cleanDirectory,
		hash[4:6],
		hash[2:4],
		hash,
	)
	profilePath, err = secureWorkspacePath(cleanDirectory, profilePath)
	if err != nil {
		return nil, fmt.Errorf("invalid profiler data path: %w", err)
	}
	info, err := os.Stat(profilePath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("profiler data %s is not a file", profilePath)
	}
	if info.Size() > maxProfilerContentBytes {
		return nil, fmt.Errorf(
			"profiler data %s exceeds the %d MiB limit",
			profilePath,
			maxProfilerContentBytes>>20,
		)
	}
	file, err := os.Open(profilePath)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()
	var reader io.Reader = file
	header := make([]byte, 3)
	count, readErr := io.ReadFull(file, header)
	if readErr != nil &&
		!errors.Is(readErr, io.ErrUnexpectedEOF) &&
		!errors.Is(readErr, io.EOF) {
		return nil, fmt.Errorf("read profiler data %s: %w", profilePath, readErr)
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind profiler data %s: %w", profilePath, err)
	}
	if count == 3 && bytes.Equal(header, []byte{0x1f, 0x8b, 0x08}) {
		gzipReader, gzipErr := gzip.NewReader(file)
		if gzipErr != nil {
			return nil, fmt.Errorf(
				"decompress profiler data %s: %w",
				profilePath,
				gzipErr,
			)
		}
		defer func() {
			_ = gzipReader.Close()
		}()
		reader = gzipReader
	}
	content, err := io.ReadAll(io.LimitReader(
		reader,
		maxProfilerContentBytes+1,
	))
	if err != nil {
		return nil, fmt.Errorf("read profiler data %s: %w", profilePath, err)
	}
	if len(content) > maxProfilerContentBytes {
		return nil, fmt.Errorf(
			"profiler data %s expands beyond the %d MiB limit",
			profilePath,
			maxProfilerContentBytes>>20,
		)
	}
	return content, nil
}

func profilerSerializedString(content []byte, key string) string {
	marker := []byte(key + `";s:`)
	offset := bytes.Index(content, marker)
	if offset < 0 {
		return ""
	}
	offset += len(marker)
	lengthStart := offset
	for offset < len(content) &&
		content[offset] >= '0' &&
		content[offset] <= '9' {
		offset++
	}
	if offset == lengthStart || offset+2 > len(content) ||
		content[offset] != ':' || content[offset+1] != '"' {
		return ""
	}
	length, err := strconv.Atoi(string(content[lengthStart:offset]))
	if err != nil || length < 0 {
		return ""
	}
	start := offset + 2
	end := start + length
	if end > len(content) {
		return ""
	}
	return string(content[start:end])
}

func profilerRenderedTemplates(content []byte) []string {
	segment := content
	if offset := bytes.Index(content, []byte(`"template_paths"`)); offset >= 0 {
		segment = content[offset:]
		if end := bytes.IndexByte(segment, 0); end >= 0 {
			segment = segment[:end]
		}
		var result []string
		seen := make(map[string]struct{})
		for _, match := range templatePairPattern.FindAllSubmatch(
			segment,
			-1,
		) {
			template := strings.TrimSpace(string(match[1]))
			path := strings.TrimSpace(string(match[2]))
			if template == "" || path == "" {
				continue
			}
			if _, duplicate := seen[template]; duplicate {
				continue
			}
			seen[template] = struct{}{}
			result = append(result, template)
		}
		if len(result) != 0 {
			return result
		}
	}
	var result []string
	for _, match := range fallbackTemplatePattern.FindAllSubmatch(
		content,
		-1,
	) {
		template := strings.TrimSpace(string(match[1]))
		if template != "" {
			result = append(result, template)
		}
	}
	if len(result) != 0 {
		return uniqueProfilerStrings(result)
	}
	if match := legacyTemplatePattern.FindSubmatch(content); len(match) == 2 {
		return []string{string(match[1])}
	}
	return nil
}

func profilerEntryView(content []byte, templates []string) string {
	for _, template := range templates {
		if !strings.HasPrefix(template, "@WebProfiler") {
			return template
		}
	}
	if len(templates) != 0 {
		return templates[0]
	}
	if match := legacyTemplatePattern.FindSubmatch(content); len(match) == 2 {
		return string(match[1])
	}
	return ""
}

func profilerFormTypes(content []byte) []string {
	collectorOffset := bytes.Index(content, []byte(`\FormDataCollector"`))
	if collectorOffset < 0 {
		return nil
	}
	forms := content[collectorOffset:]
	formsOffset := bytes.Index(forms, []byte(`"forms"`))
	if formsOffset < 0 {
		return nil
	}
	forms = forms[formsOffset:]
	if end := bytes.IndexByte(forms, 0); end >= 0 {
		forms = forms[:end]
	}
	match := formTypePattern.FindSubmatch(forms)
	if len(match) != 2 {
		return nil
	}
	formType := strings.TrimSpace(string(match[1]))
	if formType == "" || strings.EqualFold(
		formType,
		`Symfony\Component\Form\Extension\Core\Type\FormType`,
	) {
		return nil
	}
	return []string{formType}
}

func profilerMailMessages(content []byte) []ProfilerMailMessage {
	const maxMailMessages = 25
	nameMarker := []byte("AbstractHeader\x00name\";s:")
	result := make([]ProfilerMailMessage, 0)
	seen := make(map[string]struct{})
	for offset := 0; offset < len(content) && len(result) < maxMailMessages; {
		relative := bytes.Index(content[offset:], nameMarker)
		if relative < 0 {
			break
		}
		start := offset + relative + len(nameMarker)
		name, end, found := profilerLengthPrefixedString(content, start)
		if !found {
			offset = start
			continue
		}
		offset = end
		if !strings.EqualFold(strings.TrimSpace(name), "Subject") {
			continue
		}
		header := content[end:]
		if closing := bytes.IndexByte(header, '}'); closing >= 0 {
			header = header[:closing]
		}
		title := strings.TrimSpace(
			profilerSerializedString(
				header,
				"UnstructuredHeader\x00value",
			),
		)
		if title == "" {
			continue
		}
		title = truncateProfilerMailTitle(title)
		key := strings.ToLower(title) + "\x00mailer"
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, ProfilerMailMessage{
			Title: title,
			Panel: "mailer",
		})
	}
	return result
}

func profilerLengthPrefixedString(
	content []byte,
	offset int,
) (string, int, bool) {
	lengthStart := offset
	for offset < len(content) &&
		content[offset] >= '0' &&
		content[offset] <= '9' {
		offset++
	}
	if offset == lengthStart || offset+2 > len(content) ||
		content[offset] != ':' || content[offset+1] != '"' {
		return "", lengthStart, false
	}
	length, err := strconv.Atoi(string(content[lengthStart:offset]))
	if err != nil || length < 0 {
		return "", lengthStart, false
	}
	start := offset + 2
	end := start + length
	if end+2 > len(content) ||
		content[end] != '"' || content[end+1] != ';' {
		return "", lengthStart, false
	}
	return string(content[start:end]), end + 2, true
}

func truncateProfilerMailTitle(title string) string {
	const maxTitleRunes = 512
	runes := []rune(title)
	if len(runes) <= maxTitleRunes {
		return title
	}
	return string(runes[:maxTitleRunes])
}

func splitProfilerController(controller string) (string, string, bool) {
	controller = strings.TrimSpace(strings.TrimPrefix(controller, `\`))
	separator := strings.LastIndex(controller, "::")
	if separator <= 0 || separator+2 >= len(controller) {
		return "", "", false
	}
	className := strings.TrimSpace(controller[:separator])
	methodName := strings.TrimSpace(controller[separator+2:])
	if className == "" || methodName == "" {
		return "", "", false
	}
	return className, methodName, true
}

func normalizedProfilerBaseURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("profiler baseUrl must be an absolute HTTP(S) URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("profiler baseUrl must use HTTP or HTTPS")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("profiler baseUrl must not contain credentials")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	return parsed.String(), nil
}

func profilerURL(baseURL, requestURL, hash string) string {
	if baseURL != "" {
		return baseURL + "/_profiler/" + hash
	}
	parsed, err := url.Parse(requestURL)
	if err != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" {
		return "_profiler/" + hash
	}
	pathPrefix := ""
	if location := frontControllerPattern.FindStringIndex(parsed.Path); location != nil {
		end := location[1]
		if strings.HasSuffix(parsed.Path[:end], "/") {
			end--
		}
		pathPrefix = parsed.Path[:end]
	}
	parsed.Path = strings.TrimRight(pathPrefix, "/") + "/_profiler/" + hash
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func secureWorkspacePath(root, candidate string) (string, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absoluteCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	lexicalCandidate := absoluteCandidate
	relative, err := filepath.Rel(absoluteRoot, absoluteCandidate)
	if err != nil {
		return "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %s is outside workspace %s", candidate, root)
	}
	if evaluatedRoot, evalErr := filepath.EvalSymlinks(absoluteRoot); evalErr == nil {
		if evaluatedCandidate, candidateErr := filepath.EvalSymlinks(
			absoluteCandidate,
		); candidateErr == nil {
			relative, err = filepath.Rel(evaluatedRoot, evaluatedCandidate)
			if err != nil {
				return "", err
			}
			if relative == ".." ||
				strings.HasPrefix(
					relative,
					".."+string(filepath.Separator),
				) {
				return "", fmt.Errorf(
					"path %s resolves outside workspace %s",
					candidate,
					root,
				)
			}
		}
	}
	return filepath.Clean(lexicalCandidate), nil
}

func uniqueProfilerStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

var _ lsp.CommandProvider = (*ProfilerCatalogProvider)(nil)
