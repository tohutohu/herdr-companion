---
name: drive-sessions
description: このセッションから、この Mac の別の Claude Code / Codex セッションを hc CLI で起動・指示・監視し、質問や承認に答えられる。「別のセッションで」「他のエージェントに任せて」「あのセッションどうなった」「回答待ちに答えて」で使う。この会話の中で済むサブエージェントには使わない。定期実行は schedule-resident-tasks。
---

# 別のセッションを動かす

```bash
hc ls                                  # 一覧。"!" は回答待ち、"(you)" は自分
hc start --cwd DIR "指示"               # → started claude:<id>。Codex は --provider codex、軽い作業は --model haiku
hc until ID --timeout 9m               # 質問・承認・ターン終了・失敗まで待つ
hc read ID                             # 新着を読む
hc send ID "次の指示"
hc show ID                             # 保留中の質問と、そのまま使える answer コマンド
hc answer ID 2                         # 番号・ラベル・自由入力。承認は approve | deny
```

ID は短い前方一致でよい。並列の起動と合流、全セッションの見張りは `hc guide` を読む。フラグは推測せず `hc help COMMAND` で確かめる。

## 落とし穴

- `hc` を待つ間はシェルのタイムアウトに注意する。`--timeout 9m` には Bash の `timeout: 600000` を合わせるか、バックグラウンドで実行する。
- `hc until` の時間切れは終了コード 3 で、失敗ではない。表示された `continue:` のコマンドで待ち続ける。`hc send` の直後に呼んでよい（着手前の idle を終わりと誤認しない）。
- `completed` でも裏でサブエージェントやコマンドが動いていることがある。`hc ls` の `[N in background]` が目印で、終わると新しいターンが自動で始まる。結果まで待つなら `hc until ID --for settled`。
- `hc read` は読むたびに既読位置が進み、同じ内容は二度出ない。全体は `--all`、位置を動かさないなら `--peek`。
- `--cwd` を省くと今のディレクトリで起動する。別のプロジェクトでは必ず指定する。
- 答える前に `hc show` を見る。ユーザーが先にアプリで答えていることがある。
- ユーザーが操作中のセッションには、頼まれない限り送らない。`hc archive` はエージェントを止めるので、終わった作業にだけ使う。
- `not supported here` で答えられない質問は、`hc interrupt ID` で止めてから `hc send` で指示し直す。
- 自分自身のセッションへの操作は拒否される。常駐タスクの Run は `hc ls` に出ない。
- `hc` が無ければ入れる: `curl -fsSL -o ~/.local/bin/hc "https://github.com/tohutohu/herdr-companion/releases/latest/download/hc-darwin-$(uname -m | sed s/x86_64/amd64/)" && chmod +x ~/.local/bin/hc`
