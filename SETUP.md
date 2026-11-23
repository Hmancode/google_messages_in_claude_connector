# Setting up gm-reader on Windows

This guide assumes no programming experience. It takes about 5 minutes.

**What you need:**

- Your Windows laptop, with **Claude Desktop** (or Claude Code) installed
- Your Android phone, using **Google Messages** as its texting app, turned on and online
- **Firefox**. If it isn't installed, the setup opens the download page for you. (Chrome and Edge usually don't work for the sign-in step.)

Before you start, read the [Risks section of the README](README.md#risks). In short: pairing gives gm-reader access to every conversation in your Google Messages, and it uses an unofficial Google protocol.

---

## 1. Download and run it

1. Go to <https://github.com/Hmancode/google_messages_in_claude_connector/releases> and open the newest release.
2. Under **Assets**, click **gm-reader.exe** to download it.
3. Your browser may warn that the file "isn't commonly downloaded." That's normal for new programs that aren't signed by a company. Choose **Keep** (in Edge: click the **…** next to the download, then **Keep**, then **Keep anyway**).
4. Open your **Downloads** folder and **double-click gm-reader.exe**.
5. If Windows shows **"Windows protected your PC"**, click **More info**, then **Run anyway**. Windows shows this for programs that aren't signed by a company. You only see it for the downloaded copy.

A window opens and walks you through three steps. Each one is explained below.

*Prefer to build it yourself?* See "Building from source" in the README, then run `gm-reader.exe setup`.

## 2. What the setup does

### Step 1: Install

gm-reader copies itself to `%LOCALAPPDATA%\Programs\gm-reader` (a folder in your user account that OneDrive doesn't sync). Claude runs it from there. If Claude already uses gm-reader from another folder, such as `C:\Tools\gm-reader`, the setup updates that copy instead. You can delete the downloaded file afterwards.

### Step 2: Link your phone

1. **Sign in to Google.** Press **Enter** and Firefox opens a separate window just for this sign-in. Sign in with the **same Google account your phone's Google Messages uses**, and only that one. (To check on the phone: open Google Messages and tap your profile picture in the top right; the account is shown at the top.)
2. After signing in, Google shows a page full of code-like text that starts with something like `["2026…`. **That means you're done.** gm-reader picks up the sign-in and closes the window by itself. Don't click "Sign out" anywhere.
3. **Tap the emoji on your phone.** gm-reader shows an emoji, for example 🦊. Your phone shows a Google Messages prompt asking you to confirm a new device. Tap the **same emoji** there.
   - If the window shows a box instead of the emoji, open the link printed under it to see the picture.
   - If nothing appears on the phone, open Google Messages on the phone and look for the prompt there.
4. **Choose whether Claude may send.** gm-reader asks:

   ```
   Let Claude send messages, react, start chats and send files? [y/N]:
   ```

   - Type `n` (or just press Enter) for **read-only**. Claude can read and search but never send anything.
   - Type `y` to allow sending. Every message, new chat and file then pops up a **Yes/No box on this laptop** first, showing who it's going to and what it says. Nothing is sent unless you click **Yes**. (Reactions don't show the box.)

   You can change this any time (see [below](#changing-what-claude-is-allowed-to-do)).

**How the sign-in works:** the Firefox window uses a temporary Firefox profile that gm-reader creates in its own folder, so it doesn't touch your normal Firefox, its saved passwords or other accounts. gm-reader reads only the Google sign-in cookies that Messages for Web uses from that temporary profile, saves them encrypted with the pairing, and deletes the temporary profile. It never reads your own Firefox profile.

### Step 3: Connect to Claude

gm-reader adds itself to Claude Desktop's settings and keeps a backup of the old settings file. If Claude is open, it offers to **close and reopen Claude** so Claude loads gm-reader. Anything Claude is doing at that moment stops, so finish or wait first. (If you installed Claude from the Microsoft Store, open it again from the Start menu afterwards.)

If you say no, fully quit Claude yourself before using gm-reader. Closing the window isn't enough: find Claude's icon in the **system tray** near the clock (click **^** to see hidden icons), right-click it, choose **Quit**, and open Claude again.

**Using Claude Code too?** The setup prints a `claude mcp add ...` command at the end. Run it once in PowerShell.

## 3. Try it

In a new Claude chat, ask:

> list my recent text conversations

The first time Claude uses a gm-reader tool, Claude asks for permission:

- For read tools (`list_conversations`, `read_messages`, `search_messages`, `search_contacts`, `pairing_status`), "always allow" is fine.
- For `send_message`, `send_attachment` and `start_chat`, choose **allow once** each time, so you always get a chance to check. The laptop's Yes/No box still appears too.

More things to try:

- "What did people say in the climbing group chat today?"
- "Search my messages for 'dinner reservation'."
- "Is gm-reader still connected?" (this runs `pairing_status`)

The first sync after pairing takes a minute or two while gm-reader downloads recent messages from your 50 most recent chats.

## Updating

Download the new **gm-reader.exe** from the releases page and double-click it, as in step 1. It updates the installed copy (even while Claude is using the old one), keeps your pairing and settings (press **Enter** when it says the computer is already linked), and offers to restart Claude.

## Keeping it working

- **Keep your phone on and online.** gm-reader reads through your phone, the same way messages.google.com does.
- **Open Claude at least once a week.** Google drops the pairing after about 14 days without contact. Ask "check gm-reader status" now and then; it warns when 4 or fewer days are left.
- **Don't open messages.google.com in a browser while you use gm-reader.** Google allows one active web session, so the browser would take over. If that happens, close the browser tab and restart Claude.
- **If Claude says the Google sign-in stopped working**, double-click gm-reader.exe and type `s` when it says the computer is already linked. You sign in again; no emoji this time.
- **If Google removed the pairing**, double-click gm-reader.exe and type `p` to link your phone again.

## Running gm-reader commands

The commands below are typed in PowerShell, in the folder gm-reader is installed in:

1. Open **File Explorer** (the folder icon on the taskbar).
2. Click the address bar at the top, type `%LOCALAPPDATA%\Programs\gm-reader` (or `C:\Tools\gm-reader` if you set it up there), and press **Enter**.
3. Click the address bar again, type `powershell`, and press **Enter**. A PowerShell window opens in that folder.

To see the status without Claude:

```powershell
.\gm-reader.exe status
```

## Changing what Claude is allowed to do

See the current settings:

```powershell
.\gm-reader.exe settings
```

Turn actions on or off. The actions are `send`, `react`, `attachments` and `new-chats`, or `all`:

```powershell
.\gm-reader.exe settings --allow send,react
.\gm-reader.exe settings --deny all
```

Turning something **on** pops up a Yes/No box you have to approve, so no other program can quietly turn sending on. Turning things **off** needs no approval. **Restart Claude afterwards** for the change to take effect.

## Optional: automation

gm-reader can also sign and send short replies without the Yes/No box, and ping a URL when new texts arrive, for building an assistant. Both are off by default; the README's [Building an assistant](README.md#building-an-assistant) section explains the limits. To see the commands:

```powershell
.\gm-reader.exe automation
```

## If the automatic sign-in doesn't work

When the sign-in doesn't finish, the setup offers to try again or to copy the sign-in by hand (type `m`). You can also start that way with `.\gm-reader.exe setup --manual`. gm-reader opens a Firefox private window at the sign-in page. Then:

1. Sign in with the Google account your phone's Google Messages uses (only that one). You land on a page full of code-like text.
2. Press **F12** and click the **Network** tab.
3. Press **F5**. Near the top of the list is a row whose **File** column says `config`.
4. Right-click the `config` row, hover over **Copy Value**, and click **Copy as cURL (POSIX)**.

gm-reader notices the copy by itself and continues. If what you copied isn't right, it says why. Close the private window when gm-reader is done, without clicking "Sign out".

> Going straight to `messages.google.com/web/config` without signing in through the setup's link doesn't work: that page loads even when you're signed out, and then there are no cookies to copy.

## Connect Claude by hand

Only needed if step 3 couldn't update Claude's settings.

1. In Claude Desktop, open **Settings → Developer → Edit Config** and open `claude_desktop_config.json` in Notepad. (Or press **Windows + R**, type `notepad %APPDATA%\Claude\claude_desktop_config.json`, and press **Enter**; if Notepad asks to create the file, click **Yes**.)
2. If the file is empty, paste this, replacing `YOUR-NAME` with your Windows user name. If it already has an `"mcpServers"` section, add only the `"gm-reader"` part inside it.

   ```json
   {
     "mcpServers": {
       "gm-reader": {
         "command": "C:\\Users\\YOUR-NAME\\AppData\\Local\\Programs\\gm-reader\\gm-reader.exe",
         "args": ["serve"]
       }
     }
   }
   ```

   Note the **double backslashes** (`\\`) in the path. JSON needs them.
3. Save (**Ctrl + S**), close Notepad, and fully quit and reopen Claude.

## Unpairing and uninstalling

1. **On your phone:** open Google Messages → tap your profile picture → **Device pairing** → remove the device listed for gm-reader.
2. **Delete gm-reader's data:** in PowerShell in gm-reader's folder (see [Running gm-reader commands](#running-gm-reader-commands)), run:

   ```powershell
   .\gm-reader.exe forget
   ```

   Type `yes` when asked. This deletes the pairing, the message cache, the logs and any leftover sign-in folder. (Or delete the folder `%LOCALAPPDATA%\gm-reader` yourself.)
3. **Remove it from Claude:** open Claude's config file as in [Connect Claude by hand](#connect-claude-by-hand), delete the `"gm-reader"` entry, save, and restart Claude. For Claude Code, run `claude mcp remove gm-reader`.
4. **Delete the program:** delete the folder `%LOCALAPPDATA%\Programs\gm-reader` (or `C:\Tools\gm-reader`).

## Troubleshooting

| What you see | What to do |
| --- | --- |
| Double-clicking does nothing, or the window closes at once | Right-click gm-reader.exe → **Properties**; if there's an **Unblock** box at the bottom, tick it and click **OK**, then try again. |
| Your antivirus warns about gm-reader | Some antivirus programs flag any program that reads browser cookies. gm-reader only reads the temporary Firefox profile it created for the sign-in; its source code is public. You can use the [manual sign-in](#if-the-automatic-sign-in-doesnt-work) instead. |
| Firefox opened, you signed in, but gm-reader keeps waiting | Make sure you reached the page of code-like text. If Firefox is set to always use private browsing, the sign-in can't be picked up: type `m` to do it by hand. |
| Claude doesn't mention gm-reader or its tools | Fully quit Claude from the system tray and reopen it. Run the setup again to check Claude's settings. |
| "No Google sign-in cookies were found" | The window wasn't signed in. Sign in through the setup's window, not messages.google.com directly. |
| "Google did not accept the cookies" | Run the setup again and sign in to the right account only. |
| "No phone found" / "your phone isn't ready" | Open Google Messages on the phone, make sure it's the default SMS app and online, and try again. |
| Nothing appears on the phone to tap | Open Google Messages on the phone; the prompt may be inside the app. Check the phone and laptop use the same Google account. |
| Answers say "local cache (phone not reachable)" | The phone is off or offline, or messages.google.com is open somewhere. Ask Claude to check gm-reader's status. |
| The Yes/No box never appeared and the send was cancelled | The box may be behind other windows. Look for it on the taskbar. It cancels itself after 2 minutes. |
| Search doesn't find an old message | Search covers synced messages: by default the latest 100 of your 50 most recent chats, plus anything Claude has read. Ask Claude to read further back in that chat first. |

The log file is at `%LOCALAPPDATA%\gm-reader\gm-reader.log`. It never contains message text, so it's safe to share when asking for help.
