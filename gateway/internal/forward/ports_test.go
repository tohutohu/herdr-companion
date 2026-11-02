package forward

import (
	"reflect"
	"testing"
)

func TestLsofの出力からlocalhostで届くポートだけを拾う(t *testing.T) {
	out := `p644
cControlCenter
f10
n*:7000
f11
n*:7000
p818
cfigma_agent
f9
n127.0.0.1:44950
p900
cnode
f20
n[::1]:5173
f21
n127.0.0.1:5173
p1000
cherdr-mobile-gateway
f5
n100.99.15.34:8765
p1100
ctailscaled
f7
n100.99.15.34:41641
p1200
cpython3
f3
n*:8000
`
	got := parseListening(out, 1200)
	want := []Port{
		{Port: 5173, PID: 900, Command: "node"},
		{Port: 7000, PID: 644, Command: "ControlCenter"},
		{Port: 44950, PID: 818, Command: "figma_agent"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func Testプロセスの作業フォルダを読む(t *testing.T) {
	got := parseCwds("p900\nfcwd\nn/Users/me/work/app\np644\nfcwd\nn/\n")
	want := map[int]string{900: "/Users/me/work/app", 644: "/"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}
