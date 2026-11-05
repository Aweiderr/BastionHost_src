package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
	"testing"
	"time"
)

func TestSFTPPolicyDirections(t *testing.T) {
	pol := sftpPolicy{AllowUpload: false, AllowDownload: true, MaxBytes: 100, DenyExt: extSet([]string{"exe"})}
	if d := decideSFTPOpen(pol, "/tmp/a.txt", sftpFlagWrite|sftpFlagCreat); d.Action != "deny" || d.Direction != "upload" {
		t.Fatalf("upload %#v", d)
	}
	if d := decideSFTPOpen(pol, "/tmp/a.txt", sftpFlagRead); d.Action != "allow" || d.Direction != "download" {
		t.Fatalf("download %#v", d)
	}
	if d := decideSFTPOpen(pol, "/tmp/a.EXE", sftpFlagRead); d.Action != "deny" {
		t.Fatalf("ext %#v", d)
	}
	if d := decideSFTPOpen(sftpPolicy{AllowUpload: true, AllowDownload: true}, "/tmp/a.txt", sftpFlagRead|sftpFlagWrite); d.Action != "allow" || d.Direction != "upload" {
		t.Fatalf("rw %#v", d)
	}
}

func TestSFTPProxyDeniesUploadAndAuditsDownload(t *testing.T) {
	client, proxyClient := net.Pipe()
	upstream, proxyUpstream := net.Pipe()
	var events []sftpEvent
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- proxySFTP(ctx, proxyClient, proxyUpstream, sftpPolicy{
			AllowUpload: false, AllowDownload: true, MaxBytes: 1024,
		}, func(ev sftpEvent) {
			events = append(events, ev)
		}, sha256.New)
	}()

	go serveUpstream(t, upstream)

	mustWritePacket(t, client, sftpOpenPacket(7, "/tmp/secret.txt", sftpFlagWrite|sftpFlagCreat))
	st := mustReadPacket(t, client)
	if st[4] != sftpFXPStatus || binary.BigEndian.Uint32(st[9:13]) != sftpFXPerm {
		t.Fatalf("upload status %v", st)
	}

	mustWritePacket(t, client, sftpOpenPacket(8, "/tmp/readme.txt", sftpFlagRead))
	handlePkt := mustReadPacket(t, client)
	if handlePkt[4] != sftpFXPHandle {
		t.Fatalf("handle %v", handlePkt)
	}
	_, handle, err := parseSFTPHandle(handlePkt)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("hello-sftp")
	mustWritePacket(t, client, sftpReadPacket(9, handle, 0, uint32(len(payload))))
	dataPkt := mustReadPacket(t, client)
	_, data, err := parseSFTPData(dataPkt)
	if err != nil || string(data) != string(payload) {
		t.Fatalf("data %q %v", data, err)
	}
	mustWritePacket(t, client, sftpClosePacket(10, handle))
	closePkt := mustReadPacket(t, client)
	if closePkt[4] != sftpFXPStatus || binary.BigEndian.Uint32(closePkt[9:13]) != sftpFXOK {
		t.Fatalf("close %v", closePkt)
	}

	deadline := time.After(time.Second)
	for len(events) < 2 {
		select {
		case <-deadline:
			t.Fatalf("events %#v", events)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if events[0].Direction != "upload" || events[0].Action != "deny" || events[0].Result != "denied" || events[0].Name != "secret.txt" {
		t.Fatalf("deny %#v", events[0])
	}
	sum := sha256.Sum256(payload)
	if events[1].Direction != "download" || events[1].Action != "allow" || events[1].Result != "success" ||
		events[1].Size != int64(len(payload)) || events[1].Digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("download %#v", events[1])
	}
	_ = client.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("proxy did not exit")
	}
}

func serveUpstream(t *testing.T, conn net.Conn) {
	t.Helper()
	defer conn.Close()
	for {
		raw, err := readSFTPPacket(conn)
		if err != nil {
			return
		}
		switch raw[4] {
		case sftpFXPOpen:
			id, _, _, err := parseSFTPOpen(raw)
			if err != nil {
				t.Errorf("open %v", err)
				return
			}
			_, _ = conn.Write(sftpHandlePacket(id, []byte("h1")))
		case sftpFXPRead:
			id, _, err := parseSFTPRead(raw)
			if err != nil {
				return
			}
			_, _ = conn.Write(sftpDataPacket(id, []byte("hello-sftp")))
		case sftpFXPClose:
			id, _, _ := parseSFTPClose(raw)
			_, _ = conn.Write(sftpStatus(id, sftpFXOK, "ok"))
		default:
			return
		}
	}
}

func mustWritePacket(t *testing.T, w io.Writer, raw []byte) {
	t.Helper()
	if _, err := w.Write(raw); err != nil {
		t.Fatal(err)
	}
}

func mustReadPacket(t *testing.T, r io.Reader) []byte {
	t.Helper()
	_ = r.(net.Conn).SetReadDeadline(time.Now().Add(2 * time.Second))
	raw, err := readSFTPPacket(r)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func sftpOpenPacket(id uint32, path string, flags uint32) []byte {
	body := append(uint32BE(id), sshString(path)...)
	body = append(body, uint32BE(flags)...)
	body = append(body, uint32BE(0)...) // attrs flags = 0
	return sftpPacket(sftpFXPOpen, body)
}

func sftpReadPacket(id uint32, handle []byte, offset uint64, n uint32) []byte {
	body := append(uint32BE(id), sshString(string(handle))...)
	var off [8]byte
	binary.BigEndian.PutUint64(off[:], offset)
	body = append(body, off[:]...)
	body = append(body, uint32BE(n)...)
	return sftpPacket(sftpFXPRead, body)
}

func sftpClosePacket(id uint32, handle []byte) []byte {
	body := append(uint32BE(id), sshString(string(handle))...)
	return sftpPacket(sftpFXPClose, body)
}

func sftpHandlePacket(id uint32, handle []byte) []byte {
	body := append(uint32BE(id), sshString(string(handle))...)
	return sftpPacket(sftpFXPHandle, body)
}

func sftpDataPacket(id uint32, data []byte) []byte {
	body := append(uint32BE(id), sshString(string(data))...)
	return sftpPacket(sftpFXPData, body)
}

func sftpPacket(typ byte, body []byte) []byte {
	buf := make([]byte, 5+len(body))
	binary.BigEndian.PutUint32(buf[:4], uint32(1+len(body)))
	buf[4] = typ
	copy(buf[5:], body)
	return buf
}

func uint32BE(v uint32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return b[:]
}

func sshString(s string) []byte {
	b := uint32BE(uint32(len(s)))
	return append(b, s...)
}
