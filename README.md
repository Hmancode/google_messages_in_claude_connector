# gm-reader

gm-reader is a small, open-source [MCP](https://modelcontextprotocol.io) server that lets Claude read and search your **Google Messages** conversations (SMS, MMS and RCS, including group chats). It runs on your Windows laptop as a single `.exe` and talks to Claude Desktop or Claude Code over stdio, so it opens no network ports.

Reading is always on. Sending messages, reacting, starting chats and sending files are **off unless you turn them on**. When they are on, every message, new chat and file pops up a Yes/No box on your computer first.

**New here? Follow [SETUP.md](SETUP.md).** It walks through everything step by step.

> ⚠️ gm-reader uses Google's **unofficial** Messages for Web protocol (through [libgm](https://github.com/mautrix/gmessages/tree/main/pkg/libgm)). Google could break it at any time, and in theory could flag the account. Please read [Risks](#risks) before you pair.

## What Claude can do

| Tool | What it does | Needs |
| --- | --- | --- |
| `list_conversations` | Recent chats with name, participants, last message time, unread flag | always on |
| `read_messages` | Messages in one chat: sender, time, text, attachment type, reactions (media is not downloaded) | always on |
| `search_messages` | Full-text search of synced messages | always on |
| `search_contacts` | Look up a contact's number by name | always on |
| `pairing_status` | Connected or not, last sync, days until re-pairing is needed, warnings | always on |
| `send_message` | Send text to an existing chat | `send`, plus a Yes in the confirmation box |
| `send_attachment` | Send a file from your computer (up to 100 MB) | `attachments`, plus a Yes in the box |
| `start_chat` | Start a new chat or group, optionally with a first message | `new-chats`, plus a Yes in the box |
| `react_to_message` | Add or remove an emoji reaction | `react` (no box; low risk and reversible) |

Turned-off actions aren't just blocked: their tools don't exist, so Claude never sees them. Change them with `gm-reader settings` (see [SETUP.md](SETUP.md#changing-what-claude-is-allowed-to-do)).

## How it stays safe

**Only a short, reviewed list of libgm functions can be called.** All access to libgm goes through one package (`internal/gm`). A build-breaking test (`internal/guard`) parses the whole codebase and fails if:

- any other package imports libgm, or `internal/gm` exposes libgm objects;
- any code calls a banned libgm function: `MarkRead`, `DeleteMessage`, `DeleteConversation`, `UpdateConversation`, `SetTyping`, `UpdateSettings`, `RegisterPush`, `SetActiveSession`, `Unpair`, `UnpairBugle`, `UnpairGaia`, the old QR-login functions, or the media download functions;
- a write function (`SendMessage`, `SendReaction`, `GetOrCreateConversation`, `UploadMedia`) is called anywhere except `internal/gm/write.go`;
- libgm gains a new function that the guard hasn't classified yet, so libgm upgrades get reviewed.

A separate grep (`scripts/check-banned.sh`) runs in CI as a second layer.

**Write actions go through two gates.** `write.go` first checks your settings, then (for messages, files and new chats) shows a Windows Yes/No box with the exact recipient and content. Nothing is sent unless you click **Yes**. "No" is the default button, and the box cancels itself after 2 minutes. Claude can't click it, and neither can a malicious text message that tries to talk Claude into sending something. Invisible text-direction characters are shown as `[hidden-char]` so the box can't be disguised. Sending only works into conversations that already exist, unless `new-chats` is on. Write tools are also marked as non-read-only and destructive, so the Claude app should ask you before using them. Don't choose "always allow" for them.

**Chats are never marked as read.** gm-reader never calls `MarkRead` and sends no read receipts or typing indicators.

**Message content is treated as untrusted.** Incoming texts might contain prompt-injection attempts ("ignore your instructions and…"). Every read tool's description and result tells Claude that message content is data written by other people, never instructions.

**Privacy**

- No telemetry. The only network traffic is to Google's Messages servers, and only after you pair.
- The pairing session is stored in `%LOCALAPPDATA%\gm-reader\session.bin`, encrypted with Windows DPAPI (user scope). Only your Windows account on this computer can decrypt it.
- The message cache (`cache.db`, SQLite) is **not encrypted by gm-reader**. It's protected only if your drive uses BitLocker or device encryption. To keep nothing on disk, use `serve --no-cache`: the cache then lives in memory and disappears when Claude closes.
- Logs (`gm-reader.log`) never contain message text, names or cookies. libgm's own logs pass through a filter that removes raw data fields. A test checks this with a canary message.

## Risks

- **The pairing is a credential.** It gives full access to every conversation in the account, like a password. Anyone who can run programs as your Windows user could use it.
- **Unofficial protocol.** This works the way messages.google.com does, but Google doesn't offer it as an API. It could stop working, and Google could in theory flag the account.
- **One active web session at a time.** When gm-reader connects, Google treats it as the active Messages for Web session, just as opening messages.google.com does. That happens inside libgm's connect step and can't be removed. If you then open Messages for Web in a browser, the browser takes over and gm-reader's live updates pause until Claude restarts. gm-reader never grabs the session back by itself.
- **The phone must stay on and online.** Messages are relayed through your phone. Google drops the pairing after about **14 days** without contact. `pairing_status` warns when 4 or fewer days are left.
- **Google cookies can expire** (for example if you sign out of Google in the browser you copied them from). Run `gm-reader pair --refresh-cookies`; your phone doesn't need to be re-paired.
- **Write actions are real.** A sent message can't be unsent. Keep the confirmation box on (the default) and read it before clicking Yes.

## Revoking access

1. On your phone: **Google Messages → your profile picture → Device pairing →** remove the device.
2. On your computer: run `gm-reader forget`, or delete the folder `%LOCALAPPDATA%\gm-reader\`.
3. Remove the `gm-reader` entry from Claude Desktop's config (`%APPDATA%\Claude\claude_desktop_config.json`), or run `claude mcp remove gm-reader` for Claude Code.

## Where it works

| Where | Works? |
| --- | --- |
| Claude Desktop on the paired laptop | ✅ |
| Claude Code on the paired laptop | ✅ |
| Claude chats linked to the laptop, while the Claude desktop app is open there | Should work, since those chats can use the laptop's local tools. Confirmation boxes appear on the laptop, so a send from your phone cancels after 2 minutes unless you're at the laptop |
| Regular chats in the Claude phone app or on claude.ai | ❌ for now (see below) |

Claude's phone and web apps can only use tools that are reachable at a public, login-protected web address. gm-reader is built so that adding such a mode later is a small change: the MCP server, the tools and the cache don't depend on how Claude connects. Moving to a cloud host needs:

1. a Streamable HTTP mode with sign-in (OAuth) in front of the existing server;
2. a way to protect the session file on that host (DPAPI is Windows-only);
3. `gm-reader settings --confirm-mode claude-app`, because a headless server can't show a Yes/No box. The Claude app's own per-tool approval prompt becomes the confirmation. Keep write tools on "ask every time".

## Building from source

Requires Go 1.26 or newer.

```powershell
git clone https://github.com/Hmancode/google_messages_in_claude_connector
cd google_messages_in_claude_connector
go test ./...
./scripts/check-banned.sh        # in Git Bash or WSL
$env:CGO_ENABLED="0"; go build -trimpath -o gm-reader.exe ./cmd/gm-reader
```

CI (`.github/workflows/ci.yml`) runs vet, all tests and the guard, the grep, and a smoke test on Linux and Windows. The smoke test starts `serve`, speaks MCP to it, and checks with `netstat` that it has no open ports. Tagging `v*` publishes `gm-reader.exe` and its SHA-256 checksum to GitHub Releases.

Pinned dependency: `go.mau.fi/mautrix-gmessages v0.2611.0` (libgm). QR pairing was removed upstream in v26.05 because Google discontinued it, so gm-reader uses Google-account pairing (cookies plus an emoji confirmed on the phone).

## Credits and license

- [mautrix-gmessages / libgm](https://github.com/mautrix/gmessages) by Tulir Asokan and contributors does all the protocol work.
- [fdsouvenir/gmcli](https://github.com/fdsouvenir/gmcli) and [MaxGhenis/openmessage](https://github.com/MaxGhenis/openmessage) were read for patterns; no code was copied.

gm-reader is licensed under the **GNU Affero General Public License v3.0** ([LICENSE](LICENSE)), because libgm is AGPL-3.0. If you modify gm-reader and let others use it over a network, you must offer them your source code.

Not affiliated with or endorsed by Google.
