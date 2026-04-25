package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	nurl "net/url"
	"strconv"
	"strings"
	"time"

	md "github.com/JohannesKaufmann/html-to-markdown"
	readability "github.com/go-shiori/go-readability"

	"github.com/devlin-ai/devlin/internal/logger"
)

type WebFetchTool struct{}

type webfetchParams struct {
	URL     string  `json:"url"`
	Format  string  `json:"format,omitempty"`
	Timeout float64 `json:"timeout,omitempty"`
}

type webfetchOutput struct {
	Title     string `json:"title"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated"`
}

const webfetchDescription = `- Fetches content from a specified URL
- Takes a URL and optional format as input
- Fetches the URL content, converts to requested format (markdown by default)
- Returns the content in the specified format
- Use this tool when you need to retrieve and analyze web content

Usage notes:
  - IMPORTANT: if another tool is present that offers better web fetching capabilities, is more targeted to the task, or has fewer restrictions, prefer using that tool instead of this one.
  - The URL must be a fully-formed valid URL
  - HTTP URLs will be automatically upgraded to HTTPS
  - Format options: "markdown" (default), "text", or "html"
  - This tool is read-only and does not modify any files
  - Results may be summarized if the content is very large`

const webfetchParameters = `{
	"type": "object",
	"properties": {
		"url": {
			"type": "string",
			"description": "The URL to fetch content from"
		},
		"format": {
			"type": "string",
			"enum": ["text", "markdown", "html"],
			"description": "The format to return the content in (text, markdown, or html). Defaults to markdown."
		},
		"timeout": {
			"type": "number",
			"description": "Optional timeout in seconds (max 120)"
		}
	},
	"required": ["url"]
}`

const (
	maxFetchBytes       = 10 * 1024 * 1024
	maxOutputChars      = 100_000
	defaultFetchTimeout = 30
	maxFetchTimeout     = 120
	maxURLLength        = 2000
)

func (WebFetchTool) Name() string        { return "webfetch" }
func (WebFetchTool) Description() string { return webfetchDescription }
func (WebFetchTool) Parameters() json.RawMessage {
	return json.RawMessage(webfetchParameters)
}

func (WebFetchTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params webfetchParams
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	if err := validateFetchURL(params.URL); err != nil {
		return "", err
	}

	format := params.Format
	if format == "" {
		format = "markdown"
	}

	timeoutSecs := defaultFetchTimeout
	if params.Timeout > 0 {
		timeoutSecs = int(params.Timeout)
		if timeoutSecs > maxFetchTimeout {
			timeoutSecs = maxFetchTimeout
		}
	}

	fetchURL := params.URL
	if strings.HasPrefix(fetchURL, "http://") {
		fetchURL = "https://" + fetchURL[7:]
	}

	body, contentType, err := fetchURLContent(ctx, fetchURL, format, timeoutSecs)
	if err != nil {
		return "", err
	}

	result, truncated := convertContent(body, contentType, format)

	title := fmt.Sprintf("%s (%s)", fetchURL, contentType)

	out, err := json.Marshal(webfetchOutput{
		Title:     title,
		Output:    result,
		Truncated: truncated,
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func validateFetchURL(rawURL string) error {
	if len(rawURL) > maxURLLength {
		return fmt.Errorf("URL exceeds maximum length of %d characters", maxURLLength)
	}

	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return fmt.Errorf("URL must start with http:// or https://")
	}

	parsed, err := nurl.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if parsed.User != nil {
		return fmt.Errorf("URL must not contain username or password")
	}

	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("URL must have a hostname")
	}

	parts := strings.Split(host, ".")
	if len(parts) < 2 && host != "localhost" {
		return fmt.Errorf("URL hostname must have at least two parts")
	}
	return nil
}

func fetchURLContent(ctx context.Context, rawURL, format string, timeoutSecs int) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}

	acceptHeader := "*/*"
	switch format {
	case "markdown":
		acceptHeader = "text/markdown;q=1.0, text/x-markdown;q=0.9, text/plain;q=0.8, text/html;q=0.7, */*;q=0.1"
	case "text":
		acceptHeader = "text/plain;q=1.0, text/markdown;q=0.9, text/html;q=0.8, */*;q=0.1"
	case "html":
		acceptHeader = "text/html;q=1.0, application/xhtml+xml;q=0.9, text/plain;q=0.8, text/markdown;q=0.7, */*;q=0.1"
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")
	req.Header.Set("Accept", acceptHeader)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			if len(via) > 0 {
				prevHost := via[0].URL.Hostname()
				curHost := req.URL.Hostname()
				if prevHost != curHost {
					return http.ErrUseLastResponse
				}
			}
			return nil
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden && isCloudflareChallenge(resp) {
		return retryWithHonestUA(rawURL, acceptHeader, timeoutSecs)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if size, err := strconv.Atoi(cl); err == nil && size > maxFetchBytes {
			return nil, "", fmt.Errorf("response too large (exceeds %d MB limit)", maxFetchBytes/(1024*1024))
		}
	}

	limited := io.LimitReader(resp.Body, maxFetchBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", fmt.Errorf("read response: %w", err)
	}

	if len(body) > maxFetchBytes {
		return nil, "", fmt.Errorf("response too large (exceeds %d MB limit)", maxFetchBytes/(1024*1024))
	}

	contentType := resp.Header.Get("Content-Type")
	return body, contentType, nil
}

func isCloudflareChallenge(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	return resp.Header.Get("Cf-Mitigated") == "challenge"
}

func retryWithHonestUA(rawURL, acceptHeader string, timeoutSecs int) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", "devlin")
	req.Header.Set("Accept", acceptHeader)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			if len(via) > 0 {
				prevHost := via[0].URL.Hostname()
				curHost := req.URL.Hostname()
				if prevHost != curHost {
					return http.ErrUseLastResponse
				}
			}
			return nil
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetch URL (retry): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	limited := io.LimitReader(resp.Body, maxFetchBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", fmt.Errorf("read response (retry): %w", err)
	}

	if len(body) > maxFetchBytes {
		return nil, "", fmt.Errorf("response too large (exceeds %d MB limit)", maxFetchBytes/(1024*1024))
	}

	contentType := resp.Header.Get("Content-Type")
	return body, contentType, nil
}

func convertContent(body []byte, contentType, format string) (string, bool) {
	content := string(body)
	isHTML := strings.Contains(contentType, "text/html")

	truncated := false

	switch format {
	case "html":
		if len(content) > maxOutputChars {
			content = content[:maxOutputChars]
			truncated = true
		}
		return content, truncated

	case "text":
		if isHTML {
			content = htmlToText(content)
		}
		if len(content) > maxOutputChars {
			content = content[:maxOutputChars]
			truncated = true
		}
		return content, truncated

	case "markdown":
		if isHTML {
			content = htmlToMarkdown(content)
		}
		if len(content) > maxOutputChars {
			content = content[:maxOutputChars]
			truncated = true
		}
		return content, truncated

	default:
		if len(content) > maxOutputChars {
			content = content[:maxOutputChars]
			truncated = true
		}
		return content, truncated
	}
}

func htmlToMarkdown(htmlContent string) string {
	parsed, err := readability.FromReader(strings.NewReader(htmlContent), &nurl.URL{})
	if err == nil && parsed.TextContent != "" {
		converter := md.NewConverter("", true, nil)
		markdown, err := converter.ConvertString(parsed.TextContent)
		if err == nil {
			return markdown
		}
		logger.L().Warn("readability markdown conversion failed, falling back to full page", "error", err)
	}

	converter := md.NewConverter("", true, nil)
	markdown, err := converter.ConvertString(htmlContent)
	if err != nil {
		logger.L().Warn("full page markdown conversion failed, returning raw HTML", "error", err)
		return htmlContent
	}
	return markdown
}

func htmlToText(htmlContent string) string {
	parsed, err := readability.FromReader(strings.NewReader(htmlContent), &nurl.URL{})
	if err == nil && parsed.TextContent != "" {
		return parsed.TextContent
	}

	text := stripHTMLTags(htmlContent)
	return text
}

func stripHTMLTags(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	inTag := false
	for i := 0; i < len(s); i++ {
		if s[i] == '<' {
			inTag = true
			continue
		}
		if s[i] == '>' {
			inTag = false
			continue
		}
		if !inTag {
			out.WriteByte(s[i])
		}
	}
	return strings.TrimSpace(out.String())
}

func (WebFetchTool) Display(args, output string) ToolDisplay {
	var wp webfetchParams
	if err := json.Unmarshal([]byte(args), &wp); err != nil {
		return ToolDisplay{Title: "webfetch", Body: []string{output}}
	}

	var out webfetchOutput
	if err := json.Unmarshal([]byte(output), &out); err != nil {
		return ToolDisplay{Title: "webfetch", Body: []string{output}}
	}

	disp := ToolDisplay{Title: out.Title}
	if out.Output != "" {
		disp.Body = strings.Split(out.Output, "\n")
	}
	return disp
}

func (WebFetchTool) Core() bool { return false }
func (WebFetchTool) PromptSnippet() string {
	return "webfetch — Fetch and convert a URL to markdown/text/html."
}
func (WebFetchTool) PromptGuidelines() []string {
	return []string{
		"Use webfetch to retrieve web pages for analysis",
		"Defaults to markdown output. Prefer markdown over html for readability",
	}
}

func init() {
	Register(&WebFetchTool{})
}
