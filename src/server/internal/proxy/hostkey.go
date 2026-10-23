package proxy

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"golang.org/x/crypto/ssh"
)

// 主机密钥策略（与 ast_asset.ssh_host_key_policy 对齐）。
const (
	HostKeyPolicyStrict   = "strict"   // 必须与库中指纹一致；库中为空则拒绝
	HostKeyPolicyTOFU     = "tofu"     // 首次信任并回写；已存在则必须一致
	HostKeyPolicyInsecure = "insecure" // 跳过校验（仅调试）
)

var (
	// ErrHostKeyMismatch 目标主机指纹与资产台账不一致。
	ErrHostKeyMismatch = errors.New("目标主机 SSH 指纹与资产台账不一致，连接已阻断")
	// ErrHostKeyUnknown 严格模式下资产尚未登记指纹。
	ErrHostKeyUnknown = errors.New("资产未登记 SSH 主机指纹，严格策略下拒绝连接")
)

// HostKeyCheck 是一次指纹校验的结果。
type HostKeyCheck struct {
	Fingerprint string // 实际观测到的 SHA256 指纹
	Learned     bool   // TOFU 首次见到，需要回写台账
	Policy      string
}

// NormalizeHostKeyFingerprint 统一指纹格式（去空白、统一 SHA256: 前缀大小写）。
func NormalizeHostKeyFingerprint(fp string) string {
	fp = strings.TrimSpace(fp)
	if fp == "" {
		return ""
	}
	// ssh.FingerprintSHA256 形如 "SHA256:xxxx"
	if i := strings.IndexByte(fp, ':'); i > 0 {
		return strings.ToUpper(fp[:i]) + fp[i:]
	}
	return "SHA256:" + fp
}

// FingerprintsEqual 比较两个指纹（忽略空白与算法前缀大小写）。
func FingerprintsEqual(a, b string) bool {
	return NormalizeHostKeyFingerprint(a) == NormalizeHostKeyFingerprint(b)
}

// NormalizeHostKeyPolicy 归一化策略名；空串视为 tofu。
func NormalizeHostKeyPolicy(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case HostKeyPolicyStrict:
		return HostKeyPolicyStrict
	case HostKeyPolicyInsecure:
		return HostKeyPolicyInsecure
	case HostKeyPolicyTOFU, "":
		return HostKeyPolicyTOFU
	default:
		return HostKeyPolicyTOFU
	}
}

// EvaluateHostKey 根据策略判定是否接受目标公钥。
//
// expected 是资产台账中的指纹；为空表示尚未登记。
func EvaluateHostKey(policy, expected string, key ssh.PublicKey) (*HostKeyCheck, error) {
	if key == nil {
		return nil, fmt.Errorf("目标未提供主机公钥")
	}
	fp := ssh.FingerprintSHA256(key)
	policy = NormalizeHostKeyPolicy(policy)
	out := &HostKeyCheck{Fingerprint: fp, Policy: policy}

	switch policy {
	case HostKeyPolicyInsecure:
		return out, nil
	case HostKeyPolicyStrict:
		if expected == "" {
			return out, ErrHostKeyUnknown
		}
		if !FingerprintsEqual(expected, fp) {
			return out, fmt.Errorf("%w：期望 %s，实际 %s", ErrHostKeyMismatch, NormalizeHostKeyFingerprint(expected), fp)
		}
		return out, nil
	default: // tofu
		if expected == "" {
			out.Learned = true
			return out, nil
		}
		if !FingerprintsEqual(expected, fp) {
			return out, fmt.Errorf("%w：期望 %s，实际 %s", ErrHostKeyMismatch, NormalizeHostKeyFingerprint(expected), fp)
		}
		return out, nil
	}
}

// NewHostKeyCallback 构造 ssh.HostKeyCallback。
//
// onLearn 在 TOFU 首次见到时回调（可异步回写台账）；允许为 nil。
func NewHostKeyCallback(policy, expected string, onLearn func(fp string)) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		check, err := EvaluateHostKey(policy, expected, key)
		if err != nil {
			return err
		}
		if check.Learned && onLearn != nil {
			onLearn(check.Fingerprint)
		}
		return nil
	}
}
