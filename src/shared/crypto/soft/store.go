package soft

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bastion.local/shared/crypto"
)

// store 是软件实现的密钥库：每个密钥一个 JSON 文件，私钥材料用主密钥（SM4）加密。
//
// 目录结构：
//
//	<KeyStoreDir>/
//	    ├── master.key         主密钥（32 字节，首次运行自动生成，权限 0600）
//	    └── <keyID>.json       密钥记录（含加密后的私钥材料）
//
// 安全说明：主密钥用于加密保护私钥材料；生产环境应将主密钥文件放置在受限目录，
// 并考虑后续替换为密码机托管（切换 Provider=pkcs11 后本文件不再承载私钥）。
type store struct {
	dir        string
	masterFile string
	masterKey  []byte // 32 字节 SM4 密钥

	mu    sync.RWMutex
	cache map[string]*keyRecord
}

// keyRecord 是持久化的密钥记录。
type keyRecord struct {
	Meta crypto.KeyMeta `json:"meta"`
	// SecretCipher 是加密后的密钥材料（Base64）：
	//   - SM2：私钥标量
	//   - SM4/HMAC：对称密钥
	//   - SM2 公钥不加密，直接存在 Meta.PublicKey
	SecretCipher string `json:"secret_cipher,omitempty"`
	// SecretIV 是加密使用的随机 IV（Base64）
	SecretIV string `json:"secret_iv,omitempty"`
}

// newStore 打开或创建密钥库。
func newStore(dir, masterFile string) (*store, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: 密钥库目录未配置", crypto.ErrInvalidParam)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建密钥库目录失败: %w", err)
	}
	s := &store{dir: dir, masterFile: masterFile, cache: map[string]*keyRecord{}}
	if err := s.loadMasterKey(); err != nil {
		return nil, err
	}
	if err := s.loadAll(); err != nil {
		return nil, err
	}
	return s, nil
}

// loadMasterKey 加载主密钥；不存在则生成。
func (s *store) loadMasterKey() error {
	if s.masterFile == "" {
		s.masterFile = filepath.Join(s.dir, "master.key")
	}
	data, err := os.ReadFile(s.masterFile)
	if err == nil {
		if len(data) != SM4KeySize {
			return fmt.Errorf("主密钥文件长度非法（期望 %d 字节，实际 %d）: %s", SM4KeySize, len(data), s.masterFile)
		}
		s.masterKey = data
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("读取主密钥失败: %w", err)
	}
	// 首次运行：生成主密钥
	key := make([]byte, SM4KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return fmt.Errorf("生成主密钥失败: %w", err)
	}
	if err := os.WriteFile(s.masterFile, key, 0o600); err != nil {
		return fmt.Errorf("写入主密钥失败: %w", err)
	}
	s.masterKey = key
	return nil
}

// loadAll 加载全部密钥记录到缓存。
func (s *store) loadAll() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("读取密钥库目录失败: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(s.dir, e.Name())
		raw, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("读取密钥文件失败 %s: %w", p, err)
		}
		var rec keyRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return fmt.Errorf("解析密钥文件失败 %s: %w", p, err)
		}
		if rec.Meta.KeyID == "" {
			continue
		}
		s.cache[rec.Meta.KeyID] = &rec
	}
	return nil
}

// fileOf 返回密钥文件路径。
func (s *store) fileOf(keyID string) string {
	return filepath.Join(s.dir, keyID+".json")
}

// put 写入或更新密钥记录。
func (s *store) put(rec *keyRecord) error {
	rec.Meta.UpdateTime = time.Now()
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	// 原子写入：先写临时文件再改名，避免中途崩溃导致文件损坏
	tmp := s.fileOf(rec.Meta.KeyID) + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("写入密钥文件失败: %w", err)
	}
	if err := os.Rename(tmp, s.fileOf(rec.Meta.KeyID)); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("提交密钥文件失败: %w", err)
	}
	s.cache[rec.Meta.KeyID] = rec
	return nil
}

// get 读取密钥记录。
func (s *store) get(keyID string) (*keyRecord, error) {
	s.mu.RLock()
	rec, ok := s.cache[keyID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", crypto.ErrKeyNotFound, keyID)
	}
	if rec.Meta.Status != crypto.KeyStatusActive {
		return nil, fmt.Errorf("%w: %s（状态 %s）", crypto.ErrKeyDisabled, keyID, rec.Meta.Status)
	}
	return rec, nil
}

// list 列出全部密钥记录。
func (s *store) list() []*keyRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*keyRecord, 0, len(s.cache))
	for _, r := range s.cache {
		out = append(out, r)
	}
	return out
}

// protect 用主密钥加密密钥材料。
func (s *store) protect(plain []byte) (cipherB64, ivB64 string, err error) {
	iv, err := sm4RandomIV()
	if err != nil {
		return "", "", err
	}
	ct, err := sm4Encrypt(s.masterKey, plain, crypto.ModeCBC, iv)
	if err != nil {
		return "", "", err
	}
	return crypto.B64(ct), crypto.B64(iv), nil
}

// unprotect 用主密钥解密密钥材料。
func (s *store) unprotect(cipherB64, ivB64 string) ([]byte, error) {
	ct, err := crypto.B64Decode(cipherB64)
	if err != nil {
		return nil, fmt.Errorf("密钥材料解码失败: %w", err)
	}
	iv, err := crypto.B64Decode(ivB64)
	if err != nil {
		return nil, fmt.Errorf("密钥 IV 解码失败: %w", err)
	}
	return sm4Decrypt(s.masterKey, ct, crypto.ModeCBC, iv)
}
