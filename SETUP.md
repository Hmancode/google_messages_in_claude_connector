# Setting up gm-reader on Windows

This guide assumes no programming experience. It takes about 15 minutes.

**What you need:**

- Your Windows laptop, with **Claude Desktop** (or Claude Code) installed
- Your Android phone, using **Google Messages** as its texting app, turned on and online
- **Firefox**, installed from [mozilla.org](https://www.mozilla.org/firefox/). It's only needed for one step; Chrome often doesn't work for that step.

Before you start, read the [Risks section of the README](README.md#risks). In short: pairing gives gm-reader access to every conversation in your Google Messages, and it uses an unofficial Google protocol.

---

## 1. Download gm-reader

1. Go to <https://github.com/Hmancode/google_messages_in_claude_connector/releases> and open the newest release.
2. Under **Assets**, click **gm-reader.exe** to download it.
3. Your browser may warn that the file "isn't commonly downloaded." That's normal for new programs that aren't signed by a company. Choose **Keep** (in Edge: click the **…** next to the download, then **Keep**, then **Keep anyway**).

*Prefer to build it yourself?* See "Building from source" in the README, then continue from step 2.

## 2. Put it in a permanent folder

Claude will run gm-reader from wherever you put it, so pick a place where it can stay.

1. Open **File Explorer** (the folder icon on the taskbar).
2. Click **This PC** in the left panel, then double-click **Local Disk (C:)**.
3. Right-click an empty area, choose **New → Folder**, and name it `Tools`.
4. Open `Tools`, create another folder inside it named `gm-reader`.
5. Move `gm-reader.exe` from your **Downloads** folder into `C:\Tools\gm-reader\`.

The full path is now `C:\Tools\gm-reader\gm-reader.exe`.

## 3. Open PowerShell in that folder

1. In File Explorer, open `C:\Tools\gm-reader`.
2. Click the address bar at the top (where it says `This PC > Local Disk (C:) > Tools > gm-reader`). It turns into editable text.
3. Type `powershell` and press **Enter**. A blue or black window opens, already in the right folder.

To check it worked, type this and press **Enter**:

```powershell
.\gm-reader.exe version
```

**If Windows shows "Windows protected your PC"** (SmartScreen): click **More info**, then **Run anyway**. Windows shows this for new programs that aren't signed by a company. You only need to do it once.

## 4. Pair with your phone

In the PowerShell window, type:

```powershell
.\gm-reader.exe pair
```

gm-reader prints instructions. Here they are in more detail.

**a. Open a Firefox private window.** Open Firefox and press **Ctrl + Shift + P**. A purple-themed window opens; that's the private window.

**b. Sign in.** Copy this address, paste it into the private window's address bar, and press **Enter**:

```
https://accounts.google.com/AccountChooser?continue=https://messages.google.com/web/config
```

Sign in with the **same Google account your phone's Google Messages uses**. (To check on the phone: open Google Messages, tap your profile picture in the top right; the account is shown at the top.) Sign in to **only that one account** in this window.

After signing in, Google sends you to a page full of code-like text that starts with something like `["2026…`. That's the right page. **Don't go to any other page in this window.**

> Going straight to `messages.google.com/web/config` without this link doesn't work: that page loads even when you're signed out, and then there are no cookies to copy.

**c. Open the developer tools.** Press **F12**. A panel opens at the bottom or side of the window. Click the tab named **Network**.

**d. Reload.** Press **F5**. A list of rows fills the Network panel. Near the top is a row whose **File** column says `config`.

**e. Copy the request.** Right-click the `config` row, hover over **Copy Value**, and click **Copy as cURL (POSIX)**.

**f. Go back to PowerShell** and press **Enter**. gm-reader reads what you copied straight from the clipboard, so you don't need to paste anything. It should say something like:

```
Found the cookies gm-reader needs (APISID, HSID, OSID, SAPISID, SID, SSID, __Secure-1PSIDTS).
```

If it says cookies are missing, gm-reader also says what it found on the clipboard. Most often the window wasn't signed in: close it, open a new private window, and start again from step b using the sign-in link. Then run `.\gm-reader.exe pair` again.

**g. Tap the emoji on your phone.** gm-reader shows an emoji, for example 🦊. Your phone should show a Google Messages notification or screen asking you to confirm a new device. Tap the **same emoji** there.

- If PowerShell shows a box instead of the emoji, open the link gm-reader prints under it to see the picture.
- If nothing appears on the phone, open Google Messages on the phone and look for the prompt there.

When it works, PowerShell says **Paired with your phone.**

**h. Choose whether Claude may send.** gm-reader asks:

```
Let Claude send messages, react, start chats and send files? [y/N]:
```

- Type `n` (or just press Enter) for **read-only**. Claude can read and search but never send anything.
- Type `y` to allow sending. Every message, new chat and file will then pop up a **Yes/No box on this laptop** first, showing who it's going to and what it says. Nothing is sent unless you click **Yes**. (Reactions don't show the box.)

You can change this any time later (see [below](#changing-what-claude-is-allowed-to-do)).

**i. Close the Firefox private window.** Just close it with the **X**. **Don't click "Sign out"**: signing out cancels the cookies gm-reader just saved, and you'd have to repeat this step.

## 5. Connect gm-reader to Claude

At the end of pairing, gm-reader asks:

```
Add gm-reader to Claude Desktop's settings now? [y/N]:
```

Type `y`. gm-reader adds itself to Claude Desktop's config file and keeps a backup of the old file.

(If you said no, run `.\gm-reader.exe setup-claude` any time to do the same thing.)

**Then fully restart Claude Desktop.** Closing the window isn't enough:

1. Find Claude's icon in the **system tray**, at the bottom right of the screen near the clock. You may need to click the **^** arrow to see hidden icons.
2. Right-click the Claude icon and choose **Quit**.
3. Open Claude Desktop again from the Start menu.

**Using Claude Code too?** Run this once in PowerShell (it's also printed by `setup-claude`):

```powershell
claude mcp add --transport stdio --scope user gm-reader -- "C:\Tools\gm-reader\gm-reader.exe" serve
```

### Adding it by hand (only if the automatic step failed)

1. In Claude Desktop, open **Settings → Developer → Edit Config**. This opens the config file Claude actually reads, which matters if Claude came from the Microsoft Store. Open `claude_desktop_config.json` in Notepad. (Without Claude's menu: press **Windows + R**, type `notepad %APPDATA%\Claude\claude_desktop_config.json`, and press **Enter**.)
2. If the file is empty, paste this. If it already has an `"mcpServers"` section, add only the `"gm-reader"` part inside it.

   ```json
   {
     "mcpServers": {
       "gm-reader": {
         "command": "C:\\Tools\\gm-reader\\gm-reader.exe",
         "args": ["serve"]
       }
     }
   }
   ```

   Note the **double backslashes** (`\\`) in the path. JSON needs them.
3. Save (**Ctrl + S**), close Notepad, and fully restart Claude as described above.

## 6. Try it

In a new Claude chat, ask:

> list my group chats

The first time Claude uses a gm-reader tool, Claude asks for permission:

- For read tools (`list_conversations`, `read_messages`, `search_messages`, `search_contacts`, `pairing_status`), "always allow" is fine.
- For `send_message`, `send_attachment` and `start_chat`, choose **allow once** each time, so you always get a chance to check. The laptop's Yes/No box still appears too.

More things to try:

- "What did people say in the climbing group chat today?"
- "Search my messages for 'dinner reservation'."
- "Is gm-reader still connected?" (this runs `pairing_status`)

The first sync after pairing takes a minute or two while gm-reader downloads recent messages from your 50 most recent chats.

## Keeping it working

- **Keep your phone on and online.** gm-reader reads through your phone, the same way messages.google.com does.
- **Open Claude at least once a week.** Google drops the pairing after about 14 days without contact. Ask "check gm-reader status" now and then; it warns when 4 or fewer days are left.
- **Don't open messages.google.com in a browser while you use gm-reader.** Google allows one active web session, so the browser would take over. If that happens, close the browser tab and restart Claude.
- **If Claude says the Google cookies stopped working**, run this in PowerShell from `C:\Tools\gm-reader` and repeat step 4a–4f (no emoji needed this time):

  ```powershell
  .\gm-reader.exe pair --refresh-cookies
  ```

- **If Google removed the pairing**, run `.\gm-reader.exe pair` again.

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

## Unpairing and uninstalling

1. **On your phone:** open Google Messages → tap your profile picture → **Device pairing** → remove the device listed for gm-reader.
2. **Delete gm-reader's data:** in PowerShell in `C:\Tools\gm-reader`, run:

   ```powershell
   .\gm-reader.exe forget
   ```

   Type `yes` when asked. This deletes the pairing, the message cache and the logs. (Or delete the folder `%LOCALAPPDATA%\gm-reader` yourself.)
3. **Remove it from Claude:** open `%APPDATA%\Claude\claude_desktop_config.json` in Notepad as in step 5, delete the `"gm-reader"` entry, save, and restart Claude. For Claude Code, run `claude mcp remove gm-reader`.
4. **Delete the program:** delete the folder `C:\Tools\gm-reader`.

## Troubleshooting

| What you see | What to do |
| --- | --- |
| Claude doesn't mention gm-reader or its tools | Fully quit Claude from the system tray and reopen it. Check the path in the config file matches where `gm-reader.exe` is. |
| "No Google sign-in cookies were found" | The private window wasn't signed in. Use the sign-in link in step 4b, not messages.google.com directly. |
| "Google did not accept the cookies" | Redo step 4 in a fresh Firefox private window, signed in to the right account only. |
| "No phone found" / "your phone isn't ready" | Open Google Messages on the phone, make sure it's the default SMS app and online, and try again. |
| Nothing appears on the phone to tap | Open Google Messages on the phone; the prompt may be inside the app. Check the phone and laptop use the same Google account. |
| Answers say "local cache (phone not reachable)" | The phone is off or offline, or messages.google.com is open somewhere. Ask Claude to check gm-reader's status. |
| The Yes/No box never appeared and the send was cancelled | The box may be behind other windows. Look for it on the taskbar. It cancels itself after 2 minutes. |
| Search doesn't find an old message | Search covers synced messages: by default the latest 100 of your 50 most recent chats, plus anything Claude has read. Ask Claude to read further back in that chat first. |

The log file is at `%LOCALAPPDATA%\gm-reader\gm-reader.log`. It never contains message text, so it's safe to share when asking for help.
