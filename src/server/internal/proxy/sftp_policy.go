package proxy

import (
	"path"
	"strings"
)

// SFTP 打开标志，与 draft-ietf-secsh-filexfer 一致。
const (
	sftpFlagRead   uint32 = 0x00000001
	sftpFlagWrite  uint32 = 0x00000002
	sftpFlagAppend uint32 = 0x00000004
	sftpFlagCreat  uint32 = 0x00000008
	sftpFlagTrunc  uint32 = 0x00000010
)

const sftpWriteFlags = sftpFlagWrite | sftpFlagAppend | sftpFlagCreat | sftpFlagTrunc

// sftpPolicy 是一次 SFTP 会话的传输限制。
// can_file 为假时两个方向都关。节点配置可以再单独关掉上传或下载，并限制大小和扩展名。
type sftpPolicy struct {
	AllowUpload   bool
	AllowDownload bool
	MaxBytes      int64
	DenyExt       map[string]struct{}
}

type sftpOpenDecision struct {
	Direction string
	Action    string
	Reason    string
}

func decideSFTPOpen(pol sftpPolicy, filePath string, flags uint32) sftpOpenDecision {
	upload := flags&sftpWriteFlags != 0
	download := flags&sftpFlagRead != 0
	direction := "download"
	if upload {
		direction = "upload"
	}
	if !upload && !download {
		return sftpOpenDecision{Direction: direction, Action: "deny", Reason: "无法识别的打开方式"}
	}
	ext := fileExt(filePath)
	if ext != "" {
		if _, ok := pol.DenyExt[ext]; ok {
			return sftpOpenDecision{Direction: direction, Action: "deny", Reason: "扩展名不允许：" + ext}
		}
	}
	if upload && !pol.AllowUpload {
		return sftpOpenDecision{Direction: "upload", Action: "deny", Reason: "当前授权或节点策略不允许上传"}
	}
	if download && !upload && !pol.AllowDownload {
		return sftpOpenDecision{Direction: "download", Action: "deny", Reason: "当前授权或节点策略不允许下载"}
	}
	if upload && download && !pol.AllowDownload {
		return sftpOpenDecision{Direction: "upload", Action: "deny", Reason: "当前授权或节点策略不允许下载"}
	}
	return sftpOpenDecision{Direction: direction, Action: "allow"}
}

func fileExt(filePath string) string {
	base := filePath
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	ext := strings.ToLower(path.Ext(base))
	if ext == "." {
		return ""
	}
	return ext
}

func fileBase(filePath string) string {
	base := filePath
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSpace(base)
	if base == "" || base == "." || base == "/" {
		return filePath
	}
	if len(base) > 512 {
		return base[:512]
	}
	return base
}

func extSet(list []string) map[string]struct{} {
	if len(list) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(list))
	for _, item := range list {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		if !strings.HasPrefix(item, ".") {
			item = "." + item
		}
		out[item] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *Server) filePolicy(canFile bool) sftpPolicy {
	upload, download := true, true
	var max int64 = 100 << 20
	var deny []string
	if s != nil && s.cfg != nil {
		if strings.EqualFold(strings.TrimSpace(s.cfg.Proxy.SFTPUpload), "deny") {
			upload = false
		}
		if strings.EqualFold(strings.TrimSpace(s.cfg.Proxy.SFTPDownload), "deny") {
			download = false
		}
		if s.cfg.Proxy.SFTPMaxBytes > 0 {
			max = s.cfg.Proxy.SFTPMaxBytes
		}
		deny = s.cfg.Proxy.SFTPDenyExt
	}
	if !canFile {
		upload = false
		download = false
	}
	return sftpPolicy{
		AllowUpload: upload, AllowDownload: download, MaxBytes: max, DenyExt: extSet(deny),
	}
}
