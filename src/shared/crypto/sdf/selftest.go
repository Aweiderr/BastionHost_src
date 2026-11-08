package sdf

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"bastion.local/shared/crypto"
)

// SelfTest 检查设备在位，并用设备完成 SM3 标准向量、SM4 往返、SM2 往返。
// 配置了 PIN 时额外做一次内部私钥签名；KEK 索引会做一次会话密钥往返。
// 这两项不在设备上留下新的长期密钥。
func (p *Provider) SelfTest(ctx context.Context) (*crypto.SelfTestResult, error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	start := time.Now()
	res := &crypto.SelfTestResult{
		Provider:  string(p.Name()),
		Healthy:   true,
		CheckedAt: start,
	}
	fail := func(item *crypto.SelfTestItem) {
		item.Passed = false
		res.Healthy = false
	}

	item := crypto.SelfTestItem{Name: "设备在位"}
	t0 := time.Now()
	info, err := p.dev.Info()
	item.CostMs = time.Since(t0).Milliseconds()
	if err != nil {
		item.Detail = err.Error()
		fail(&item)
	} else {
		item.Passed = info.Name != "" || info.Serial != "" || info.Issuer != ""
		item.Detail = fmt.Sprintf("%s / %s / %s", info.Issuer, info.Name, info.Serial)
		if !item.Passed {
			item.Detail = "设备信息为空"
			res.Healthy = false
		}
	}
	res.Items = append(res.Items, item)

	item = crypto.SelfTestItem{Name: "SM3 摘要"}
	t0 = time.Now()
	sum, err := p.hashSM3([]byte("abc"))
	item.CostMs = time.Since(t0).Milliseconds()
	const abc = "66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0"
	if err != nil {
		item.Detail = err.Error()
		fail(&item)
	} else if hex.EncodeToString(sum) != abc {
		item.Detail = "标准测试向量不匹配"
		fail(&item)
	} else {
		item.Passed = true
	}
	res.Items = append(res.Items, item)

	if p.sm3Fallback.Load() {
		item = crypto.SelfTestItem{Name: "SM3 未回退", Passed: false, Detail: "设备 SM3 失败过，已临时使用软件 SM3"}
		res.Healthy = false
		res.Items = append(res.Items, item)
	}

	item = crypto.SelfTestItem{Name: "SM4 加解密"}
	t0 = time.Now()
	key, err1 := p.dev.Random(16)
	iv, err2 := p.dev.Random(16)
	plain := []byte("bastion-selftest-payload")
	if err1 != nil || err2 != nil {
		item.Detail = "生成密钥或 IV 失败"
		fail(&item)
	} else if ct, err := p.SM4Encrypt(key, plain, crypto.ModeCBC, iv); err != nil {
		item.Detail = "加密失败: " + err.Error()
		fail(&item)
	} else if pt, err := p.SM4Decrypt(key, ct, crypto.ModeCBC, iv); err != nil {
		item.Detail = "解密失败: " + err.Error()
		fail(&item)
	} else if string(pt) != string(plain) {
		item.Detail = "往返结果不一致"
		fail(&item)
	} else {
		item.Passed = true
	}
	for i := range key {
		key[i] = 0
	}
	item.CostMs = time.Since(t0).Milliseconds()
	res.Items = append(res.Items, item)

	item = crypto.SelfTestItem{Name: "SM2 签名验签"}
	t0 = time.Now()
	pair, err := p.SM2GenerateKey()
	if err != nil {
		item.Detail = "密钥生成失败: " + err.Error()
		fail(&item)
	} else {
		msg := []byte("bastion-selftest-message")
		sig, err := p.SM2Sign(pair.Private, msg)
		if err != nil {
			item.Detail = "签名失败: " + err.Error()
			fail(&item)
		} else if ok, err := p.SM2Verify(pair.Public, msg, sig); err != nil || !ok {
			if err != nil {
				item.Detail = "验签出错: " + err.Error()
			} else {
				item.Detail = "验签未通过"
			}
			fail(&item)
		} else if ok, _ := p.SM2Verify(pair.Public, []byte("tampered"), sig); ok {
			item.Detail = "篡改消息后仍验签通过"
			fail(&item)
		} else {
			item.Passed = true
		}
		for i := range pair.Private.D {
			pair.Private.D[i] = 0
		}
	}
	item.CostMs = time.Since(t0).Milliseconds()
	res.Items = append(res.Items, item)

	item = crypto.SelfTestItem{Name: "随机数质量"}
	t0 = time.Now()
	a, err1 := p.Random(32)
	b, err2 := p.Random(32)
	item.CostMs = time.Since(t0).Milliseconds()
	if err1 != nil || err2 != nil {
		item.Detail = "随机数生成失败"
		fail(&item)
	} else if hex.EncodeToString(a) == hex.EncodeToString(b) || constantBytes(a) {
		item.Detail = "随机数重复或为常量"
		fail(&item)
	} else {
		item.Passed = true
	}
	res.Items = append(res.Items, item)

	item = crypto.SelfTestItem{Name: "内部 SM2 私钥"}
	t0 = time.Now()
	if p.opts.PIN == "" {
		item.Passed = true
		item.Detail = "未配置 BASTION_SDF_PIN，已跳过内部私钥运算"
	} else if pub, err := p.dev.ExportSignPub(uint32(p.opts.SM2Index)); err != nil {
		item.Detail = err.Error()
		fail(&item)
	} else {
		digest, err := p.sm2Digest(pointToPublic(pub), []byte("sdf-internal-selftest"))
		if err != nil {
			item.Detail = err.Error()
			fail(&item)
		} else if r, s, err := p.dev.InternalSign(uint32(p.opts.SM2Index), []byte(p.opts.PIN), digest); err != nil {
			item.Detail = err.Error()
			fail(&item)
		} else if err := p.dev.InternalVerify(uint32(p.opts.SM2Index), digest, r, s); err != nil {
			item.Detail = err.Error()
			fail(&item)
		} else {
			item.Passed = true
			item.Detail = fmt.Sprintf("索引 %d", p.opts.SM2Index)
		}
	}
	item.CostMs = time.Since(t0).Milliseconds()
	res.Items = append(res.Items, item)

	item = crypto.SelfTestItem{Name: "KEK 会话密钥"}
	t0 = time.Now()
	wrapped, err := p.dev.WrapSM4(uint32(p.opts.KEKIndex), p.algs.SMS4ECB)
	if err != nil {
		item.Detail = err.Error()
		fail(&item)
	} else {
		iv, err := p.dev.Random(16)
		msg := pkcs7Pad([]byte("kek-selftest"), 16)
		if err != nil {
			item.Detail = err.Error()
			fail(&item)
		} else if ct, err := p.dev.UnwrapEncrypt(uint32(p.opts.KEKIndex), p.algs.SMS4ECB, p.algs.SMS4CBC, wrapped, iv, msg); err != nil {
			item.Detail = err.Error()
			fail(&item)
		} else if pt, err := p.dev.UnwrapDecrypt(uint32(p.opts.KEKIndex), p.algs.SMS4ECB, p.algs.SMS4CBC, wrapped, iv, ct); err != nil {
			item.Detail = err.Error()
			fail(&item)
		} else if string(pt) != string(msg) {
			item.Detail = "KEK 往返结果不一致"
			fail(&item)
		} else {
			item.Passed = true
			item.Detail = fmt.Sprintf("KEK 索引 %d", p.opts.KEKIndex)
		}
	}
	item.CostMs = time.Since(t0).Milliseconds()
	res.Items = append(res.Items, item)

	res.DurationMs = time.Since(start).Milliseconds()
	return res, nil
}

func constantBytes(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	for i := 1; i < len(b); i++ {
		if b[i] != b[0] {
			return false
		}
	}
	return true
}
