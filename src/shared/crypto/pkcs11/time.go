package pkcs11

import "time"

// timeNow 提取为独立函数，便于测试替换。
func timeNow() time.Time { return time.Now() }
