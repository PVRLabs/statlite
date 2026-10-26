# Telegram application health alerts

Run `check-status.sh` through `notify-telegram.sh` to send an alert when an
application becomes unhealthy and a recovery when it becomes healthy again.
This is external API automation, with no alert service inside StatLite.

## Requirements and bot setup

Use a Linux host with `/bin/sh`, `curl`, `jq`, `flock` (usually util-linux),
`mktemp`, and cron or systemd. Keep the scripts from this directory together,
for example under `/opt/statlite/examples/api-automation`, readable and
executable by your scheduling user. Configure the application target in
StatLite first. The examples use an existing unprivileged user named
`statlite-alerts` with a home directory at `/home/statlite-alerts`.

1. Create a bot with [@BotFather](https://t.me/BotFather) using `/newbot`, and
   save its token privately. See Telegram's [bot setup guide](https://core.telegram.org/bots/features#botfather).
2. For a personal chat, open your bot and send `/start`. For a group, add the
   bot, allow it to send messages, and send `/start@YourBotUsername` in the
   group. A plain group without topics is simplest for this recipe.
3. Find the numeric chat ID using
   [getUpdates](https://core.telegram.org/bots/api#getupdates), as shown below.
   Group IDs are negative; preserve the minus sign. Use a dedicated bot with
   no webhook or other update consumer, otherwise updates may be consumed
   elsewhere. Send another command if the result is empty.

As the scheduling user, create protected directories and edit the config with
an editor. Do not paste credentials into shell commands or shell history.

```sh
umask 077
mkdir -p "$HOME/.config/statlite-alerts" "$HOME/.local/state/statlite-alerts"
chmod 700 "$HOME/.config/statlite-alerts" "$HOME/.local/state/statlite-alerts"
touch "$HOME/.config/statlite-alerts/health.env"
chmod 600 "$HOME/.config/statlite-alerts/health.env"
```

Put these shell assignments in `health.env`, replacing the placeholders in
your editor. The file is sourced as shell code, so only its owner should be
able to edit it.

```sh
TELEGRAM_BOT_TOKEN='replace-in-editor'
TELEGRAM_CHAT_ID='replace-in-editor'
BASE_URL='http://127.0.0.1:9090'
TARGET='my-app'
UNHEALTHY_VALUES='DOWN,OUT_OF_SERVICE'
MAX_POLL_AGE_SECONDS='180'
MAX_HEALTH_AGE_SECONDS='300'
CHECK_NAME='my-app health on my-vps'
STATE_FILE='/home/statlite-alerts/.local/state/statlite-alerts/my-app-health.state'
```

To discover the chat ID after saving the token, run this in a shell without
tracing (`set -x`). The token-bearing URL goes to curl through stdin, keeping
it out of process arguments. Output includes chat IDs and titles, not tokens.

```sh
set -a
. "$HOME/.config/statlite-alerts/health.env"
set +a
printf 'url = "https://api.telegram.org/bot%s/getUpdates"\n' "$TELEGRAM_BOT_TOKEN" |
  curl -q --config - --silent --fail --max-time 20 2>/dev/null |
  jq '.result[] | .message.chat? // empty | {id, type, title}'
```

Save the chosen ID in the config using your editor. Treat the token as a
password; rotate it through BotFather if exposed. Do not enable shell tracing,
curl verbose logging, or use a token-bearing URL as a command argument.
Environment variables are also accepted, but remain visible to sufficiently
privileged local processes. Keep the state directory private and persistent,
and use the same scheduler user every time. State and lock files are created
with mode 600. Never delete the lock file while runs may be active.

## Run the health recipe

Create `/home/statlite-alerts/check-health` with mode 700:

```sh
#!/bin/sh
set -eu
set -a
. /home/statlite-alerts/.config/statlite-alerts/health.env
set +a
exec /opt/statlite/examples/api-automation/notify-telegram.sh \
  /opt/statlite/examples/api-automation/check-status.sh
```

Run it manually as `statlite-alerts`. The first healthy result establishes a
silent baseline. The first unhealthy result sends `ALERT: my-app health on
my-vps`. Later known transitions send ALERT or RECOVERY once; unchanged states
send nothing. Messages use only `CHECK_NAME`, never check output. Customize
that label without including secrets. Telegram delivery uses plain text
[sendMessage](https://core.telegram.org/bots/api#sendmessage).

The health check reads `GET /api/v1/status`: recent collection failure counts
as unhealthy, as do exact health values in `UNHEALTHY_VALUES`. Configure those
values for your integration. API errors, stale polls, and missing or stale
application health are UNKNOWN, which preserves the previous state and never
sends recovery. See [health check details](README.md#how-the-checks-handle-missing-data).

The wrapper exits 0 after a successful run (including unhealthy notification,
unchanged state, or skipped overlapping run), and 2 for UNKNOWN or notifier
failure. Review scheduler logs for these errors. Telegram rejection, HTTP
failure, or timeout leaves state unchanged so the next run retries if the
condition still differs from the saved state. No queue or outage history is
stored. An outage that ends before successful delivery may go unreported.

State replacement is atomic and `flock` serializes each check invocation.
There is no exactly-once delivery guarantee: if Telegram accepts a message
but its response is lost, or the process stops before saving state, a later
run can send a duplicate. A failed local state write after sending also needs
operator attention. Deleting state resets the baseline behavior.

## Schedule with cron or systemd

Choose one scheduler. In the `statlite-alerts` user's crontab (`crontab -e`):

```cron
PATH=/usr/local/bin:/usr/bin:/bin
* * * * * /home/statlite-alerts/check-health
```

Cron error output needs a configured local mail/log destination. For systemd,
install `/etc/systemd/system/statlite-health-alert.service`:

```ini
[Unit]
Description=Check application health and notify Telegram
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
User=statlite-alerts
Environment=PATH=/usr/local/bin:/usr/bin:/bin
ExecStart=/home/statlite-alerts/check-health
UMask=0077
```

And `/etc/systemd/system/statlite-health-alert.timer`:

```ini
[Unit]
Description=Check application health every minute

[Timer]
OnBootSec=1min
OnUnitInactiveSec=1min

[Install]
WantedBy=timers.target
```

Enable the timer as an administrator:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now statlite-health-alert.timer
sudo journalctl -u statlite-health-alert.service
```

## Other checks and limits

The helper can wrap any command with the same 0=healthy, 1=unhealthy,
2=UNKNOWN convention, including the existing HTTP 5xx and sustained CPU checks.
Every target/condition/chat combination needs its own `STATE_FILE` and useful
`CHECK_NAME`; sharing state can hide transitions. Use a separate protected
config and launcher for each scheduled check. Unexpected check exit codes are
also UNKNOWN. Dependencies and permissions must be configured before use.

If the monitoring host is offline, it cannot run checks or deliver Telegram
alerts. Use independent uptime monitoring when that failure must be detected.
This recipe only observes scheduled samples, so short outages between runs can
be missed. It does not add generic rules or independent uptime monitoring.
