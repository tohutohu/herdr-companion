# OpenCode fixtures

`v1.json` and `v2.json` are synthetic, non-sensitive records in the provider's
SQLite format. They cover user images, hidden reasoning, tool output and costs.
They are not transcripts from the user's account.

The v1 `message.data` / `part.data` format and the released official v2.0.9
`session_v2` / `session_message.data` format were checked against upstream:

- https://github.com/anomalyco/opencode/blob/v2.0.9/packages/core/src/session/sql.ts
- https://github.com/anomalyco/opencode/blob/v2.0.9/packages/schema/src/session-message.ts
- https://github.com/anomalyco/opencode/blob/v2.0.9/packages/schema/src/prompt.ts
- https://github.com/anomalyco/opencode/blob/v2.0.9/packages/protocol/src/groups/session.ts
- https://github.com/anomalyco/opencode/blob/v2.0.9/packages/schema/src/form.ts

`Test公式v2サーバーとの実接続` also exercises a disposable official v2 server.
It requires `HERDR_OPENCODE_TEST_URL`, `HERDR_OPENCODE_TEST_PASSWORD_FILE`,
`HERDR_OPENCODE_TEST_DB`, and `HERDR_OPENCODE_TEST_CWD`. Give the server isolated
HOME and XDG directories; never point this test at a user's server. It creates
and deletes its own session, answers a form, and queues a prompt with
`resume=false` (no model invocation).
