---
name: reply-in-app
description: The user reads this conversation in the Herdr Companion app on a phone or Mac and cannot see the terminal or localhost. Read before showing an image, HTML or other output, or pointing to a dev server or preview. Use for "show me", "let me see it on my phone", "make it openable", checks while the user is away, and requests to build something to look at. Not inside a resident task run.
---

# Showing things to the user in the app

## Images

Opening an image with a tool shows it in the conversation. No need to add its path.

- Claude Code: open it with Read. Files outside the cwd are shown too.
- Codex: open it with `view_image`. Only files inside the cwd are shown; copy one from outside into the cwd first.
- DeepSeek Harness: open it with its file-reading tool, or write its path in the reply as below.

## HTML, Markdown and other files

A path written in the reply text gets a file chip the user can tap to open. A chip appears when:

- the file exists inside the cwd, is a regular file and has an extension (relative or absolute path; `src/a.go:42` points at a line)
- it is saved before the reply is written
- the message has at most 20 of them

To show something interactive or a diagram, make one HTML file in the cwd. The app renders it in a WebView and loads its relative CSS, JS and images.

Claude Code sessions may also have the `Artifact` tool, which publishes a page to the user's claude.ai account. Use it when the user asks for an artifact or wants to review a design there and leave comments (`ArtifactComments` reads them); put the page's link in the reply. Otherwise prefer the HTML file, which stays on this Mac.

## Pitfalls

- Files outside the cwd, such as `/tmp`, get no chip.
- `localhost` URLs do not open on the phone. Do not start a server and point to it; make a file.
- `![alt](path)` does not become an image. Only `http(s)` links can be tapped.
- SVG is not shown as an image. Convert it to PNG or embed it in HTML.
- Files over 10 MiB and binaries are not previewed, only offered for download.
- A push notification shows about the first 300 characters of the last message. Put the conclusion and what the user should do first.
- The screen is narrow. Avoid wide tables and long code lines.
- The user's attachments (other than images) arrive as paths in an upload directory and are deleted after 24 hours. Copy what must stay into the cwd.
- To offer choices, use `AskUserQuestion` in Claude Code, `request_user_input` in Codex Plan mode, or DeepSeek Harness's question tool: they become cards the user answers with a tap. In Codex's default mode, ask in text and end the turn.
- Write to the user in their language (the language of their messages or settings), even though this skill is in English.
