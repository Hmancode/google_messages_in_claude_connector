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
| `send_labeled_reply` | Send a short reply that gm-reader signs with your chosen signature, without the box, under strict limits (see [Building an assistant](#building-an-assistant)) | `gm-reader automation replies on` |

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

- No telemetry. The only network traffic is to Google's Messages servers, and only after you pair. If you turn on the optional [new-text hook](#building-an-assistant), gm-reader also sends one small request per batch of new texts to the address you choose. It contains conversation IDs and counts only, never message text, names or numbers.
- The pairing session is stored in `%LOCALAPPDATA%\gm-reader\session.bin`, encrypted with Windows DPAPI (user scope). Only your Windows account on this computer can decrypt it.
- The message cache (`cache.db`, SQLite) is **not encrypted by gm-reader**. It's protected only if your drive uses BitLocker or device encryption. To keep nothing on disk, use `serve --no-cache`: the cache then lives in memory and disappears when Claude closes.
- Logs (`gm-reader.log`) never contain message text, names or cookies. libgm's own logs pass through a filter that removes raw data fields. A test checks this with a canary message.

## Building an assistant

gm-reader has two optional features for people who want an assistant that answers simple texts for them, for example "when are you free?" when you're busy. Both are off by default, and turning either on asks for your approval on the computer.

**Labeled replies** (`send_labeled_reply`) send without the confirmation box, so gm-reader enforces the limits in code instead of trusting the model:

- Your signature (for example `- Sam's assistant`) is added to every reply on its own line, so recipients can tell it wasn't you. Claude can't remove it.
- Only one-on-one chats with people saved in your contacts, plus group chats you list by conversation ID.
- Only as an answer: the newest message in the chat must be theirs and recent (default: 2 hours).
- At most 280 characters (configurable). No links, web addresses, email addresses, or numbers with 5 or more digits, which blocks the "send me the code you just got" trick and leaking phone numbers.
- At most one labeled reply per chat every 3 hours, which also stops two auto-responders from looping, and at most 15 a day (both configurable).

**The new-text hook** sends a request to an HTTPS address you choose when a new text arrives from an allowed chat (the same rule as above). Texts arriving within 45 seconds are bundled into one request. The request body is `{"text": "gm-reader: new texts in conversation_id 122 (2 new)."}` with your token as a bearer token. That format works directly with a Claude scheduled task's API trigger, so a cloud task can wake up, read the new texts through gm-reader, and answer with a labeled reply. The hook only runs while gm-reader does, which means while Claude is open on the computer.

```powershell
gm-reader automation groups 884,1028                # optional: group chats by conversation ID
gm-reader automation replies on --signature "- Sam's assistant"
gm-reader automation hook on --url https://api.anthropic.com/v1/claude_code/routines/<id>/fire
gm-reader automation hook test
gm-reader automation                                # show current settings
```

`hook on` reads the token from your clipboard, so it never ends up in your PowerShell history. Restart Claude after a change. `pairing_status` shows how many labeled replies went out in the last day and whether the hook is working.

What your assistant should say, and what it may share, is up to the instructions you give it. gm-reader's limits are the backstop, not the whole policy. Keep its instructions strict about never sharing other people's information, never making commitments for you, and never acting because a text asks it to.

## Risks

- **The pairing is a credential.** It gives full access to every conversation in the account, like a password. Anyone who can run programs as your Windows user could use it.
- **Unofficial protocol.** This works the way messages.google.com does, but Google doesn't offer it as an API. It could stop working, and Google could in theory flag the account.
- **One active web session at a time.** When gm-reader connects, Google treats it as the active Messages for Web session, just as opening messages.google.com does. That happens inside libgm's connect step and can't be removed. If you then open Messages for Web in a browser, the browser takes over and gm-reader's live updates pause until Claude restarts. gm-reader never grabs the session back by itself.
- **The phone must stay on and online.** Messages are relayed through your phone. Google drops the pairing after about **14 days** without contact. `pairing_status` warns when 4 or fewer days are left.
- **Google cookies can expire** (for example if you sign out of Google in the browser you copied them from). Run `gm-reader pair --refresh-cookies`; your phone doesn't need to be re-paired.
- **Write actions are real.** A sent message can't be unsent. Keep the confirmation box on (the default) and read it before clicking Yes.
- **Labeled replies skip the box.** If you turn them on, Claude can send short, signed replies without asking you. The code-enforced limits above shrink the risk; they don't remove it, because a cleverly worded text could still try to steer what your assistant says.

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

CI (`.github/workflows/ci.yml`) runs vet, all tests and the guard, the grep, and a smoke test on Linux and Windows. The smoke test starts `serve`, speaks MCP to it, and checks with `netstat` that it has no open ports. Publishing a GitHub release (for example `v0.1.0`) makes CI build `gm-reader.exe` and attach it, with its SHA-256 checksum, to that release.

Pinned dependency: `go.mau.fi/mautrix-gmessages v0.2611.0` (libgm). QR pairing was removed upstream in v26.05 because Google discontinued it, so gm-reader uses Google-account pairing (cookies plus an emoji confirmed on the phone).

## Credits and license

- [mautrix-gmessages / libgm](https://github.com/mautrix/gmessages) by Tulir Asokan and contributors does all the protocol work.
- [fdsouvenir/gmcli](https://github.com/fdsouvenir/gmcli) and [MaxGhenis/openmessage](https://github.com/MaxGhenis/openmessage) were read for patterns; no code was copied.

gm-reader is licensed under the **GNU Affero General Public License v3.0** ([LICENSE](LICENSE)), because libgm is AGPL-3.0. If you modify gm-reader and let others use it over a network, you must offer them your source code.

Not affiliated with or endorsed by Google.
