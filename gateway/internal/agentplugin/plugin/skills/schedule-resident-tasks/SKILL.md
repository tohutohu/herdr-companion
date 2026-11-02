---
name: schedule-resident-tasks
description: 定期実行・監視・あとでの再確認は、このセッションから常駐タスク（hc resident）で実際に作れる。できないと答える前、cron・launchd・/loop・/schedule を使う前に読む。「毎朝」「1時間おきに」「新着があったら知らせて」「後でもう一度確認して」など、ユーザーがいない間に確認して必要なときだけ報告してほしい依頼で使う。既存の常駐タスクの変更・停止・報告が来ない調査にも使う。cron の文法の質問や CI など他システムの定期実行の編集には使わない。
---

# 常駐タスクを作る

Run は毎回新しい会話で動く。Memory・前回の結果・結果の JSON 形式は Gateway がプロンプトに入れるので、指示文には「何を調べ、何を変化とみなし、何を知らせるか」だけを書く。

1. `hc resident ls` で同じ目的のタスクが無いか確かめる。
2. 指示文をファイルに書く:

   ```text
   main の CI を `gh run list --branch main --limit 5` で確認する。
   前回より新しい失敗があるときだけ、失敗したジョブと原因の見立てを報告する。
   ```

3. 作る。定型の確認は小さいモデルで足りる:

   ```bash
   hc resident preview --weekly mon,thu@07:00          # 次の 3 回を確かめる
   hc resident create --name CI監視 --provider claude --model haiku --cwd DIR \
     --instruction-file task.md --weekly mon,thu@07:00
   ```

4. 1 回試してから終える。結果と次回の予定をユーザーに伝える:

   ```bash
   hc resident run CI監視       # → queued run RUN。Run の終わりを待つコマンドは無い
   hc resident log RUN          # 状態・結果・拒否された操作・警告。running の間は間を置いて見直す
   ```

予定は `--daily HH:MM`、`--weekly`、`--cron`、一度きりの `--once RFC3339`、手動だけの `--manual`。その他は `hc help resident`。

## 落とし穴

- 権限は既定のままにする。`--profile read_only`、細かい `--allow`、指示文での禁止（「編集しないで」など）は、ユーザーが求めたときだけ付ける。`--allow` を足すのは `hc resident log` に拒否として出た操作に限る。
- `--name` を付ける。省くと指示の 1 行目が名前になり、以後のコマンドで指定しづらい。
- 自動の起動は既定で 1 日 12 回まで。2 時間おきより頻繁な予定には `--max-runs-per-day` も上げる。
- 頻繁に見張るなら `--precheck 'CMD'` で安く絞る。exit 0 でエージェントを起動（標準出力がプロンプトに入る）、exit 1 は起動せず見送り、それ以外は失敗。標準入力に Memory の JSON が来る。CMD はサンドボックスの外で動く。
- エージェントが信頼していないフォルダは `--trust` が要る。プロジェクトの hooks と MCP が無人で動くので、ユーザーの依頼の範囲か確かめる。フォルダは workspaceRoots の中だけ。
- 定期実行で変化が無ければ、会話にも通知にも出ない。報告が来ないときは `hc resident runs TASK` で確かめる。
- Run が判断待ちなら `hc resident answer TASK "回答"`。報告の続きを話すなら `hc resident promote RUN` でセッションにする。
- Gateway の再起動で切れた Run は再送されず `needs_reconciliation` になる。状況を確かめて `hc resident dismiss RUN`。
