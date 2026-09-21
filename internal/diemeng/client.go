package diemeng

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "secrets", "diemeng", "minute-api.json")
}

// Load reads a startup snapshot. A missing file leaves local CSV operation available.
func Load(path string) (*Client, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("无法读取蝶梦私有配置")
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, errors.New("蝶梦私有配置 JSON 无效")
	}
	return NewClient(cfg)
}

type Client struct {
	cfg         Config
	http        *http.Client
	endpoints   map[string]Endpoint
	gate        chan struct{}
	next        time.Time
	interval    time.Duration
	timeout     time.Duration
	downloadDir string
}

func NewClient(cfg Config) (*Client, error) {
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || cfg.APIKey == "" {
		return nil, errors.New("蝶梦配置需提供 HTTP(S) base_url 和非空 api_key，URL 不得包含凭据或查询参数")
	}
	if u.Path == "" {
		cfg.BaseURL += "/api"
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // Diemeng uses direct access; Cloudflare has its own proxy process.
	c := &Client{cfg: cfg, http: &http.Client{Transport: transport, Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, endpoints: map[string]Endpoint{}, gate: make(chan struct{}, 1), interval: 1200 * time.Millisecond, timeout: 60 * time.Second, downloadDir: `H:\Download\go-stock-diemeng`}
	for _, e := range Catalog() {
		c.endpoints[e.Name] = e
	}
	return c, nil
}

type Result struct {
	Source   string          `json:"source"`
	Endpoint string          `json:"endpoint"`
	Response json.RawMessage `json:"response,omitempty"`
	Fields   []Field         `json:"fields,omitempty"`
	Download *Receipt        `json:"download,omitempty"`
}

type Receipt struct {
	Path    string `json:"path"`
	Bytes   int64  `json:"bytes"`
	SHA256  string `json:"sha256"`
	Summary string `json:"summary"`
}

func (c *Client) wait(ctx context.Context) error {
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.gate }()
	delay := time.Until(c.next)
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.next = time.Now().Add(c.interval)
	return nil
}

func (c *Client) Call(ctx context.Context, name string, arguments map[string]any) (*Result, error) {
	if c == nil {
		return nil, errors.New("蝶梦未配置：请填写本机私有配置文件后重启")
	}
	e, ok := c.endpoints[name]
	if !ok {
		return nil, errors.New("未知的蝶梦接口")
	}
	// Normalize Go callers and MCP inputs to the same schema-validation representation.
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return nil, errors.New("参数不能转换为 JSON")
	}
	var args map[string]any
	if err := json.Unmarshal(encoded, &args); err != nil {
		return nil, errors.New("参数 JSON 无效")
	}
	if err := e.validate(args); err != nil {
		return nil, fmt.Errorf("%s", c.redact(err.Error()))
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := c.wait(ctx); err != nil {
		return nil, errors.New("蝶梦请求等待超时或已取消")
	}
	address := c.cfg.BaseURL + strings.TrimPrefix(e.Path, "/api")
	var body io.Reader
	if e.Method == http.MethodGet {
		query := url.Values{}
		for key, value := range args {
			query.Set(key, fmt.Sprint(value))
		}
		if len(query) > 0 {
			address += "?" + query.Encode()
		}
	} else {
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, e.Method, address, body)
	if err != nil {
		return nil, errors.New("无法构造蝶梦请求")
	}
	req.Header.Set("apiKey", c.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	if e.Download {
		req.Header.Set("Accept-Encoding", "identity")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("蝶梦网络请求失败、超时或已取消")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("蝶梦 HTTP %d（鉴权/权限/额度或上游错误），未自动重试", resp.StatusCode)
	}
	reader := bufio.NewReader(resp.Body)
	if e.Download {
		magic, _ := reader.Peek(2)
		if bytes.Equal(magic, []byte{0x1f, 0x8b}) {
			return c.saveDownload(e, args, reader)
		}
		_, err := c.decodeJSON(e, reader)
		if err != nil {
			return nil, err
		}
		return nil, errors.New("蝶梦下载未返回 GZIP 文件")
	}
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return nil, errors.New("蝶梦压缩响应无效")
		}
		defer gz.Close()
		return c.decodeJSON(e, gz)
	}
	return c.decodeJSON(e, reader)
}

func (c *Client) redact(value string) string {
	return strings.ReplaceAll(value, c.cfg.APIKey, "[REDACTED]")
}

func (c *Client) decodeJSON(e Endpoint, reader io.Reader) (*Result, error) {
	const maxJSON = 64 << 20
	body, err := io.ReadAll(io.LimitReader(reader, maxJSON+1))
	if err != nil {
		return nil, errors.New("蝶梦响应读取失败")
	}
	if len(body) > maxJSON {
		return nil, errors.New("蝶梦单页 JSON 超过64MiB，请缩小 page_size 或查询范围")
	}
	var envelope map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, errors.New("蝶梦响应不是有效 JSON 对象")
	}
	if !json.Valid(body) {
		return nil, errors.New("蝶梦响应包含无效 JSON")
	}
	code := fmt.Sprint(envelope["code"])
	if code != "200" {
		return nil, fmt.Errorf("蝶梦业务错误 code=%s: %s", c.redact(code), c.redact(fmt.Sprint(envelope["msg"])))
	}
	if _, ok := envelope["data"]; !ok {
		return nil, errors.New("蝶梦响应缺少 data")
	}
	var clean func(any) any
	clean = func(v any) any {
		switch value := v.(type) {
		case string:
			return c.redact(value)
		case []any:
			for i := range value {
				value[i] = clean(value[i])
			}
			return value
		case map[string]any:
			out := make(map[string]any, len(value))
			for key, item := range value {
				out[c.redact(key)] = clean(item)
			}
			return out
		default:
			return v
		}
	}
	body, err = json.Marshal(clean(envelope))
	if err != nil {
		return nil, errors.New("蝶梦响应转换失败")
	}
	return &Result{Source: "diemeng", Endpoint: e.Path, Response: body, Fields: e.Fields}, nil
}

func (c *Client) saveDownload(e Endpoint, args map[string]any, reader io.Reader) (*Result, error) {
	// CreateTemp uses exclusive creation. Downloading is never a fallback or retry.
	err := os.MkdirAll(c.downloadDir, 0700)
	var f *os.File
	if err == nil {
		f, err = os.CreateTemp(c.downloadDir, "daily-"+args["date"].(string)+"-*.json.gz")
	}
	if err != nil {
		return nil, errors.New("无法创建蝶梦下载文件")
	}
	complete := false
	defer func() {
		f.Close()
		if !complete {
			os.Remove(f.Name())
		}
	}()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), reader)
	if err != nil {
		return nil, errors.New("蝶梦下载中断，未完成文件已清理")
	}
	if err := f.Close(); err != nil {
		return nil, errors.New("蝶梦下载文件关闭失败")
	}
	check, err := os.Open(f.Name())
	if err != nil {
		return nil, errors.New("无法验证下载文件")
	}
	gz, err := gzip.NewReader(check)
	if err == nil {
		err = validateDump(gz)
		gz.Close()
	}
	check.Close()
	if err != nil {
		return nil, errors.New("蝶梦下载文件校验失败，可能是压缩业务错误或不完整数据；文件已清理")
	}
	complete = true
	return &Result{Source: "diemeng", Endpoint: e.Path, Download: &Receipt{Path: f.Name(), Bytes: n, SHA256: hex.EncodeToString(hash.Sum(nil)), Summary: fmt.Sprintf("全市场 %s，level=%v，GZIP 文件已保存；未展开到对话。", args["date"], args["level"])}}, nil
}

func validateDump(reader io.Reader) error {
	decoder := json.NewDecoder(reader)
	first, err := decoder.Token()
	if err != nil {
		return err
	}
	if first != json.Delim('[') && first != json.Delim('{') {
		return errors.New("unexpected download root")
	}
	for decoder.More() {
		if first == json.Delim('{') {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			if key == "code" || key == "msg" || key == "data" {
				return errors.New("business envelope is not a market dump")
			}
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		value = bytes.TrimSpace(value)
		want := byte('{')
		if first == json.Delim('{') {
			want = '['
		}
		if len(value) == 0 || value[0] != want {
			return errors.New("invalid market record shape")
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("invalid trailing data or gzip checksum")
	}
	return nil
}
