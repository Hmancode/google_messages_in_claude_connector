#!/usr/bin/env bash
# Second, independent layer next to internal/guard: a plain grep that fails if
# any banned libgm method is called anywhere, or if a write method is called
# outside internal/gm/write.go.
set -u
cd "$(dirname "$0")/.."

banned='MarkRead|DeleteMessage|DeleteConversation|UpdateConversation|SetTyping|UpdateSettings|RegisterPush|SetActiveSession|Unpair|UnpairBugle|UnpairGaia|StartUploadMedia|FinalizeUploadMedia|DoGaiaPairing|DownloadMedia|GetFullSizeImage|Reconnect'
write='SendMessage|SendReaction|GetOrCreateConversation|UploadMedia'
status=0

hits=$(grep -rnE "\.(${banned})\(" --include='*.go' . || true)
if [ -n "$hits" ]; then
  echo "FAIL: banned libgm methods are called:"
  echo "$hits"
  status=1
fi

hits=$(grep -rnE "\.(${write})\(" --include='*.go' . | grep -v '^./internal/gm/write.go:' || true)
if [ -n "$hits" ]; then
  echo "FAIL: write methods are called outside internal/gm/write.go:"
  echo "$hits"
  status=1
fi

hits=$(grep -rlE '^\s*([A-Za-z_.]+\s+)?"go.mau.fi/mautrix-gmessages' --include='*.go' . | grep -v '^./internal/gm/' || true)
if [ -n "$hits" ]; then
  echo "FAIL: libgm imported outside internal/gm:"
  echo "$hits"
  status=1
fi

[ $status -eq 0 ] && echo "OK: no banned libgm calls; write calls only in internal/gm/write.go"
exit $status
