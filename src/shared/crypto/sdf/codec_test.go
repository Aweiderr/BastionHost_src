package sdf

import (
	"bytes"
	"slices"
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

func TestSM2KeyGenAlgOrder(t *testing.T) {
	g, err := parseAlgs("gmt0006")
	if err != nil {
		t.Fatal(err)
	}
	got := g.sm2KeyGenAlgs()
	want := []uint32{0x00020100, 0x00020200, 0x00020800}
	if !slices.Equal(got, want) {
		t.Fatalf("gmt0006 密钥生成标识 = %v，期望 %v", got, want)
	}
	legacy, err := parseAlgs("legacy")
	if err != nil {
		t.Fatal(err)
	}
	got = legacy.sm2KeyGenAlgs()
	want = []uint32{0x00020000, 0x00020100, 0x00020400}
	if !slices.Equal(got, want) {
		t.Fatalf("legacy 密钥生成标识 = %v，期望 %v", got, want)
	}
}
