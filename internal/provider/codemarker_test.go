package provider

import (
	"strings"
	"testing"
)

func TestCodeMarker(t *testing.T) {
	yes := []struct{ body, code string }{
		{`{"code":11115,"msg":"x"}`, "11115"},                // 紧凑数字
		{`{"code": 11115, "msg":"x"}`, "11115"},              // 美化空白
		{`{"code":"11115"}`, "11115"},                        // 字符串码
		{`{"code": "11115"}`, "11115"},                       // 空白 + 字符串
		{`{'code':11115}`, "11115"},                          // 单引号
		{`{"a":{"code":11135,"msg":"y"},"code":0}`, "11135"}, // 嵌套、后随逗号
		{`{"code":14018}`, "14018"},                          // 末尾无闭合
		{`{"error":{"code":"11101","message":"cannot unmarshal"}}`, "11101"},
	}
	for _, c := range yes {
		if !CodeMarker(strings.ToLower(c.body), c.code) {
			t.Errorf("应命中 %s: %s", c.code, c.body)
		}
	}
	no := []struct{ body, code string }{
		{`{"code":111150,"msg":"x"}`, "11115"},   // 前缀误命中
		{`{"code":"11115abc"}`, "11115"},         // 码后紧跟字母
		{`{"request_id":"a11115b7c3"}`, "11115"}, // 裸数字出现在其他值里
		{`{"msg":"used 11115 tokens"}`, "11115"}, // 文本里含这五个数字
		{`{"msg":"11115"}`, "11115"},             // 只有 msg 命中
		{``, "11115"},
		{`{"ret":"11115"}`, "11115"}, // 不是 code 键
	}
	for _, c := range no {
		if CodeMarker(strings.ToLower(c.body), c.code) {
			t.Errorf("不应命中 %s: %s", c.code, c.body)
		}
	}
}
