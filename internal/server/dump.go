// dump.go 提供 env 门控的出站请求体落盘（诊断上游拒答类故障用）。
// 默认关闭：仅当 WILDWORK_DUMP_DIR 非空时生效，零开销差异只在开启时存在。
package server

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	dumpOnce sync.Once
	dumpDir  string
	dumpSeq  int
)

// dumpEnabled 首次调用读取环境变量；之后恒定。
func dumpEnabled() (string, bool) {
	dumpOnce.Do(func() { dumpDir = strings.TrimSpace(os.Getenv("WILDWORK_DUMP_DIR")) })
	return dumpDir, dumpDir != ""
}

// dumpChatBody 把**客户端原始**请求体（prepareChatBody 之前，可直接重放回
// /v1/chat/completions）写入 dump 目录，并在同目录 index.log 追加一行元数据
// （时间/序号/模型/字节数）。
// 放在改写前是刻意的：改写后的 body 已丢掉客户端原始字段（messageId/reasoning/
// traceId 等），拿它重放复现不了「客户端真实请求被上游拒绝」这类故障。
// 失败只打日志，绝不影响主链路。
func dumpChatBody(body []byte, model, ua string) {
	dir, ok := dumpEnabled()
	if !ok {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("dump mkdir %s: %v", dir, err)
		return
	}
	dumpSeq++
	safe := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, model)
	name := filepath.Join(dir, fmt.Sprintf("%04d_%s_%s.json", dumpSeq, time.Now().Format("150405.000"), safe))
	if err := os.WriteFile(name, body, 0o600); err != nil {
		log.Printf("dump write %s: %v", name, err)
		return
	}
	if f, err := os.OpenFile(filepath.Join(dir, "index.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		fmt.Fprintf(f, "%s seq=%04d model=%s bytes=%d ua=%q\n", time.Now().Format("2006-01-02 15:04:05.000"), dumpSeq, model, len(body), ua)
		f.Close()
	} else {
		log.Printf("dump index open: %v", err)
	}
}
