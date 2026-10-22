package main

import "testing"

func Test履歴キャッシュの設定値は0か範囲内のMBだけを受け付ける(t *testing.T) {
	for _, ok := range []string{"0", "64", "2048", "65536"} {
		if _, err := parseHistoryCacheMB(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-1", "63", "65537", "2GB", "1.5"} {
		if _, err := parseHistoryCacheMB(bad); err == nil {
			t.Errorf("%q を受け付けた", bad)
		}
	}
}
