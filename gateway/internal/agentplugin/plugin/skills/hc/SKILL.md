---
name: hc
description: hc CLI で、Herdr Companion が同じ Mac で動かしている他のエージェントセッション（Claude Code / Codex）を一覧・起動・指示・待機・回答するとき、常駐タスク（定期実行）を作る・直すときに使う。「別のセッションに頼んで」「並列で走らせて」「あのセッションの質問に答えて」「毎朝チェックして」など。
---

# hc — 他のセッションを動かす

このセッションは Herdr Companion の Gateway が動かしている。Gateway は同じ Mac で他の Claude Code / Codex セッションも動かしていて、`hc` でそれらを見る・頼む・待つ・答えることができる。

## はじめに

- `hc ls` でセッション一覧（`!` は回答待ち、`(you)` は自分）。自分自身への送信・アーカイブ・中断・待機は拒否される。
- ヘルプは段階的に読む: `hc help`（索引）→ `hc help COMMAND`（1 コマンドの詳細）→ `hc guide`（委任・並列・監視の手順書）。フラグを推測せず、ヘルプで確かめる。
- `hc` が見つからなければ `~/.local/bin/hc` を探す。無ければ
  `curl -fsSL -o ~/.local/bin/hc https://github.com/tohutohu/herdr-companion/releases/latest/download/hc-darwin-arm64 && chmod +x ~/.local/bin/hc`。

## よく使う流れ

```bash
hc start --cwd ~/workspace/app "テストを直して"    # → started claude:<id>。--provider codex / --model / --worktree / --mode plan
hc until ID --timeout 9m                          # 質問・承認・ターン終了・失敗まで待つ
hc read ID                                        # 新着だけを読む
hc send ID "次はこれ" --wait --timeout 9m          # 頼んで、ターンの終わりまで待つ
hc show ID                                        # 保留中の質問と、そのまま使える answer コマンド
hc answer ID 2                                    # 選択肢の番号・ラベル・自由入力。承認は approve | approve_session | deny
hc until A B --all --for done --timeout 9m         # 並列で走らせたものの合流
```

- ID は短い前方一致でよい（`hc help ids`）。
- 待つときはシェルのタイムアウトを `--timeout` より長くする（Claude Code の Bash は `timeout: 600000` と `--timeout 9m`）か、バックグラウンドで実行する。
- 答える前に `hc show` / `hc read` で今の状態を見る。ユーザーが先にアプリで答えていることがある。
- ユーザーが自分で操作しているセッションには、頼まれない限り送らない。`hc archive` はエージェントを止めるので、終わった作業にだけ使う。
- 長い会話は `hc export ID` で Markdown に書き出して grep する。スクリプトから使うなら `--json`。

## 常駐タスク（`hc resident`）

Gateway が予定どおりに実行するタスク。詳しくは `hc help resident`。

```bash
hc resident create --provider claude --model haiku --cwd ~/workspace/app \
  --instruction-file task.md --weekly mon,thu@07:00 --trust
hc resident ls                      # "!" は判断待ち・要確認
hc resident run NAME                # 今すぐ実行
hc resident answer NAME "報告だけで"  # 判断待ちに答える
```

- 権限は既定（`workspace_write`）のままにする。`--profile read_only`・細かい `--allow`・指示文での禁止は、ユーザーが求めたときだけ付ける。
- エージェントが使える外部連携は、この Mac で設定済みの CLI と MCP だけ。
