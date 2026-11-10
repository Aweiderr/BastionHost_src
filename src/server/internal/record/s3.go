package record

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// S3Config 是 S3 兼容对象存储的连接参数。密钥只来自环境变量，不入库、不写日志。
type S3Config struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	Prefix    string
	PathStyle bool
}

// S3Store 把封存后的录像上传到对象存储，回放时再取回。
type S3Store struct {
	cfg  S3Config
	http *http.Client
}

// NewS3Store 创建对象存储。Endpoint 需包含协议，例如 http://minio:9000。
func NewS3Store(cfg S3Config) (*S3Store, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" || strings.TrimSpace(cfg.Bucket) == "" {
		return nil, fmt.Errorf("对象存储需要 endpoint 与 bucket")
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	return &S3Store{cfg: cfg, http: &http.Client{Timeout: 60 * time.Second}}, nil
}

func (s *S3Store) Publish(ctx context.Context, key, localPath string) error {
	raw, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	return s.put(ctx, s.objectKey(key), raw)
}

func (s *S3Store) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	body, err := s.get(ctx, s.objectKey(key))
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (s *S3Store) Find(ctx context.Context, sessionNo string, now time.Time) (string, error) {
	if strings.ContainsAny(sessionNo, `/\`) || sessionNo == "" {
		return "", fmt.Errorf("会话号非法")
	}
	if now.IsZero() {
		now = time.Now()
	}
	var last error
	for i := 0; i < 90; i++ {
		d := now.AddDate(0, 0, -i)
		key := d.Format("2006") + "/" + d.Format("01") + "/" + d.Format("02") + "/" + sessionNo + ".bsr"
		_, err := s.get(ctx, s.objectKey(key))
		if err == nil {
			return key, nil
		}
		last = err
	}
	if last == nil {
		last = fmt.Errorf("录像文件不存在")
	}
	return "", last
}

func (s *S3Store) URI(key string) string {
	return "s3://" + s.cfg.Bucket + "/" + s.objectKey(key)
}

func (s *S3Store) objectKey(key string) string {
	key = cleanKey(key)
	prefix := strings.Trim(s.cfg.Prefix, "/")
	if prefix == "" {
		return key
	}
	return prefix + "/" + key
}

func (s *S3Store) put(ctx context.Context, key string, body []byte) error {
	u, err := s.objectURL(key)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if err := signS3(req, body, s.cfg); err != nil {
		return err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("上传录像失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("上传录像失败: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

func (s *S3Store) get(ctx context.Context, key string) ([]byte, error) {
	u, err := s.objectURL(key)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if err := signS3(req, nil, s.cfg); err != nil {
		return nil, err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载录像失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("录像文件不存在")
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("下载录像失败: HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (s *S3Store) objectURL(key string) (string, error) {
	base, err := url.Parse(strings.TrimRight(s.cfg.Endpoint, "/"))
	if err != nil {
		return "", fmt.Errorf("对象存储地址非法: %w", err)
	}
	key = strings.TrimLeft(key, "/")
	if s.cfg.PathStyle {
		base.Path = "/" + s.cfg.Bucket + "/" + escapeKey(key)
		return base.String(), nil
	}
	base.Host = s.cfg.Bucket + "." + base.Host
	base.Path = "/" + escapeKey(key)
	return base.String(), nil
}

func escapeKey(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func signS3(req *http.Request, payload []byte, cfg S3Config) error {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	short := now.Format("20060102")
	sum := sha256.Sum256(payload)
	payloadHash := hex.EncodeToString(sum[:])
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("Host", req.URL.Host)

	canonHeaders, signed := canonicalHeaders(req)
	canon := strings.Join([]string{
		req.Method,
		canonicalPath(req.URL),
		req.URL.RawQuery,
		canonHeaders,
		signed,
		payloadHash,
	}, "\n")
	scope := short + "/" + cfg.Region + "/s3/aws4_request"
	canonSum := sha256.Sum256([]byte(canon))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(canonSum[:])
	key := hmacSHA256([]byte("AWS4"+cfg.SecretKey), short)
	key = hmacSHA256(key, cfg.Region)
	key = hmacSHA256(key, "s3")
	key = hmacSHA256(key, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(key, toSign))
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		cfg.AccessKey, scope, signed, sig))
	return nil
}

func canonicalPath(u *url.URL) string {
	p := u.EscapedPath()
	if p == "" {
		return "/"
	}
	return p
}

func canonicalHeaders(req *http.Request) (string, string) {
	type pair struct{ k, v string }
	var list []pair
	for k, vs := range req.Header {
		lk := strings.ToLower(k)
		if lk != "host" && lk != "content-type" && !strings.HasPrefix(lk, "x-amz-") {
			continue
		}
		list = append(list, pair{lk, strings.TrimSpace(strings.Join(vs, ","))})
	}
	// 稳定顺序
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			if list[j].k < list[i].k {
				list[i], list[j] = list[j], list[i]
			}
		}
	}
	var b strings.Builder
	names := make([]string, 0, len(list))
	for _, p := range list {
		b.WriteString(p.k)
		b.WriteString(":")
		b.WriteString(p.v)
		b.WriteString("\n")
		names = append(names, p.k)
	}
	return b.String(), strings.Join(names, ";")
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte(data))
	return m.Sum(nil)
}
