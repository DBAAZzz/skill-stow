package library

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"skillmanager/internal/security"
)

// 安全检查的本地数据，放在应用数据目录的 security/ 下：
//
//	key                      计算发现指纹的随机密钥，0600
//	reports/<id>/current.json  最近一次结束的报告与当时的元信息清单摘要
//	reports/<id>/previous.json 上一份完整报告（重扫失败后还能看历史）
//	reports/<id>/acks.json     “已看过”标记
//	reports/<id>/ignored.json  忽略“没检查完”的签名
//
// 目录 0700，文件 0600，先写临时文件再原子替换。不存源码、原始证据或密钥。

// securityRecord 是一份落盘的报告。Root 是实际目录，用来核对 id 没撞。
type securityRecord struct {
	Root   string          `json:"root"`
	Meta   string          `json:"meta,omitempty"` // 读取时的元信息清单摘要；没读全为空
	Report security.Report `json:"report"`
}

// SecurityAck 是一条“已看过”标记。
type SecurityAck struct {
	Key         string          `json:"key"`
	Notice      security.Notice `json:"notice"`
	Rule        string          `json:"rule"`
	RuleVersion int             `json:"ruleVersion"`
	Content     string          `json:"content,omitempty"` // 高风险：标记时整个 Skill 的内容摘要
}

func securityDir(home string) string { return filepath.Join(appDir(home), "security") }

func rootID(root string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:10])
}

func reportDir(dir, root string) string { return filepath.Join(dir, "reports", rootID(root)) }

// writePrivate 原子写入一个只有本人可读的 JSON 文件。
func writePrivate(file string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}

// loadRecord 读出一份报告；不存在、读不了、解析不了或不是这个目录的都
// 返回 nil。坏掉的文件挪到旁边，界面显示“无法核实”，不当作没有风险。
func loadRecord(file, root string) (*securityRecord, bool) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, false
	}
	var r securityRecord
	if err := json.Unmarshal(b, &r); err != nil || r.Root != root {
		os.Rename(file, file+corruptMark+"x")
		return nil, true
	}
	return &r, false
}

// ignoredFile 是“忽略没检查完”的落盘格式。
type ignoredFile struct {
	Sig string `json:"sig,omitempty"`
}

func loadIgnored(file string) string {
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	var f ignoredFile
	if json.Unmarshal(b, &f) != nil {
		return ""
	}
	return f.Sig
}

func loadAcks(file string) []SecurityAck {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var acks []SecurityAck
	if json.Unmarshal(b, &acks) != nil {
		return nil
	}
	return acks
}

// loadKey 读出指纹密钥；没有或不对就生成一把新的。密钥换了，旧的
// “已看过”标记全部失效（当作没看过），不报错。
func loadKey(dir string) []byte {
	file := filepath.Join(dir, "key")
	if b, err := os.ReadFile(file); err == nil && len(b) == 32 {
		return b
	}
	k := make([]byte, 32)
	rand.Read(k)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err == nil {
			tmp := file + ".tmp"
			if os.WriteFile(tmp, k, 0o600) == nil {
				os.Rename(tmp, file)
			}
		}
	}
	return k
}

// fingerprinter 用本机密钥对发现的各部分做 HMAC-SHA256，部分之间带长度，
// 避免拼接歧义。
func fingerprinter(key []byte) security.Fingerprinter {
	return func(parts ...string) string {
		m := hmac.New(sha256.New, key)
		var n [8]byte
		for _, p := range parts {
			binary.BigEndian.PutUint64(n[:], uint64(len(p)))
			m.Write(n[:])
			m.Write([]byte(p))
		}
		return hex.EncodeToString(m.Sum(nil)[:16])
	}
}
