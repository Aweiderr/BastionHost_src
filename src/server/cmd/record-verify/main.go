// Command record-verify 校验会话录像文件的完整性与可解密性。
//
// 它按容器格式逐片解密并重算 SM3 摘要链：
// 只要有任何一片被删除、重排或篡改，哈希链就会断裂。
// 这是"录像可回放、且可自证未被篡改"的最小证明工具。
//
// 用法：
//
//	record-verify -file ./data/records/2026/09/18/Sxxx.bsr
//	record-verify -file xxx.bsr -keystore ./data/server-keys -master ./data/server-master.key
package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"time"

	"bastion.local/shared/crypto"
	"bastion.local/shared/crypto/factory"
)

const digestSize = 32

func main() {
	file := flag.String("file", "", "录像文件路径")
	keystore := flag.String("keystore", "./data/server-keys", "密钥库目录")
	master := flag.String("master", "./data/server-master.key", "主密钥文件")
	backend := flag.String("backend", "go", "软件实现后端")
	show := flag.Int("show", 0, "打印前 N 个分片的明文预览")
	flag.Parse()

	if *file == "" {
		fmt.Println("必须指定 -file")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		fmt.Printf("读取录像失败: %v\n", err)
		os.Exit(1)
	}

	prov, err := factory.New(factory.Config{
		Provider: "soft", Backend: *backend,
		KeyStoreDir: *keystore, MasterKeyFile: *master,
	})
	if err != nil {
		fmt.Printf("密码服务初始化失败: %v\n", err)
		os.Exit(1)
	}
	keys, err := prov.KeyList(context.Background(), crypto.KeyFilter{
		Usage: crypto.UsageDataEncrypt, Alias: "record_encrypt",
	})
	if err != nil || len(keys) == 0 {
		fmt.Printf("未找到录像加密密钥（alias=record_encrypt）: %v\n", err)
		os.Exit(1)
	}
	keyID := keys[0].KeyID
	fmt.Printf("录像文件   : %s（%d 字节）\n", *file, len(raw))
	fmt.Printf("加密密钥   : %s\n", keyID)

	// ---- 解析文件头 ----
	if len(raw) < 10 || string(raw[:4]) != "BSR1" {
		fmt.Println("❌ 文件头魔数不匹配，不是本系统录像文件")
		os.Exit(1)
	}
	version := raw[4]
	noLen := int(binary.BigEndian.Uint16(raw[8:10]))
	if 10+noLen > len(raw) {
		fmt.Println("❌ 文件头长度非法")
		os.Exit(1)
	}
	sessionNo := string(raw[10 : 10+noLen])
	offset := 10 + noLen
	fmt.Printf("格式版本   : %d\n", version)
	fmt.Printf("会话号     : %s\n", sessionNo)

	// ---- 逐片解密并校验摘要链 ----
	ctx := context.Background()
	prev := make([]byte, digestSize)
	chain := make([]byte, digestSize)
	index := 0
	var plainTotal int64

	for {
		if offset+12+digestSize*2 > len(raw) {
			break
		}
		idx := binary.BigEndian.Uint32(raw[offset : offset+4])
		plainLen := binary.BigEndian.Uint32(raw[offset+4 : offset+8])
		cipherLen := binary.BigEndian.Uint32(raw[offset+8 : offset+12])
		head := offset + 12
		prevInFile := raw[head : head+digestSize]
		digestInFile := raw[head+digestSize : head+digestSize*2]
		body := head + digestSize*2
		if body+int(cipherLen) > len(raw) {
			fmt.Printf("❌ 第 %d 片长度越界（cipher_len=%d）\n", index, cipherLen)
			os.Exit(1)
		}
		cipherText := raw[body : body+int(cipherLen)]

		plain, err := prov.KeyDecrypt(ctx, keyID, cipherText)
		if err != nil {
			fmt.Printf("❌ 第 %d 片解密失败: %v\n", index, err)
			os.Exit(1)
		}
		if uint32(len(plain)) != plainLen {
			fmt.Printf("❌ 第 %d 片明文长度不符：期望 %d 实际 %d\n", index, plainLen, len(plain))
			os.Exit(1)
		}
		if !equal(prevInFile, prev) {
			fmt.Printf("❌ 第 %d 片前序摘要不匹配，录像被重排或篡改\n", index)
			os.Exit(1)
		}

		h := prov.SM3New()
		h.Write(prev)
		var idxBuf [4]byte
		binary.BigEndian.PutUint32(idxBuf[:], idx)
		h.Write(idxBuf[:])
		h.Write(plain)
		want := h.Sum(nil)
		if !equal(want, digestInFile) {
			fmt.Printf("❌ 第 %d 片摘要校验失败，明文已被篡改\n", index)
			os.Exit(1)
		}

		ch := prov.SM3New()
		ch.Write(chain)
		ch.Write(digestInFile)
		chain = ch.Sum(nil)

		if *show > 0 && index < *show {
			fmt.Printf("   分片 %d 明文预览: %q\n", index, previewBytes(plain, 120))
		}
		plainTotal += int64(len(plain))
		prev = digestInFile
		offset = body + int(cipherLen)
		index++
	}

	// ---- 校验文件尾 ----
	if offset+8+digestSize > len(raw) || string(raw[offset:offset+4]) != "BSRE" {
		fmt.Println("❌ 缺少文件尾，录像未正常封存")
		os.Exit(1)
	}
	tailChunks := binary.BigEndian.Uint32(raw[offset+4 : offset+8])
	tailDigest := raw[offset+8 : offset+8+digestSize]

	fmt.Printf("分片数     : %d\n", index)
	fmt.Printf("明文总长   : %d 字节\n", plainTotal)
	fmt.Printf("文件尾声明 : %d 片\n", tailChunks)
	if int(tailChunks) != index {
		fmt.Println("❌ 文件尾分片数与实际不符")
		os.Exit(1)
	}
	if !equal(tailDigest, chain) {
		fmt.Println("❌ 全量摘要不匹配，录像不可信")
		os.Exit(1)
	}
	fmt.Printf("全量摘要   : %s\n", hex.EncodeToString(chain))
	fmt.Printf("校验时间   : %s\n", time.Now().Format(time.RFC3339))
	fmt.Println("\n✅ 录像完整：所有分片可解密，摘要链连续，文件尾一致")
}

func equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func previewBytes(b []byte, n int) string {
	s := string(b)
	s = replaceAll(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func replaceAll(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch r {
		case '\r':
			out = append(out, '⏎')
		case '\n':
			out = append(out, '␊')
		case 0x1b:
			out = append(out, '␛')
		default:
			if r < 0x20 {
				continue
			}
			out = append(out, r)
		}
	}
	return string(out)
}
