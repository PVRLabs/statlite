// Package prometheus provides bounded, framework-neutral collection of
// Prometheus text and OpenMetrics exposition data.
package prometheus

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type Limits struct {
	MaxCompressedBytes   int64
	MaxDecompressedBytes int64
	MaxRedirects         int
	MaxSamples           int
	MaxLabelsPerSample   int
	MaxLabelBytes        int
	MaxAggregationStates int
}

var DefaultLimits = Limits{
	MaxCompressedBytes: 1 << 20, MaxDecompressedBytes: 4 << 20,
	MaxRedirects: 3, MaxSamples: 100_000, MaxLabelsPerSample: 32,
	MaxLabelBytes: 1024, MaxAggregationStates: 10_000,
}

func (l Limits) validate() error {
	if l.MaxCompressedBytes <= 0 || l.MaxDecompressedBytes <= 0 || l.MaxRedirects < 0 ||
		l.MaxSamples <= 0 || l.MaxLabelsPerSample < 0 || l.MaxLabelBytes <= 0 || l.MaxAggregationStates <= 0 {
		return errors.New("all Prometheus limits must be positive (redirects may be zero)")
	}
	return nil
}

type FailureClass string

const (
	FailureInvalidURL     FailureClass = "invalid_url"
	FailureTransport      FailureClass = "transport"
	FailureAuthentication FailureClass = "authentication"
	FailureNotFound       FailureClass = "not_found"
	FailureHTTP           FailureClass = "http"
	FailureContentType    FailureClass = "content_type"
	FailureUnsafe         FailureClass = "unsafe"
	FailureMalformed      FailureClass = "malformed"
)

type Error struct {
	Class      FailureClass
	StatusCode int
	Message    string
	Err        error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Err }

type BasicAuth struct{ Username, Password string }

type Client struct {
	httpClient *http.Client
	limits     Limits
	auth       *BasicAuth
}

func NewClient(timeout time.Duration, limits Limits, auth *BasicAuth) (*Client, error) {
	if timeout <= 0 {
		return nil, errors.New("Prometheus timeout must be positive")
	}
	if err := limits.validate(); err != nil {
		return nil, err
	}
	c := &Client{limits: limits, auth: auth}
	c.httpClient = &http.Client{Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > limits.MaxRedirects {
			return errors.New("Prometheus redirect limit exceeded")
		}
		if len(via) > 0 && !sameOrigin(req.URL, via[0].URL) {
			return errors.New("Prometheus redirects may not change origin")
		}
		if len(via) > 0 && c.auth != nil {
			req.SetBasicAuth(c.auth.Username, c.auth.Password)
		}
		return nil
	}}
	return c, nil
}

// Scrape fetches and parses one exposition response. The handler is called as
// samples are parsed; callers should retain only target-relevant state.
func (c *Client) Scrape(ctx context.Context, endpoint string, handler Handler) (Stats, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return Stats{}, failure(FailureInvalidURL, 0, "invalid Prometheus URL", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Stats{}, failure(FailureInvalidURL, 0, "creating Prometheus request", err)
	}
	req.Header.Set("Accept", "application/openmetrics-text; version=1.0.0; escaping=underscores; q=1.0, text/plain; version=0.0.4; escaping=underscores; q=0.9")
	req.Header.Set("Accept-Encoding", "gzip")
	if c.auth != nil {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Stats{}, failure(FailureTransport, 0, fmt.Sprintf("fetching Prometheus exposition: %v", err), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return Stats{}, failure(FailureAuthentication, resp.StatusCode, "Prometheus authentication failed", nil)
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return Stats{}, failure(FailureNotFound, resp.StatusCode, fmt.Sprintf("Prometheus endpoint returned HTTP %d", resp.StatusCode), nil)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Stats{}, failure(FailureHTTP, resp.StatusCode, fmt.Sprintf("Prometheus endpoint returned HTTP %d", resp.StatusCode), nil)
	}
	format, err := responseFormat(resp.Header.Get("Content-Type"))
	if err != nil {
		return Stats{}, failure(FailureContentType, resp.StatusCode, err.Error(), err)
	}
	var body io.Reader = resp.Body
	if encoding := strings.TrimSpace(strings.ToLower(resp.Header.Get("Content-Encoding"))); encoding != "" && encoding != "identity" {
		if encoding != "gzip" {
			return Stats{}, failure(FailureContentType, resp.StatusCode, "unsupported Prometheus content encoding", nil)
		}
		raw := &countingReader{r: resp.Body, max: c.limits.MaxCompressedBytes, name: "compressed response"}
		gz, gzipErr := gzip.NewReader(raw)
		if gzipErr != nil {
			class := FailureMalformed
			if errors.Is(gzipErr, errLimitExceeded) {
				class = FailureUnsafe
			}
			return Stats{}, failure(class, resp.StatusCode, "invalid gzip Prometheus response", gzipErr)
		}
		defer gz.Close()
		body = gz
	}
	body = &countingReader{r: body, max: c.limits.MaxDecompressedBytes, name: "decompressed response"}
	stats, err := Parse(body, format, c.limits, handler)
	if err != nil {
		return stats, err
	}
	return stats, nil
}

type Format string

const (
	TextFormat        Format = "text"
	OpenMetricsFormat Format = "openmetrics"
)

func responseFormat(value string) (Format, error) {
	mediaType, params, err := mime.ParseMediaType(value)
	if err != nil {
		return "", fmt.Errorf("invalid Prometheus content type: %w", err)
	}
	switch strings.ToLower(mediaType) {
	case "text/plain":
		if err := validateContentTypeParams(params, "0.0.4", false); err != nil {
			return "", err
		}
		return TextFormat, nil
	case "application/openmetrics-text":
		if err := validateContentTypeParams(params, "1.0.0", true); err != nil {
			return "", err
		}
		return OpenMetricsFormat, nil
	default:
		return "", fmt.Errorf("unsupported Prometheus content type %q", mediaType)
	}
}

func validateContentTypeParams(params map[string]string, version string, requireVersion bool) error {
	for name := range params {
		if name != "version" && name != "charset" && name != "escaping" {
			return fmt.Errorf("unsupported exposition content-type parameter %q", name)
		}
	}
	if got, ok := params["version"]; (requireVersion && !ok) || (ok && got != version) {
		return fmt.Errorf("unsupported exposition version %q (want %s)", got, version)
	}
	if charset, ok := params["charset"]; ok && !strings.EqualFold(charset, "utf-8") {
		return fmt.Errorf("unsupported exposition charset %q", charset)
	}
	if escaping, ok := params["escaping"]; ok && escaping != "underscores" {
		return fmt.Errorf("unsupported exposition escaping %q", escaping)
	}
	return nil
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Hostname(), b.Hostname()) && effectivePort(a) == effectivePort(b)
}

func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}

type Label struct{ Name, Value string }
type Sample struct {
	Name   string
	Labels []Label
	Value  float64
}
type Handler func(Sample) error
type Stats struct{ Samples int }

func Parse(r io.Reader, format Format, limits Limits, handler Handler) (Stats, error) {
	if err := limits.validate(); err != nil {
		return Stats{}, err
	}
	if format != TextFormat && format != OpenMetricsFormat {
		return Stats{}, failure(FailureContentType, 0, "unsupported exposition format", nil)
	}
	tracked := &finalByteReader{r: r}
	scanner := bufio.NewScanner(tracked)
	scanner.Buffer(make([]byte, 64*1024), int(limits.MaxDecompressedBytes)+1)
	stats := Stats{}
	sawEOF := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if sawEOF {
			return stats, failure(FailureMalformed, 0, "exposition contains data after # EOF", nil)
		}
		if line == "# EOF" {
			sawEOF = true
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		sample, err := parseSample(line, format, limits)
		if err != nil {
			class := FailureMalformed
			if errors.Is(err, errLimitExceeded) {
				class = FailureUnsafe
			}
			return stats, failure(class, 0, fmt.Sprintf("invalid exposition sample: %v", err), err)
		}
		stats.Samples++
		if stats.Samples > limits.MaxSamples {
			return stats, failure(FailureUnsafe, 0, "Prometheus parsed-sample limit exceeded", nil)
		}
		if handler != nil {
			if err := handler(sample); err != nil {
				return stats, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		class := FailureMalformed
		if errors.Is(err, errLimitExceeded) {
			class = FailureUnsafe
		}
		return stats, failure(class, 0, fmt.Sprintf("reading Prometheus exposition: %v", err), err)
	}
	if format == TextFormat && tracked.readAny && tracked.last != '\n' {
		return stats, failure(FailureMalformed, 0, "Prometheus exposition is not terminated by LF", nil)
	}
	if format == OpenMetricsFormat && !sawEOF {
		return stats, failure(FailureMalformed, 0, "OpenMetrics exposition is missing # EOF", nil)
	}
	return stats, nil
}

func parseSample(line string, format Format, limits Limits) (Sample, error) {
	nameEnd := strings.IndexAny(line, "{ \t")
	if nameEnd < 0 {
		return Sample{}, errors.New("missing sample value")
	}
	name := line[:nameEnd]
	if !validMetricName(name) {
		return Sample{}, fmt.Errorf("invalid metric name %q", name)
	}
	rest := strings.TrimSpace(line[nameEnd:])
	labels := []Label(nil)
	if strings.HasPrefix(rest, "{") {
		end, err := findLabelEnd(rest)
		if err != nil {
			return Sample{}, err
		}
		labels, err = parseLabels(rest[1:end], limits)
		if err != nil {
			return Sample{}, err
		}
		rest = strings.TrimSpace(rest[end+1:])
	}
	if marker := strings.Index(rest, " # "); marker >= 0 {
		if format != OpenMetricsFormat {
			return Sample{}, errors.New("exemplars are not supported in Prometheus text format")
		}
		if err := validateExemplar(strings.TrimSpace(rest[marker+3:]), format, limits); err != nil {
			return Sample{}, err
		}
		rest = strings.TrimSpace(rest[:marker])
	}
	fields := strings.Fields(rest)
	if len(fields) < 1 || len(fields) > 2 {
		return Sample{}, errors.New("sample must have a value and optional timestamp")
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return Sample{}, fmt.Errorf("invalid sample value %q", fields[0])
	}
	if len(fields) == 2 {
		if err := validateTimestamp(fields[1], format); err != nil {
			return Sample{}, errors.New("invalid sample timestamp")
		}
	}
	return Sample{Name: name, Labels: labels, Value: value}, nil
}

func validateExemplar(s string, format Format, limits Limits) error {
	if !strings.HasPrefix(s, "{") {
		return errors.New("invalid exemplar")
	}
	end, err := findLabelEnd(s)
	if err != nil {
		return fmt.Errorf("invalid exemplar: %w", err)
	}
	if _, err := parseLabels(s[1:end], limits); err != nil {
		return fmt.Errorf("invalid exemplar: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(s[end+1:]))
	if len(fields) < 1 || len(fields) > 2 {
		return errors.New("invalid exemplar value or timestamp")
	}
	if _, err := strconv.ParseFloat(fields[0], 64); err != nil {
		return errors.New("invalid exemplar value")
	}
	if len(fields) == 2 {
		if err := validateTimestamp(fields[1], format); err != nil {
			return errors.New("invalid exemplar timestamp")
		}
	}
	return nil
}

func validateTimestamp(value string, format Format) error {
	if format == TextFormat {
		_, err := strconv.ParseInt(value, 10, 64)
		return err
	}
	timestamp, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsInf(timestamp, 0) || math.IsNaN(timestamp) {
		return errors.New("invalid OpenMetrics timestamp")
	}
	return nil
}

func findLabelEnd(s string) (int, error) {
	escaped, quoted := false, false
	for i := 1; i < len(s); i++ {
		switch {
		case escaped:
			escaped = false
		case s[i] == '\\' && quoted:
			escaped = true
		case s[i] == '"':
			quoted = !quoted
		case s[i] == '}' && !quoted:
			return i, nil
		}
	}
	return 0, errors.New("unterminated label set")
}

func parseLabels(s string, limits Limits) ([]Label, error) {
	labels := make([]Label, 0, min(4, limits.MaxLabelsPerSample))
	seen := make(map[string]struct{})
	for strings.TrimSpace(s) != "" {
		s = strings.TrimSpace(s)
		eq := strings.IndexByte(s, '=')
		if eq <= 0 {
			return nil, errors.New("invalid label")
		}
		name := strings.TrimSpace(s[:eq])
		if !validLabelName(name) {
			return nil, fmt.Errorf("invalid label name %q", name)
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("duplicate label name %q", name)
		}
		seen[name] = struct{}{}
		s = strings.TrimSpace(s[eq+1:])
		if !strings.HasPrefix(s, "\"") {
			return nil, errors.New("label value must be quoted")
		}
		value, consumed, err := quotedValue(s)
		if err != nil {
			return nil, err
		}
		if len(name)+len(value) > limits.MaxLabelBytes {
			return nil, fmt.Errorf("Prometheus label-size limit exceeded: %w", errLimitExceeded)
		}
		labels = append(labels, Label{Name: name, Value: value})
		if len(labels) > limits.MaxLabelsPerSample {
			return nil, fmt.Errorf("Prometheus labels-per-sample limit exceeded: %w", errLimitExceeded)
		}
		s = strings.TrimSpace(s[consumed:])
		if s == "" {
			break
		}
		if s[0] != ',' {
			return nil, errors.New("labels must be comma-separated")
		}
		s = s[1:]
	}
	return labels, nil
}

func quotedValue(s string) (string, int, error) {
	var value strings.Builder
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' {
			if i+1 >= len(s) {
				return "", 0, errors.New("unterminated label escape")
			}
			i++
			switch s[i] {
			case '\\', '"':
				value.WriteByte(s[i])
			case 'n':
				value.WriteByte('\n')
			default:
				return "", 0, errors.New("invalid label escape")
			}
			continue
		}
		if s[i] == '"' {
			if !utf8.ValidString(value.String()) {
				return "", 0, errors.New("invalid escaped label value")
			}
			return value.String(), i + 1, nil
		}
		value.WriteByte(s[i])
	}
	return "", 0, errors.New("unterminated label value")
}

func validMetricName(s string) bool { return validName(s, true) }
func validLabelName(s string) bool  { return validName(s, false) }
func validName(s string, colon bool) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (colon && r == ':') || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

var errLimitExceeded = errors.New("size limit exceeded")

type finalByteReader struct {
	r       io.Reader
	readAny bool
	last    byte
}

func (r *finalByteReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.readAny = true
		r.last = p[n-1]
	}
	return n, err
}

type countingReader struct {
	r      io.Reader
	n, max int64
	name   string
}

func (r *countingReader) Read(p []byte) (int, error) {
	remaining := r.max + 1 - r.n
	if remaining <= 0 {
		return 0, fmt.Errorf("%s: %w", r.name, errLimitExceeded)
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := r.r.Read(p)
	r.n += int64(n)
	if r.n > r.max {
		return n, fmt.Errorf("%s limit exceeded: %w", r.name, errLimitExceeded)
	}
	return n, err
}
func failure(class FailureClass, status int, message string, err error) error {
	return &Error{Class: class, StatusCode: status, Message: message, Err: err}
}

// Accumulator bounds aggregation state while allowing target-owned selection
// and grouping. It intentionally has no knowledge of metric families or labels.
type Accumulator struct {
	max    int
	values map[string]Aggregate
}
type Aggregate struct {
	Sum   float64
	Count uint64
}

func NewAccumulator(maxStates int) (*Accumulator, error) {
	if maxStates <= 0 {
		return nil, errors.New("aggregation-state limit must be positive")
	}
	return &Accumulator{max: maxStates, values: make(map[string]Aggregate)}, nil
}
func (a *Accumulator) Add(key string, value float64) error {
	v, ok := a.values[key]
	if !ok && len(a.values) >= a.max {
		return failure(FailureUnsafe, 0, "Prometheus retained-aggregation-state limit exceeded", nil)
	}
	v.Sum += value
	v.Count++
	a.values[key] = v
	return nil
}
func (a *Accumulator) Get(key string) (Aggregate, bool) { v, ok := a.values[key]; return v, ok }
func (a *Accumulator) Len() int                         { return len(a.values) }
