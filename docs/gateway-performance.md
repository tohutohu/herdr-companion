# Gateway performance investigation (2026-10-04)

Macで動いているGatewayを、通常のクライアント通信中にCPU・allocation・heap・goroutineのpprofで調査した。実験用のエージェント操作やHerdr serverの再起動は行っていない。修正版はメニューバーのGateway Managerを再ビルドし、`/Applications/Herdr Companion Gateway.app`へ反映済み。

## 観測結果

比較前は、`GET /messages`の二重取得を修正済みで、Go 1.27.1と認証付きpprofを導入した版。比較後は、履歴取得の並行リクエスト共有・JSON v2の明示呼び出し・以下の変換／ログ読み取りの削減を加えた版。

| 指標 | 比較前 (12:40:04–12:40:34 JST) | 比較後 (12:56:42–12:57:12 JST) |
|---|---:|---:|
| 30秒のCPUサンプル合計 | 8.54秒 | 2.98秒 |
| 30秒の累計割り当て (`alloc_space`) | 5,845.58 MB | 1,517.57 MB |
| 通常HTTPリクエスト完了数 | 20 | 29 |
| HTTP所要時間の中央値／最大 | 5,110 / 9,058 ms | 1,359 / 2,169 ms |
| `/v1/sessions` 所要時間の中央値 | 7,517 ms (4件) | 960 ms (5件) |

MBはpprofの表示値。CPU時間は1コア分の秒数の合計であり、Mac全体のCPU使用率ではない。通常HTTPからはpprofを除外した。比較後の観測値はCPU時間約65%、割り当て約74%の減少となった。ただし時刻・エージェントの出力・HTTPの200/304の割合や起動からの経過時間が異なるため、固定負荷のA/Bベンチマークではない。

Heapの保持量は、比較後の12:57:12時点で約25 MB、12:59:38時点で約16 MB。短い観測の範囲では保持メモリが増え続ける兆候より、大きな一時割り当てが問題だった。これだけで長期的なリークを否定できるわけではない。

## 原因と修正

- **同じ履歴の重複取得**: `/messages`はSummaryとMessagesをまとめて取得・変換する。さらに一覧・通知監視・複数クライアントで同じthreadの読み取りが重なった場合も、進行中の1回のRPCを共有する。完了後の結果はキャッシュしないため、次の読み取りは最新の履歴を取得する。各クライアントのキャンセルは独立し、共有RPC自体には30秒の期限がある。
- **回答履歴ログの全読み直し**: 比較前の`bufio.ReadBytes`累計割り当ては約995 MB/30秒。追記時にrollout全体を再走査していた。ファイルごとに最後の完全な行までのoffsetと質問の解析状態を保持し、追記分だけ読む。未完の行は次回に再試行し、ファイルの置換・縮小・同サイズでのmtime変更時は読み直す。通常のrolloutは追記専用であることを前提とする。
- **一覧で捨てるツール出力の変換**: Summaryは末尾のユーザー／アシスタント本文だけを変換する。質問の探索ではツール出力フィールドを持たない型を使う。`pendingAsyncQuestions`の割り当ては約869 MBから約18.5 MB/30秒へ減った。
- **切り捨て前の巨大なrune配列**: `Truncate`は文字列全体を`[]rune`へ変換していた。必要な文字数まで走査し、切り出した接頭辞だけを返す。日本語・不正UTF-8・境界値の従来の出力をテストで確認した。
- **JSON復号**: 初回の修正ではCodexのRPC受信と履歴item解析に`encoding/json/v2`を明示的に使用した。その後のgoccy/go-json比較に基づく最終構成は下記の通り。送信側は既存のJSON形式を維持した。

4 MBの過去ログに短いレコードを追記する`BenchmarkAnsweredAppend`では、全走査が約5–9 ms / 8.5 MB割り当て、増分読み取りが約0.06 ms / 66 KB割り当てだった。質問への回答・未完の行・次の発言への紐づけ・以前に返した結果の不変性・縮小後のリセットをテストしている。

## 残る待ち時間とMacの負荷

修正後もCodexの`thread/read`には約1.3–2.3秒の呼び出しがある。26.7 MBの応答でも、結果のJSON復号は約20 ms、送信は1 ms未満だった。残りにはapp-serverの処理／待ち、転送、RPC受信ループの復号・配送、Macのスケジューリングが含まれる。GatewayのCPUプロファイルだけでは、これらを完全に分離できない。`slow codex rpc`ログにoperation・総時間・送信時間・結果復号時間・結果バイト数を記録するようにした。本文や認証情報は記録しない。

調査開始時のMacは10コアに対しload averageが約200、圧縮メモリ約27 GB、swap約18 GBだった。12:58の観測でもload average約22、物理メモリ使用約62/64 GB、圧縮メモリ約12 GBと余裕は小さい。一方、その瞬間のswap in/outの増加は0で、CPU idleも約35%あった。Mac全体の負荷は悪化要因になり得るが、Gateway側にも削減できる処理が実際にあった。

## odjsonの判断

[`github.com/mazrean/odjson`](https://pkg.go.dev/github.com/mazrean/odjson) v0.2.0を一時的に導入し、RPC・Thread・itemおよび質問探索用の型に生成したcodecを、Go 1.27.1の標準JSON v1/v2と比較した。入力は約2.36 MBのツール出力中心の合成履歴。

今回の型と入力では、標準v2よりRPC／Threadの復号が遅くなるケースが多かった。itemで時間が改善するケースもあったが割り当てが増え、生成コードの追加に見合う改善を確認できなかったため採用していない。生成コードと依存は除去済み。標準v1/v2の比較用`BenchmarkCodexJSON`は残している。別のデータ構造でも同じ結果になるとは限らない。

## goccy/go-jsonの追加比較と採用

[`github.com/goccy/go-json`](https://github.com/goccy/go-json) v0.11.2をGo 1.27.1 / Apple M1 Max上で比較した。標準v1/v2/goccyを同じ型・入力・新規の出力先で3回ずつ計測した中央値は以下の通り。通常の`Unmarshal`を使用し、入力を参照したままにする`DecodeNoCopyString`等のオプションは使用していない。

| 合成履歴の復号対象 | 標準v2の時間 | goccyの時間 | 標準v2 / goccyの割り当て |
|---|---:|---:|---:|
| Thread (約2.36 MB) | 3.33 ms | 2.27 ms | 2.63 / 2.64 MB |
| tool item (約74 KB) | 232 µs | 97 µs | 156 / 74 KB |
| 質問探索用projection | 93 µs | 58 µs | 96 / 131 bytes |
| 日本語・引用符・バックスラッシュ入りThread (約3.35 MB) | 8.81 ms | 5.12 ms | 3.41 / 3.45 MB |
| 同じ日本語入りtool item | 783 µs | 461 µs | 205 / 107 KB |

Threadや小さいprojectionの割り当ては若干増える一方、繰り返し変換する大きなtool itemの割り当てがほぼ半分になった。

本番の`ConvertThread`・`pendingAsyncQuestions`・`lastThreadText`を連続で実行する`BenchmarkCodexHistory`も追加した。約2.36 MBのツール履歴と末尾のアシスタント本文を使い、標準v2版とgoccy版の別のテスト実行ファイルを作成し、実行順を交互にして各3回、1秒ずつ計測した。

RPCの型付き結果・履歴item・軽量な探索へ広くgoccyを使用した合成ベンチマークでは31.73→16.94 ms、5.12→2.50 MB/opとなった。一方、この構成を実負荷で測ると、goccyの`RuntimeContext.SetInput`による作業バッファへのコピーだけで約250 MB/30秒の追加割り当てがあった。固定サイズの入力を繰り返すベンチマークはバッファを再利用しやすく、巨大なThreadと様々なitemを並行処理する実負荷のコストを十分に表していなかった。通常HTTP完了数も47→28件と異なり、総割り当て1,700→1,435 MB、CPU時間4.00→2.34秒だけを根拠に全体が改善したとは判断できない。大型RPCの結果復号は中央値22→9 msだったが、総RPC時間は3.9→6.2秒とむしろ増えた。13:33にはMacのload averageが約106に戻っていた。

最終的には **`convertItem`の完全なitem復号だけにgoccyを採用**し、RPC全体・Thread・質問／plan／末尾本文の軽量探索・画像取得には標準v2を維持した。この構成と標準v2のみの構成を改めて交互に比較すると、中央値は **27.33→18.22 ms（約33%短縮）、5.12→2.51 MB/op（約51%削減）**。このMacの負荷で絶対時間は変動するため、Gateway全体の応答がこの割合で改善するという意味ではない。

限定した最終構成の実負荷pprof (13:38:01–13:38:31) はCPU時間3.16秒、累計割り当て1,323.68 MB、保持heap約23 MBだった。goccyの`SetInput`割り当ては **約250→24 MB/30秒**へ減り、軽量な探索や巨大なThreadで生じていた余分なコピーを抑えられた。この窓の通常HTTPは26件、所要時間中央値2.74秒、最大10.08秒で、エラーはなかった。前の窓とは処理数・出力・起動からの時間が異なり、秒単位のCodex応答待ちが解消したとは言えない。残る割り当ての約79%はRPCのRawMessageコピーとWebSocket受信の`io.ReadAll`だった。

RPCの入口で結果の内部まで重複キー・不正UTF-8を拒否することをテストしている。goccyは標準`encoding/json`との互換性を目指すライブラリで、標準v2とすべての入力で同じ意味になるわけではない。既存の正常なCodex fixtureについて、Thread・RPC・item・画像／質問フィールドの値の一致、入力バッファとdecoderの再利用後も値が保持されること、壊れたJSONの拒否、従来のgolden出力を確認した。

Go全テスト・vet・Codexのrace detectorは成功し、goccyを含むGateway Managerを再ビルド・署名検証して、このMacへ反映した。ベンチマークと追加の生pprofは`/private/tmp/herdr-go-json-evaluation/`に保存している。

## 再計測

pprofはGatewayのAPIと同じBearer認証が必要。認証なしでは401になる。以下はこのMacの接続先で、tokenを引数や標準出力へ出さずに30秒採取する例。

```bash
python3 - <<'PY'
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor
import json, os, urllib.request

cfg = json.loads((Path.home() / '.config/herdr-mobile/desktop/config.json').read_text())
out = Path('/private/tmp/herdr-gateway-profile')
out.mkdir(mode=0o700, exist_ok=True)
out.chmod(0o700)
base = os.environ.get('GATEWAY_URL', 'http://100.99.15.34:8765')
def capture(name, route):
    request = urllib.request.Request(base + route,
        headers={'Authorization': 'Bearer ' + cfg['authToken']})
    data = urllib.request.urlopen(request, timeout=45).read()
    with os.fdopen(os.open(out / name, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), 'wb') as f:
        f.write(data)
    print(name)
with ThreadPoolExecutor(max_workers=2) as pool:
    jobs = [pool.submit(capture, name, route) for name, route in [
        ('cpu.pprof', '/debug/pprof/profile?seconds=30'),
        ('allocs.pprof', '/debug/pprof/allocs?seconds=30'),
    ]]
    for job in jobs:
        job.result()
capture('heap.pprof', '/debug/pprof/heap')
capture('goroutines.txt', '/debug/pprof/goroutine?debug=2')
PY

cd gateway
env -u GOROOT go tool pprof -top -nodecount=20 \
  '/Applications/Herdr Companion Gateway.app/Contents/Resources/herdr-mobile-gateway' \
  /private/tmp/herdr-gateway-profile/cpu.pprof
env -u GOROOT go tool pprof -top -alloc_space -nodecount=20 \
  '/Applications/Herdr Companion Gateway.app/Contents/Resources/herdr-mobile-gateway' \
  /private/tmp/herdr-gateway-profile/allocs.pprof
env -u GOROOT go test ./internal/providers/codex -run '^$' \
  -bench 'Benchmark(CodexJSON|CodexHistory|AnsweredAppend)' -benchmem -count=3
```

今回の生プロファイルは`/private/tmp/herdr-gateway-pprof-20261004/`に保存した。プロファイルとgoroutine dumpは内部パス等を含むためGitへは追加していない。比較前の実行ファイルも同じディレクトリに保存している。Mac Gatewayの再ビルド・署名検証、Go全テスト・vet、Codex/model/APIのrace detectorは成功した。
