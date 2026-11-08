package sdf

import (
	"context"
	stdcrypto "crypto"
	"errors"
	"fmt"
	"strings"
	"time"

	"bastion.local/shared/crypto"
)

func (p *Provider) KeyGenerate(ctx context.Context, req crypto.KeyGenRequest) (*crypto.KeyMeta, error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	if req.KeyType == "" || req.Usage == "" {
		return nil, fmt.Errorf("%w: 密钥类型与用途不能为空", errInvalid)
	}
	switch req.KeyType {
	case crypto.KeyTypeRSA, crypto.KeyTypeHMAC:
		// RSA 兼容链与 HMAC 仍由软件模块托管。本版 SDF 路径不把这两类材料写入密码机。
		return p.legacy.KeyGenerate(ctx, req)
	case crypto.KeyTypeSM2:
		return p.generateSM2(req)
	case crypto.KeyTypeSM4:
		return p.generateSM4(req)
	default:
		return nil, fmt.Errorf("%w: 不支持的密钥类型 %s", errInvalid, req.KeyType)
	}
}

func (p *Provider) generateSM2(req crypto.KeyGenRequest) (*crypto.KeyMeta, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	idx, err := p.allocSM2()
	if err != nil {
		return nil, err
	}
	var pub ECPoint
	var enc *ECPoint
	if p.opts.AllowKeyGen {
		if !p.dev.CanCreateECC() {
			return nil, fmt.Errorf("%w: 厂商库没有 EVDF_CreateKeyPair_ECC，无法在设备内生成密钥（请预先灌入索引，或关闭 BASTION_SDF_ALLOW_KEYGEN）", crypto.ErrNotImplemented)
		}
		pub, err = p.dev.CreateECC(1, idx)
		if err != nil {
			return nil, fmt.Errorf("在索引 %d 生成 SM2 签名密钥失败: %w", idx, err)
		}
		ep, errEnc := p.dev.CreateECC(0, idx)
		if errEnc == nil {
			enc = &ep
		}
	} else {
		pub, err = p.dev.ExportSignPub(idx)
		if err != nil {
			return nil, fmt.Errorf("导出索引 %d 的 SM2 签名公钥失败（确认该索引已有签名密钥）: %w", idx, err)
		}
		ep, errEnc := p.dev.ExportEncPub(idx)
		if errEnc == nil {
			enc = &ep
		}
	}
	id, err := newKeyID("sm2")
	if err != nil {
		return nil, err
	}
	now := time.Now()
	remark := req.Remark
	if remark != "" {
		remark += "；"
	}
	remark += fmt.Sprintf("SM2 私钥留在密码机索引 %d，不导出", idx)
	rec := &keyRec{
		Kind:  kindECC,
		Index: idx,
		Meta: crypto.KeyMeta{
			KeyID:       id,
			KeyType:     crypto.KeyTypeSM2,
			Usage:       req.Usage,
			Alias:       req.Alias,
			TenantID:    req.TenantID,
			OwnerID:     req.OwnerID,
			Status:      crypto.KeyStatusActive,
			Provider:    string(crypto.ProviderSDF),
			PublicKey:   pubRaw(pub.X, pub.Y),
			Fingerprint: fingerprint(pub.X),
			Version:     1,
			ExpireAt:    req.ExpireAt,
			RotateDays:  req.RotateDays,
			CreateTime:  now,
			UpdateTime:  now,
			Remark:      remark,
		},
	}
	if enc != nil {
		rec.EncPubB64 = pubRaw(enc.X, enc.Y)
	}
	if err := p.cat.insert(rec); err != nil {
		return nil, err
	}
	meta := rec.Meta
	return &meta, nil
}

func (p *Provider) allocSM2() (uint32, error) {
	base := uint32(p.opts.SM2Index)
	if !p.opts.AllowKeyGen {
		if id, ok := p.cat.indexActive(kindECC, base); ok {
			return 0, fmt.Errorf("%w: SM2 索引 %d 已绑定密钥 %s", errInvalid, base, id)
		}
		return base, nil
	}
	for i := base; i < base+64 && i < 256; i++ {
		if _, ok := p.cat.indexActive(kindECC, i); !ok {
			return i, nil
		}
	}
	return 0, fmt.Errorf("%w: 没有可用的 SM2 索引", errInvalid)
}

func (p *Provider) generateSM4(req crypto.KeyGenRequest) (*crypto.KeyMeta, error) {
	wrapped, err := p.dev.WrapSM4(uint32(p.opts.KEKIndex), p.algs.SMS4ECB)
	if err != nil {
		return nil, fmt.Errorf("用 KEK 索引 %d 生成 SM4 密钥失败: %w", p.opts.KEKIndex, err)
	}
	id, err := newKeyID("sm4")
	if err != nil {
		return nil, err
	}
	now := time.Now()
	remark := req.Remark
	if remark != "" {
		remark += "；"
	}
	remark += fmt.Sprintf("SM4 密钥由 KEK 索引 %d 包装，明文不离开密码机", p.opts.KEKIndex)
	rec := &keyRec{
		Kind:       kindSM4,
		Index:      uint32(p.opts.KEKIndex),
		KEKIndex:   uint32(p.opts.KEKIndex),
		WrappedB64: crypto.B64(wrapped),
		Meta: crypto.KeyMeta{
			KeyID:       id,
			KeyType:     crypto.KeyTypeSM4,
			Usage:       req.Usage,
			Alias:       req.Alias,
			TenantID:    req.TenantID,
			OwnerID:     req.OwnerID,
			Status:      crypto.KeyStatusActive,
			Provider:    string(crypto.ProviderSDF),
			Fingerprint: fingerprint(wrapped),
			Version:     1,
			ExpireAt:    req.ExpireAt,
			RotateDays:  req.RotateDays,
			CreateTime:  now,
			UpdateTime:  now,
			Remark:      remark,
		},
	}
	if err := p.cat.insert(rec); err != nil {
		return nil, err
	}
	meta := rec.Meta
	return &meta, nil
}

func (p *Provider) KeyGet(ctx context.Context, keyID string) (*crypto.KeyMeta, error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	if rec, ok := p.cat.get(keyID); ok {
		meta := rec.Meta
		return &meta, nil
	}
	return p.legacy.KeyGet(ctx, keyID)
}

func (p *Provider) KeyList(ctx context.Context, filter crypto.KeyFilter) ([]*crypto.KeyMeta, error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	out := make([]*crypto.KeyMeta, 0)
	for _, rec := range p.cat.list() {
		meta := rec.Meta
		if !matchFilter(meta, filter) {
			continue
		}
		cp := meta
		out = append(out, &cp)
		seen[meta.KeyID] = struct{}{}
	}
	legacy, err := p.legacy.KeyList(ctx, filter)
	if err != nil {
		return nil, err
	}
	for _, meta := range legacy {
		if _, ok := seen[meta.KeyID]; ok {
			continue
		}
		out = append(out, meta)
	}
	return out, nil
}

func matchFilter(m crypto.KeyMeta, f crypto.KeyFilter) bool {
	if f.TenantID != 0 && m.TenantID != f.TenantID {
		return false
	}
	if f.KeyType != "" && m.KeyType != f.KeyType {
		return false
	}
	if f.Usage != "" && m.Usage != f.Usage {
		return false
	}
	if f.Status != "" && m.Status != f.Status {
		return false
	}
	if f.OwnerID != 0 && m.OwnerID != f.OwnerID {
		return false
	}
	if f.Alias != "" && !strings.Contains(m.Alias, f.Alias) {
		return false
	}
	return true
}

func (p *Provider) KeySign(ctx context.Context, keyID string, data []byte) ([]byte, error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	rec, err := p.cat.lookup(keyID)
	if errors.Is(err, errNotInCatalog) {
		return p.legacy.KeySign(ctx, keyID, data)
	}
	if err != nil {
		return nil, err
	}
	if rec.Kind != kindECC {
		return nil, fmt.Errorf("%w: 密钥 %s 类型为 %s，不能用于签名", errInvalid, keyID, rec.Meta.KeyType)
	}
	return p.signIndex(rec, data)
}

func (p *Provider) signIndex(rec *keyRec, data []byte) ([]byte, error) {
	if p.opts.PIN == "" {
		return nil, fmt.Errorf("%w: 未配置 BASTION_SDF_PIN，无法使用设备内私钥", errInvalid)
	}
	pub, err := parsePubB64(rec.Meta.PublicKey)
	if err != nil {
		return nil, err
	}
	digest, err := p.sm2Digest(pub, data)
	if err != nil {
		return nil, err
	}
	r, s, err := p.dev.InternalSign(rec.Index, []byte(p.opts.PIN), digest)
	if err != nil {
		return nil, err
	}
	return marshalSig(r, s)
}

func (p *Provider) KeySignWithHash(ctx context.Context, keyID string, data []byte, h stdcrypto.Hash) ([]byte, error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	rec, err := p.cat.lookup(keyID)
	if errors.Is(err, errNotInCatalog) {
		return p.legacy.KeySignWithHash(ctx, keyID, data, h)
	}
	if err != nil {
		return nil, err
	}
	if rec.Meta.KeyType == crypto.KeyTypeSM2 {
		if h != 0 {
			return nil, fmt.Errorf("%w: SM2 不支持预哈希签名（请使用标准流程，h 传 0）", errInvalid)
		}
		return p.signIndex(rec, data)
	}
	return nil, fmt.Errorf("%w: SDF 托管密钥不支持按外部摘要签名（RSA 请继续使用软件密钥）", crypto.ErrNotImplemented)
}

func (p *Provider) KeySignPrehashed(ctx context.Context, keyID string, digest []byte, h stdcrypto.Hash) ([]byte, error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	_, err := p.cat.lookup(keyID)
	if errors.Is(err, errNotInCatalog) {
		return p.legacy.KeySignPrehashed(ctx, keyID, digest, h)
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%w: SDF 托管密钥不支持 RSA 预摘要签名", crypto.ErrNotImplemented)
}

func (p *Provider) KeyEncrypt(ctx context.Context, keyID string, plaintext []byte) ([]byte, error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	rec, err := p.cat.lookup(keyID)
	if errors.Is(err, errNotInCatalog) {
		return p.legacy.KeyEncrypt(ctx, keyID, plaintext)
	}
	if err != nil {
		return nil, err
	}
	switch rec.Kind {
	case kindECC:
		if len(plaintext) == 0 {
			return nil, nil
		}
		blob, err := p.dev.InternalEncrypt(rec.Index, plaintext)
		if err != nil {
			return nil, err
		}
		return marshalCipher(blob)
	case kindSM4:
		wrapped, err := crypto.B64Decode(rec.WrappedB64)
		if err != nil || len(wrapped) == 0 {
			return nil, fmt.Errorf("%w: 密钥 %s 缺少 KEK 包装材料", crypto.ErrKeyDisabled, keyID)
		}
		iv, err := randReadN(16)
		if err != nil {
			return nil, err
		}
		ct, err := p.dev.UnwrapEncrypt(rec.KEKIndex, p.algs.SMS4ECB, p.algs.SMS4CBC, wrapped, iv, pkcs7Pad(plaintext, 16))
		if err != nil {
			return nil, err
		}
		return append(iv, ct...), nil
	default:
		return nil, fmt.Errorf("%w: 密钥类型 %s 不支持加密", errInvalid, rec.Meta.KeyType)
	}
}

func (p *Provider) KeyDecrypt(ctx context.Context, keyID string, ciphertext []byte) ([]byte, error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	rec, err := p.cat.lookup(keyID)
	if errors.Is(err, errNotInCatalog) {
		return p.legacy.KeyDecrypt(ctx, keyID, ciphertext)
	}
	if err != nil {
		return nil, err
	}
	switch rec.Kind {
	case kindECC:
		if p.opts.PIN == "" {
			return nil, fmt.Errorf("%w: 未配置 BASTION_SDF_PIN，无法使用设备内私钥", errInvalid)
		}
		blob, err := unmarshalCipher(ciphertext)
		if err != nil {
			return nil, err
		}
		return p.dev.InternalDecrypt(rec.Index, []byte(p.opts.PIN), blob)
	case kindSM4:
		if len(ciphertext) <= 16 {
			return nil, fmt.Errorf("%w: 密文长度不足（缺少 IV）", errDecrypt)
		}
		wrapped, err := crypto.B64Decode(rec.WrappedB64)
		if err != nil || len(wrapped) == 0 {
			return nil, fmt.Errorf("%w: 密钥 %s 缺少 KEK 包装材料", crypto.ErrKeyDisabled, keyID)
		}
		out, err := p.dev.UnwrapDecrypt(rec.KEKIndex, p.algs.SMS4ECB, p.algs.SMS4CBC, wrapped, ciphertext[:16], ciphertext[16:])
		if err != nil {
			return nil, err
		}
		return pkcs7Unpad(out, 16)
	default:
		return nil, fmt.Errorf("%w: 密钥类型 %s 不支持解密", errInvalid, rec.Meta.KeyType)
	}
}

func (p *Provider) KeyDestroy(ctx context.Context, keyID string) error {
	if err := ctxDone(ctx); err != nil {
		return err
	}
	rec, ok := p.cat.get(keyID)
	if !ok {
		return p.legacy.KeyDestroy(ctx, keyID)
	}
	rec.Meta.Status = crypto.KeyStatusDestroyed
	rec.Meta.UpdateTime = time.Now()
	rec.WrappedB64 = ""
	return p.cat.put(rec)
}

func randReadN(n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := randRead(buf); err != nil {
		return nil, err
	}
	return buf, nil
}
