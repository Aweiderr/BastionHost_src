package sdf

import (
	"fmt"

	"bastion.local/shared/crypto"
)

// GM/T 0018 返回码。数值是公开标准，不是厂商私有头文件的副本。
const (
	sdrOK            uint32 = 0x00000000
	sdrBase          uint32 = 0x01000000
	sdrUnknown              = sdrBase + 0x01
	sdrNotSupport           = sdrBase + 0x02
	sdrCommFail             = sdrBase + 0x03
	sdrHardFail             = sdrBase + 0x04
	sdrOpenDevice           = sdrBase + 0x05
	sdrOpenSession          = sdrBase + 0x06
	sdrParDeny              = sdrBase + 0x07
	sdrKeyNotExist          = sdrBase + 0x08
	sdrAlgNotSupport        = sdrBase + 0x09
	sdrVerifyErr            = sdrBase + 0x0E
	sdrSymOpErr             = sdrBase + 0x0F
	sdrKeyErr               = sdrBase + 0x15
	sdrEncDataErr           = sdrBase + 0x16
	sdrInArgErr             = sdrBase + 0x1D
	sdrHandleInvalid        = sdrBase + 0x20
)

// errVerifyFailed 表示验签未通过（算法正常，签名不对）。
var errVerifyFailed = fmt.Errorf("%w", crypto.ErrVerifyFailed)

// errNotInCatalog 表示密钥 ID 不在 SDF 目录中，应交给软件密钥库。
var errNotInCatalog = fmt.Errorf("sdf: 密钥不在密码机目录")

var (
	errInvalid        = crypto.ErrInvalidParam
	errDecrypt        = crypto.ErrDecryptFailed
	errNotImplemented = crypto.ErrNotImplemented
)

type callError struct {
	Op   string
	Code uint32
}

func (e *callError) Error() string {
	return fmt.Sprintf("sdf: %s 失败：%s（0x%08X）", e.Op, sdrText(e.Code), e.Code)
}

func call(rc int32, op string) error {
	if rc == 0 {
		return nil
	}
	return &callError{Op: op, Code: uint32(rc)}
}

func callVerify(rc int32, op string) error {
	if rc == 0 {
		return nil
	}
	if uint32(rc) == sdrVerifyErr {
		return errVerifyFailed
	}
	return call(rc, op)
}

func sdrText(code uint32) string {
	switch code {
	case sdrOK:
		return "成功"
	case sdrUnknown:
		return "未知错误"
	case sdrNotSupport:
		return "不支持的接口"
	case sdrCommFail:
		return "与设备通信失败"
	case sdrHardFail:
		return "硬件错误"
	case sdrOpenDevice:
		return "打开设备失败"
	case sdrOpenSession:
		return "创建会话失败"
	case sdrParDeny:
		return "无私钥访问权限"
	case sdrKeyNotExist:
		return "密钥不存在"
	case sdrAlgNotSupport:
		return "不支持的算法"
	case sdrVerifyErr:
		return "验签失败"
	case sdrSymOpErr:
		return "对称运算失败"
	case sdrKeyErr:
		return "密钥错误"
	case sdrEncDataErr:
		return "密文错误"
	case sdrInArgErr:
		return "输入参数错误"
	case sdrHandleInvalid:
		return "句柄无效"
	default:
		return "设备返回错误"
	}
}
