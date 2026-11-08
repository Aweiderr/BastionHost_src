package sdf

import (
	"bytes"
	"testing"
)

func TestPKCS7RoundTrip(t *testing.T) {
	in := []byte("abc")
	padded := pkcs7Pad(in, 16)
	if len(padded) != 16 || padded[15] != 13 {
		t.Fatalf("填充结果 %x", padded)
	}
	out, err := pkcs7Unpad(padded, 16)
	if err != nil || !bytes.Equal(out, in) {
		t.Fatalf("去填充 = %q %v", out, err)
	}
}

func TestAlgProfile(t *testing.T) {
	g, err := parseAlgs("")
	if err != nil || g.SM2Sign != 0x00020200 || g.SM2Enc != 0x00020800 {
		t.Fatalf("默认剖面 = %+v %v", g, err)
	}
	legacy, err := parseAlgs("legacy")
	if err != nil || legacy.SM2Sign != 0x00020100 {
		t.Fatalf("legacy 剖面 = %+v %v", legacy, err)
	}
	if _, err := parseAlgs("nope"); err == nil {
		t.Fatal("未知剖面应失败")
	}
}
