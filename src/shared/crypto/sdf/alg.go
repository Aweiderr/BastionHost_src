package sdf

import (
	"fmt"
	"strings"

	"bastion.local/shared/crypto"
)

// algSet 是送进 SDF 的算法标识。
//
// gmt0006 与 GM/T 0006、厂商 algdef.h / zayk_api.h 一致，是默认值。
// legacy 对应部分旧固件在 sdf_type.h 里注释掉的那组旧值。
type algSet struct {
	Name    string
	SM2     uint32
	SM2Sign uint32
	SM2Enc  uint32
	SM3     uint32
	SMS4ECB uint32
	SMS4CBC uint32
	SMS4CTR uint32
}

var algGMT0006 = algSet{
	Name:    "gmt0006",
	SM2:     0x00020100,
	SM2Sign: 0x00020200,
	SM2Enc:  0x00020800,
	SM3:     0x00000001,
	SMS4ECB: 0x00000401,
	SMS4CBC: 0x00000402,
	SMS4CTR: 0x00000420,
}

var algLegacy = algSet{
	Name:    "legacy",
	SM2:     0x00020000,
	SM2Sign: 0x00020100,
	SM2Enc:  0x00020400,
	SM3:     0x00000001,
	SMS4ECB: 0x00000401,
	SMS4CBC: 0x00000402,
	SMS4CTR: 0x00000420,
}

func parseAlgs(profile string) (algSet, error) {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "", "gmt0006", "gmt":
		return algGMT0006, nil
	case "legacy":
		return algLegacy, nil
	default:
		return algSet{}, fmt.Errorf("%w: 未知 SDF 算法剖面 %q（可选 gmt0006 / legacy）", crypto.ErrInvalidParam, profile)
	}
}
