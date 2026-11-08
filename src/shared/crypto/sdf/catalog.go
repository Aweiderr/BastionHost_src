package sdf

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"bastion.local/shared/crypto"
)

const (
	kindECC = "ecc"
	kindSM4 = "sm4"
)

// keyRec 只保存索引和 KEK 密文包。没有私钥标量，也没有 SM4 明文。
type keyRec struct {
	Meta       crypto.KeyMeta `json:"meta"`
	Kind       string         `json:"kind"`
	Index      uint32         `json:"index"`
	KEKIndex   uint32         `json:"kek_index,omitempty"`
	WrappedB64 string         `json:"wrapped,omitempty"`
	EncPubB64  string         `json:"enc_public,omitempty"`
}

type catalogFile struct {
	Version int                `json:"version"`
	Keys    map[string]*keyRec `json:"keys"`
}

type catalog struct {
	mu   sync.Mutex
	path string
	keys map[string]*keyRec
}

func loadCatalog(dir string) (*catalog, error) {
	path := filepath.Join(dir, "sdf", "catalog.json")
	c := &catalog{path: path, keys: map[string]*keyRec{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, fmt.Errorf("读取 SDF 密钥目录失败: %w", err)
	}
	var file catalogFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("解析 SDF 密钥目录失败: %w", err)
	}
	if file.Keys != nil {
		c.keys = file.Keys
	}
	return c, nil
}

func (c *catalog) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("创建 SDF 密钥目录失败: %w", err)
	}
	raw, err := json.MarshalIndent(catalogFile{Version: 1, Keys: c.keys}, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("写入 SDF 密钥目录失败: %w", err)
	}
	if err := os.Rename(tmp, c.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("提交 SDF 密钥目录失败: %w", err)
	}
	return nil
}

func (c *catalog) clone(id string) (*keyRec, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.keys[id]
	if !ok {
		return nil, false
	}
	cp := *rec
	return &cp, true
}

func (c *catalog) lookup(id string) (*keyRec, error) {
	rec, ok := c.clone(id)
	if !ok {
		return nil, errNotInCatalog
	}
	if rec.Meta.Status != crypto.KeyStatusActive {
		return nil, fmt.Errorf("%w: %s（状态 %s）", crypto.ErrKeyDisabled, id, rec.Meta.Status)
	}
	return rec, nil
}

func (c *catalog) get(id string) (*keyRec, bool) {
	return c.clone(id)
}

func (c *catalog) list() []*keyRec {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*keyRec, 0, len(c.keys))
	for _, rec := range c.keys {
		cp := *rec
		out = append(out, &cp)
	}
	return out
}

func (c *catalog) indexActive(kind string, index uint32) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, rec := range c.keys {
		if rec.Kind == kind && rec.Index == index && rec.Meta.Status == crypto.KeyStatusActive {
			return id, true
		}
	}
	return "", false
}

func (c *catalog) insert(rec *keyRec) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.keys[rec.Meta.KeyID]; ok {
		return fmt.Errorf("%w: 密钥 %s 已存在", errInvalid, rec.Meta.KeyID)
	}
	c.keys[rec.Meta.KeyID] = rec
	return c.saveLocked()
}

func (c *catalog) put(rec *keyRec) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys[rec.Meta.KeyID] = rec
	return c.saveLocked()
}
