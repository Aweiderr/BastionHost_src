package record

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Store 是录像封存后的共享位置。local 驱动不使用它，文件只留在本机。
type Store interface {
	// Publish 让其它节点能按 key 读到刚封存的本地文件。
	Publish(ctx context.Context, key, localPath string) error
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	// Find 按会话号在最近若干天里定位对象键。
	Find(ctx context.Context, sessionNo string, now time.Time) (string, error)
	URI(key string) string
}

// FSStore 把录像放在一个目录里。shared / nfs 驱动把这个目录挂到每台代理的同一路径。
type FSStore struct {
	Root   string
	Scheme string
}

func (s *FSStore) Publish(_ context.Context, key, localPath string) error {
	if s == nil || s.Root == "" {
		return fmt.Errorf("共享录像目录未配置")
	}
	dest := filepath.Join(s.Root, filepath.FromSlash(key))
	if samePath(dest, localPath) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	in, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func (s *FSStore) Open(_ context.Context, key string) (io.ReadCloser, error) {
	path := filepath.Join(s.Root, filepath.FromSlash(cleanKey(key)))
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("读取共享录像失败: %w", err)
	}
	return f, nil
}

func (s *FSStore) Find(_ context.Context, sessionNo string, now time.Time) (string, error) {
	if strings.ContainsAny(sessionNo, `/\`) || sessionNo == "" {
		return "", fmt.Errorf("会话号非法")
	}
	if now.IsZero() {
		now = time.Now()
	}
	for i := 0; i < 90; i++ {
		d := now.AddDate(0, 0, -i)
		key := d.Format("2006") + "/" + d.Format("01") + "/" + d.Format("02") + "/" + sessionNo + ".bsr"
		path := filepath.Join(s.Root, filepath.FromSlash(key))
		st, err := os.Stat(path)
		if err == nil && !st.IsDir() {
			return key, nil
		}
	}
	return "", fmt.Errorf("录像文件不存在")
}

func (s *FSStore) URI(key string) string {
	scheme := s.Scheme
	if scheme == "" {
		scheme = "shared"
	}
	return scheme + "://" + cleanKey(key)
}

func cleanKey(key string) string {
	key = strings.TrimPrefix(key, "/")
	key = strings.ReplaceAll(key, "\\", "/")
	return key
}

func samePath(a, b string) bool {
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return a == b
	}
	return aa == bb
}

// ReadSession 读出某个会话的录像字节。
func ReadSession(ctx context.Context, store Store, sessionNo string, now time.Time) (key string, raw []byte, err error) {
	if store == nil {
		return "", nil, fmt.Errorf("录像存储未配置")
	}
	key, err = store.Find(ctx, sessionNo, now)
	if err != nil {
		return "", nil, err
	}
	rc, err := store.Open(ctx, key)
	if err != nil {
		return key, nil, err
	}
	defer rc.Close()
	raw, err = io.ReadAll(rc)
	return key, raw, err
}
