package pricing

import (
	"math"
	"testing"
)

func near(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

func Test料金はモデルごとの単価とキャッシュの種別で決まる(t *testing.T) {
	r, ok := Lookup("claude-opus-5")
	if !ok {
		t.Fatal("claude-opus-5 の単価がない")
	}
	// 入力5 + 出力25 + 5分書き込み6.25 + 1時間書き込み10 + 読み出し0.5 ($/Mトークン)
	got := r.USD(Tokens{Input: 1_000_000, Output: 1_000_000,
		CacheWrite5m: 1_000_000, CacheWrite1h: 1_000_000, CacheRead: 1_000_000})
	if !near(got, 46.75) {
		t.Errorf("USD = %v, want 46.75", got)
	}
	// OpenAIはキャッシュ書き込みに追加料金がない。
	r, _ = Lookup("gpt-5.6-luna")
	if got := r.USD(Tokens{Input: 1_000_000, CacheRead: 1_000_000, Output: 1_000_000}); !near(got, 1.42) {
		t.Errorf("USD = %v, want 1.42", got)
	}
}

func Test長コンテキスト版と日付つきのモデルidも同じ単価になる(t *testing.T) {
	base, _ := Lookup("claude-opus-5")
	for _, id := range []string{"claude-opus-5[1m]", "claude-haiku-4-5-20251001"} {
		if _, ok := Lookup(id); !ok {
			t.Errorf("%s の単価がない", id)
		}
	}
	if r, _ := Lookup("claude-opus-5[1m]"); r != base {
		t.Errorf("1M版 = %+v, want %+v", r, base)
	}
}

func Test単価の分からないモデルは金額を出さない(t *testing.T) {
	if _, ok := Lookup("codex-auto-review"); ok {
		t.Error("未知のモデルに単価がついている")
	}
	if _, ok := Total(map[string]Tokens{"codex-auto-review": {Input: 1000, Output: 10}}); ok {
		t.Error("未知のモデルだけなのに合計が出ている")
	}
	// 分かるモデルが混ざっていれば、その分だけを合計する。
	usd, ok := Total(map[string]Tokens{
		"claude-haiku-4-5": {Input: 1_000_000, Output: 1_000_000},
		"gpt-reserve":      {Input: 1_000_000},
	})
	if !ok || !near(usd, 6) {
		t.Errorf("Total = %v, %v, want 6, true", usd, ok)
	}
}
