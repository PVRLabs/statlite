#!/bin/sh
# Wrap a check returning 0 (healthy), 1 (unhealthy), or 2 (unknown).
set -eu
umask 077
fail() { echo "Telegram notifier: $1" >&2; exit 2; }
[ "$#" -gt 0 ] || fail 'supply a check command'
[ -n "${STATE_FILE:-}" ] || fail 'STATE_FILE is required'
[ -n "${TELEGRAM_BOT_TOKEN:-}" ] || fail 'TELEGRAM_BOT_TOKEN is required'
[ -n "${TELEGRAM_CHAT_ID:-}" ] || fail 'TELEGRAM_CHAT_ID is required'
# Only these characters are valid in a bot token; also prevent curl config injection.
case $TELEGRAM_BOT_TOKEN in *[!A-Za-z0-9_:-]*) fail 'invalid bot token' ;; esac
for dependency in curl jq flock mktemp; do
  command -v "$dependency" >/dev/null 2>&1 || fail "missing dependency: $dependency"
done
# The caller must provide a private directory. Never remove this lock file:
# replacing it would allow concurrent processes to lock different inodes.
command exec 9>"${STATE_FILE}.lock" || fail 'could not open state lock'
lock_result=0
flock -n -E 75 9 || lock_result=$?
case $lock_result in
  0) ;;
  75) exit 0 ;; # Another invocation owns this state.
  *) fail 'could not lock state' ;;
esac
previous=
if [ -e "$STATE_FILE" ]; then
  previous=$(cat "$STATE_FILE") || fail 'could not read state'
  case $previous in healthy|unhealthy) ;; *) fail 'invalid state file' ;; esac
fi
# Check stdout is a short, user-facing summary. Keep diagnostics and secrets
# on stderr, which is never forwarded. Use trusted checks with this contract.
result=0
summary=$("$@" 2>/dev/null) || result=$?
case $result in
  0) current=healthy ;;
  1) current=unhealthy ;;
  *) fail 'UNKNOWN: check could not be evaluated; state unchanged' ;;
esac
[ "$current" != "$previous" ] || exit 0
# Prepare the atomic replacement before sending, so unwritable paths fail early.
temporary=$(mktemp "${STATE_FILE}.tmp.XXXXXX") || fail 'could not create state temporary file'
trap 'rm -f "$temporary"' EXIT
trap 'exit 2' HUP INT TERM
printf '%s\n' "$current" >"$temporary" || fail 'could not write state temporary file'
if [ "$current" = unhealthy ] || [ -n "$previous" ]; then
  case $current in unhealthy) prefix=ALERT ;; healthy) prefix=RECOVERY ;; esac
  # JSON is supplied in a private file; the token URL uses curl config,
  # never argv. Suppress curl diagnostics because they can include the URL.
  payload=$(printf '%s' "$prefix: ${CHECK_NAME:-application health}" |
    jq -Rs --arg summary "$summary" '
      ($summary | (split("\n")[0] // "") | sub("^(OK|ALERT|RECOVERY): *"; "") |
       gsub("[\u0000-\u001f\u007f]"; " ") | .[0:1000]) as $detail |
      {chat_id: env.TELEGRAM_CHAT_ID,
       text: ((. + if $detail == "" then "" else "\n" + $detail end)[0:4000])}') || fail 'could not construct Telegram payload'
  response=$(mktemp "${STATE_FILE}.response.XXXXXX") || fail 'could not create response temporary file'
  trap 'rm -f "$temporary" "$response"' EXIT
  printf '%s' "$payload" >"$temporary" || fail 'could not write Telegram payload'
  if ! printf 'url = "https://api.telegram.org/bot%s/sendMessage"\n' "$TELEGRAM_BOT_TOKEN" |
    curl -q --config - --silent --fail --connect-timeout 10 --max-time 20 \
      --header 'Content-Type: application/json' --data-binary "@$temporary" \
      --output "$response" 2>/dev/null; then
    fail 'delivery failed; state unchanged'
  fi
  jq -e '.ok == true' "$response" >/dev/null 2>&1 || fail 'delivery rejected; state unchanged'
fi
printf '%s\n' "$current" >"$temporary" || fail 'could not write state temporary file'
mv -f "$temporary" "$STATE_FILE" || fail 'could not persist state'
