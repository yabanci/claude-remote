package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	defaultAPIBase    = "https://api.telegram.org"
	requestTimeout    = 90 * time.Second
	defaultMaxRetries = 3
	maxRetryAfter     = 60 * time.Second
)

type Client struct {
	token      string
	apiBase    string
	httpClient *http.Client
	maxRetries int
	sleep      func(time.Duration)
}

type Option func(*Client)

func WithBaseURL(baseURL string) Option {
	return func(c *Client) { c.apiBase = baseURL }
}

func WithRetryPolicy(maxRetries int, sleep func(time.Duration)) Option {
	return func(c *Client) {
		c.maxRetries = maxRetries
		c.sleep = sleep
	}
}

func NewClient(token string, opts ...Option) *Client {
	c := &Client{
		token:      token,
		apiBase:    defaultAPIBase,
		httpClient: &http.Client{Timeout: requestTimeout},
		maxRetries: defaultMaxRetries,
		sleep:      time.Sleep,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (r apiResponse) retryAfter() time.Duration {
	if r.ErrorCode != http.StatusTooManyRequests {
		return 0
	}
	wait := time.Duration(r.Parameters.RetryAfter) * time.Second
	if wait <= 0 {
		wait = time.Second
	}
	if wait > maxRetryAfter {
		wait = maxRetryAfter
	}
	return wait
}

func (c *Client) call(ctx context.Context, method string, form url.Values) (json.RawMessage, error) {
	var lastErr error
	for attempt := 0; ; attempt++ {
		result, wait, err := c.callOnce(ctx, method, form)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if wait <= 0 || attempt >= c.maxRetries {
			return nil, lastErr
		}
		c.sleep(wait)
	}
}

func (c *Client) callOnce(ctx context.Context, method string, form url.Values) (json.RawMessage, time.Duration, error) {
	endpoint := fmt.Sprintf("%s/bot%s/%s", c.apiBase, c.token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return nil, 0, fmt.Errorf("build request for %s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return c.doWithRetryHint(req, method)
}

func (c *Client) do(req *http.Request, method string) (json.RawMessage, error) {
	result, _, err := c.doWithRetryHint(req, method)
	return result, err
}

func (c *Client) doWithRetryHint(req *http.Request, method string) (json.RawMessage, time.Duration, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("call %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read response body for %s: %w", method, err)
	}

	var parsed apiResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, 0, fmt.Errorf("decode response for %s: %w", method, err)
	}
	if !parsed.OK {
		return nil, parsed.retryAfter(), fmt.Errorf("telegram api %s failed: %s", method, parsed.Description)
	}
	return parsed.Result, 0, nil
}

func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	form := url.Values{
		"timeout": {strconv.Itoa(timeoutSec)},
	}
	if offset != 0 {
		form.Set("offset", strconv.FormatInt(offset, 10))
	}

	result, err := c.call(ctx, "getUpdates", form)
	if err != nil {
		return nil, err
	}

	var updates []Update
	if err := json.Unmarshal(result, &updates); err != nil {
		return nil, fmt.Errorf("decode getUpdates result: %w", err)
	}
	return updates, nil
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	form := url.Values{
		"chat_id": {strconv.FormatInt(chatID, 10)},
		"text":    {text},
	}
	_, err := c.call(ctx, "sendMessage", form)
	return err
}

func (c *Client) SetMyCommands(ctx context.Context, commands []BotCommand) error {
	encoded, err := json.Marshal(commands)
	if err != nil {
		return fmt.Errorf("marshal commands: %w", err)
	}
	form := url.Values{"commands": {string(encoded)}}
	_, err = c.call(ctx, "setMyCommands", form)
	return err
}

func (c *Client) GetFile(ctx context.Context, fileID string) (File, error) {
	result, err := c.call(ctx, "getFile", url.Values{"file_id": {fileID}})
	if err != nil {
		return File{}, err
	}

	var file File
	if err := json.Unmarshal(result, &file); err != nil {
		return File{}, fmt.Errorf("decode getFile result: %w", err)
	}
	return file, nil
}

func (c *Client) DownloadFile(ctx context.Context, filePath, destPath string) error {
	downloadURL := fmt.Sprintf("%s/file/bot%s/%s", c.apiBase, c.token, filePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("build download request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download file: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download file: unexpected status %s", resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("create download dir: %w", err)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create dest file: %w", err)
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("write dest file: %w", err)
	}
	return nil
}

func (c *Client) SendDocument(ctx context.Context, chatID int64, localPath string) error {
	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open file %s: %w", localPath, err)
	}
	defer func() { _ = file.Close() }()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	if err := writer.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
		return fmt.Errorf("write chat_id field: %w", err)
	}

	part, err := writer.CreateFormFile("document", filepath.Base(localPath))
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("copy file into form: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close multipart writer: %w", err)
	}

	endpoint := fmt.Sprintf("%s/bot%s/sendDocument", c.apiBase, c.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return fmt.Errorf("build sendDocument request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	_, err = c.do(req, "sendDocument")
	return err
}
