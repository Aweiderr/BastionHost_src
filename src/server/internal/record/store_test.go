package record

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bastion.local/shared/crypto/soft"
)

func testProv(t *testing.T) *soft.Provider {
	t.Helper()
	prov, err := soft.New(soft.Options{Backend: "go", KeyStoreDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return prov
}

func TestSharedRecordingSurvivesNodeLoss(t *testing.T) {
	prov := testProv(t)
	root := t.TempDir()
	// 两台代理挂载同一目录，密钥只来自共享口令，不共享节点本地密钥库。
	nodeA := &FSStore{Root: root, Scheme: "shared"}
	nodeB := &FSStore{Root: root, Scheme: "shared"}
	cipherA, err := NewSharedCipher(prov, "cluster-record-secret")
	if err != nil {
		t.Fatal(err)
	}
	cipherB, err := NewSharedCipher(prov, "cluster-record-secret")
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	w, err := New(ctx, prov, "", Options{
		Dir: root, SessionNo: "S-shared-1", ChunkSize: 1024, Store: nodeA, Cipher: cipherA,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("session output from node A\nline two")
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	ref, _, digest, chunks, err := w.Seal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ref, "shared://") {
		t.Fatalf("ref=%s", ref)
	}
	if chunks < 1 {
		t.Fatalf("chunks=%d", chunks)
	}

	// 节点 A 的进程已经不在。节点 B 只凭共享目录和同一口令回放。
	key, raw, err := ReadSession(ctx, nodeB, "S-shared-1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	plain, info, err := Decode(ctx, prov, cipherB, raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != string(payload) {
		t.Fatalf("plain=%q", plain)
	}
	if info.Digest != digest || info.SessionNo != "S-shared-1" {
		t.Fatalf("info=%+v digest=%s key=%s", info, digest, key)
	}
}

func TestS3RecordingRoundTrip(t *testing.T) {
	prov := testProv(t)
	objects := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			http.Error(w, "unsigned", http.StatusForbidden)
			return
		}
		key := strings.TrimPrefix(r.URL.Path, "/records/")
		switch r.Method {
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			objects[key] = body
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			body, ok := objects[key]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(body)
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	store, err := NewS3Store(S3Config{
		Endpoint: srv.URL, Region: "us-east-1", Bucket: "records",
		AccessKey: "AKID", SecretKey: "secret", PathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := NewSharedCipher(prov, "00112233445566778899aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ctx := context.Background()
	w, err := New(ctx, prov, "", Options{
		Dir: dir, SessionNo: "S-s3-1", ChunkSize: 4, Store: store, Cipher: cipher,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	ref, _, _, _, err := w.Seal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ref, "s3://records/") {
		t.Fatalf("ref=%s", ref)
	}
	_ = os.RemoveAll(dir)
	_, raw, err := ReadSession(ctx, store, "S-s3-1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	plain, _, err := Decode(ctx, prov, cipher, raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "abcdef" {
		t.Fatalf("plain=%q objects=%d", plain, len(objects))
	}
}

func TestFSPublishCopiesWhenRootsDiffer(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	path := filepath.Join(src, "a.bsr")
	if err := os.WriteFile(path, []byte("bsr"), 0o640); err != nil {
		t.Fatal(err)
	}
	store := &FSStore{Root: dst, Scheme: "nfs"}
	if err := store.Publish(context.Background(), "2026/11/10/a.bsr", path); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "2026", "11", "10", "a.bsr"))
	if err != nil || string(got) != "bsr" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if store.URI("2026/11/10/a.bsr") != "nfs://2026/11/10/a.bsr" {
		t.Fatal(store.URI("2026/11/10/a.bsr"))
	}
}
