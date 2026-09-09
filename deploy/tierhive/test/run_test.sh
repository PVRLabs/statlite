#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
RECIPE=$SCRIPT_DIR/../run.sh
TEST_DIR=$(mktemp -d)
trap 'rm -rf "$TEST_DIR"' EXIT HUP INT TERM

fail_test() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

assert_contains() {
	file=$1
	expected=$2
	grep -Fq "$expected" "$file" || fail_test "$file does not contain: $expected"
}

STATLITE_TIERHIVE_TESTING=1 . "$RECIPE"

assert_installed_version v1.2.3 'statlite v1.2.3'
if (assert_installed_version v1.2.3 'statlite v1.2.4') 2>/dev/null; then
	fail_test 'mismatching binary version was accepted'
fi

if (validate_version 1.2.3) 2>/dev/null; then
	fail_test 'version without v prefix was accepted'
fi
validate_version v1.2.3

validate_account_fields 123 statlite /sbin/nologin
if (validate_account_fields 0 statlite /sbin/nologin) 2>/dev/null; then
	fail_test 'root UID was accepted for the service account'
fi
if (validate_account_fields 1000 statlite /sbin/nologin) 2>/dev/null; then
	fail_test 'non-system UID was accepted for the service account'
fi
if (validate_account_fields 123 users /sbin/nologin) 2>/dev/null; then
	fail_test 'wrong primary group was accepted for the service account'
fi
if (validate_account_fields 123 statlite /bin/sh) 2>/dev/null; then
	fail_test 'login shell was accepted for the service account'
fi

CONFIG_FILE=$TEST_DIR/etc/statlite/statlite.yaml
ENV_FILE=$TEST_DIR/etc/conf.d/statlite
DATA_DIR=$TEST_DIR/var/lib/statlite
LOG_DIR=$TEST_DIR/var/log/statlite
INIT_FILE=$TEST_DIR/etc/init.d/statlite
mkdir -p "$(dirname "$CONFIG_FILE")" "$(dirname "$ENV_FILE")" "$DATA_DIR"
spring_actuator_url='http://10.0.0.2:8080/actuator?label="a&b"'
spring_app_name='spring\app "one"'
spring_auth_username="user'name"
spring_auth_password='pa$$ \word "quoted"'
create_initial_config

assert_contains "$CONFIG_FILE" 'listen: "127.0.0.1:9090"'
assert_contains "$CONFIG_FILE" 'sqlite_path: "/var/lib/statlite/statlite.sqlite"'
assert_contains "$CONFIG_FILE" 'name: "spring\\app \"one\""'
assert_contains "$CONFIG_FILE" 'url: "http://10.0.0.2:8080/actuator?label=\"a&b\""'
assert_contains "$CONFIG_FILE" 'username: ${STATLITE_ACTUATOR_USERNAME}'
assert_contains "$ENV_FILE" "export STATLITE_ACTUATOR_USERNAME='\"user'\\''name\"'"
assert_contains "$ENV_FILE" 'export STATLITE_ACTUATOR_PASSWORD='"'"'"pa$$ \\word \"quoted\""'"'"''
[ "$(stat -f '%Lp' "$CONFIG_FILE" 2>/dev/null || stat -c '%a' "$CONFIG_FILE")" = 640 ] ||
	fail_test 'config mode is not 0640'
[ "$(stat -f '%Lp' "$ENV_FILE" 2>/dev/null || stat -c '%a' "$ENV_FILE")" = 600 ] ||
	fail_test 'credential environment mode is not 0600'

config_checksum=$(cksum "$CONFIG_FILE")
env_checksum=$(cksum "$ENV_FILE")
printf '%s\n' 'preserved history marker' >"$DATA_DIR/statlite.sqlite"
database_checksum=$(cksum "$DATA_DIR/statlite.sqlite")
spring_actuator_url='http://changed.invalid/actuator'
spring_auth_username=partial
spring_auth_password=
preserve_or_create_config >/dev/null
[ "$(cksum "$CONFIG_FILE")" = "$config_checksum" ] || fail_test 'existing config was replaced'
[ "$(cksum "$ENV_FILE")" = "$env_checksum" ] || fail_test 'existing credentials were replaced'
[ "$(cksum "$DATA_DIR/statlite.sqlite")" = "$database_checksum" ] || fail_test 'existing database was replaced'

mkdir -p "$(dirname "$INIT_FILE")"
write_openrc_service
sh -n "$INIT_FILE"
if command -v dash >/dev/null 2>&1; then
	dash -n "$INIT_FILE"
fi
assert_contains "$INIT_FILE" 'command_user="statlite:statlite"'
assert_contains "$INIT_FILE" 'command_args="--config /etc/statlite/statlite.yaml"'
assert_contains "$INIT_FILE" 'directory="/var/lib/statlite"'
assert_contains "$INIT_FILE" 'pidfile="/run/${RC_SVCNAME}.pid"'
assert_contains "$INIT_FILE" 'retry="SIGTERM/20/SIGKILL/5"'
assert_contains "$INIT_FILE" '. /etc/conf.d/statlite'
[ "$(stat -f '%Lp' "$INIT_FILE" 2>/dev/null || stat -c '%a' "$INIT_FILE")" = 755 ] ||
	fail_test 'OpenRC service mode is not 0755'
config_uses_generated_listener || fail_test 'generated listener was not recognized'
sed 's/127\.0\.0\.1:9090/127.0.0.1:9191/' "$CONFIG_FILE" >"$CONFIG_FILE.changed"
mv "$CONFIG_FILE.changed" "$CONFIG_FILE"
if config_uses_generated_listener; then
	fail_test 'changed listener was recognized as the generated listener'
fi

if (spring_actuator_url=http://localhost/actuator spring_auth_username=partial spring_auth_password= validate_initial_config_inputs) 2>/dev/null; then
	fail_test 'partial fresh-install credentials were accepted'
fi
if (spring_actuator_url="bad
url" spring_auth_username= spring_auth_password= validate_initial_config_inputs) 2>/dev/null; then
	fail_test 'control character in fresh-install URL was accepted'
fi
if (spring_actuator_url='http://localhost/$service/actuator' spring_auth_username= spring_auth_password= validate_initial_config_inputs) 2>/dev/null; then
	fail_test 'environment-variable-like dollar sequence in URL was accepted'
fi
if (spring_actuator_url=http://localhost/actuator spring_app_name='app-${HOME}' spring_auth_username= spring_auth_password= validate_initial_config_inputs) 2>/dev/null; then
	fail_test 'environment-variable-like dollar sequence in application name was accepted'
fi

MOCK_BIN=$TEST_DIR/bin
mkdir -p "$MOCK_BIN" "$TEST_DIR/install"
cat >"$MOCK_BIN/curl" <<'EOF'
#!/bin/sh
set -eu
case "$*" in
	*healthz*)
		count=0
		[ ! -f "$HEALTH_COUNT" ] || count=$(cat "$HEALTH_COUNT")
		count=$((count + 1))
		printf '%s\n' "$count" >"$HEALTH_COUNT"
		[ "$count" -ge "$HEALTH_SUCCEED_AT" ]
		;;
	*releases/latest*)
		count=0
		[ ! -f "$LATEST_COUNT" ] || count=$(cat "$LATEST_COUNT")
		count=$((count + 1))
		printf '%s\n' "$count" >"$LATEST_COUNT"
		printf '%s' 'https://github.com/PVRLabs/statlite/releases/tag/v9.8.7'
		;;
	*raw.githubusercontent.com*)
		printf '%s\n' "$*" >"$INSTALLER_URL_FILE"
		while [ "$#" -gt 0 ]; do
			if [ "$1" = -o ]; then
				shift
				output=$1
				break
			fi
			shift
		done
		cat >"$output" <<'INSTALLER'
#!/bin/sh
set -eu
printf '%s\n' "$STATLITE_VERSION" >"$SELECTED_VERSION_FILE"
cat >"$STATLITE_INSTALL_DIR/statlite" <<BINARY
#!/bin/sh
printf '%s\\n' 'statlite $STATLITE_VERSION'
BINARY
chmod 0755 "$STATLITE_INSTALL_DIR/statlite"
INSTALLER
		;;
	*) exit 1 ;;
esac
EOF
chmod 0755 "$MOCK_BIN/curl"
cat >"$MOCK_BIN/rc-service" <<'EOF'
#!/bin/sh
set -eu
if [ "$2" = status ]; then
	[ "$SERVICE_RUNNING" = true ]
	exit
fi
printf '%s\n' "$2" >>"$SERVICE_ACTIONS"
[ "${SERVICE_ACTION_FAIL:-false}" != true ]
EOF
cat >"$MOCK_BIN/rc-update" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "$*" >"$RC_UPDATE_ARGS"
EOF
cat >"$MOCK_BIN/sleep" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod 0755 "$MOCK_BIN/rc-service" "$MOCK_BIN/rc-update" "$MOCK_BIN/sleep"
LATEST_COUNT=$TEST_DIR/latest-count
SELECTED_VERSION_FILE=$TEST_DIR/selected-version
INSTALLER_URL_FILE=$TEST_DIR/installer-url
HEALTH_COUNT=$TEST_DIR/health-count
SERVICE_ACTIONS=$TEST_DIR/service-actions
RC_UPDATE_ARGS=$TEST_DIR/rc-update-args
export LATEST_COUNT SELECTED_VERSION_FILE INSTALLER_URL_FILE HEALTH_COUNT SERVICE_ACTIONS RC_UPDATE_ARGS
PATH=$MOCK_BIN:$PATH
export PATH
INSTALL_DIR=$TEST_DIR/install
statlite_version=
selected_version=$(resolve_version "$statlite_version")
install_release "$selected_version"
[ "$(cat "$LATEST_COUNT")" = 1 ] || fail_test 'latest release was resolved more than once'
[ "$(cat "$SELECTED_VERSION_FILE")" = v9.8.7 ] || fail_test 'installer did not receive the concrete version'

statlite_version=v1.2.3
install_release "$statlite_version"
assert_contains "$INSTALLER_URL_FILE" 'PVRLabs/statlite/v1.2.3/install.sh'
[ "$(cat "$LATEST_COUNT")" = 1 ] || fail_test 'pinned install performed a latest-release lookup'

SERVICE_RUNNING=false
export SERVICE_RUNNING
enable_and_restart_service
assert_contains "$RC_UPDATE_ARGS" 'add statlite default'
assert_contains "$SERVICE_ACTIONS" start
SERVICE_RUNNING=true
export SERVICE_RUNNING
enable_and_restart_service
assert_contains "$SERVICE_ACTIONS" restart
SERVICE_ACTION_FAIL=true
export SERVICE_ACTION_FAIL
if (enable_and_restart_service) >"$TEST_DIR/service-failure.out" 2>"$TEST_DIR/service-failure.err"; then
	fail_test 'OpenRC action failure was accepted'
fi
assert_contains "$TEST_DIR/service-failure.err" "$LOG_DIR/statlite.err"
SERVICE_ACTION_FAIL=false
export SERVICE_ACTION_FAIL

HEALTH_SUCCEED_AT=3
export HEALTH_SUCCEED_AT
verify_health >/dev/null
[ "$(cat "$HEALTH_COUNT")" = 3 ] || fail_test 'health retries did not stop after success'
rm -f "$HEALTH_COUNT"
HEALTH_SUCCEED_AT=1
export HEALTH_SUCCEED_AT
verify_health_when_safe fresh >/dev/null
[ "$(cat "$HEALTH_COUNT")" = 1 ] || fail_test 'fresh config did not trigger health verification'
rm -f "$HEALTH_COUNT"
HEALTH_SUCCEED_AT=99
export HEALTH_SUCCEED_AT
if (verify_health) >"$TEST_DIR/health-failure.out" 2>"$TEST_DIR/health-failure.err"; then
	fail_test 'exhausted health check was accepted'
fi
[ "$(cat "$HEALTH_COUNT")" = 15 ] || fail_test 'health check was not bounded to 15 attempts'
assert_contains "$TEST_DIR/health-failure.err" "$LOG_DIR/statlite.log"
if grep -Fq 'pa$$' "$TEST_DIR/health-failure.err"; then
	fail_test 'health failure output exposed credentials'
fi

rm -f "$HEALTH_COUNT"
SERVICE_RUNNING=true
export SERVICE_RUNNING
verify_health_when_safe preserved >"$TEST_DIR/skipped-health.out"
[ ! -e "$HEALTH_COUNT" ] || fail_test 'changed listener triggered automatic health verification'
assert_contains "$TEST_DIR/skipped-health.out" 'curl -fsS http://HOST:PORT/healthz'
SERVICE_RUNNING=false
export SERVICE_RUNNING
if (verify_health_when_safe preserved) >"$TEST_DIR/custom-listener-exit.out" 2>"$TEST_DIR/custom-listener-exit.err"; then
	fail_test 'exited custom-listener service was reported as started'
fi
assert_contains "$TEST_DIR/custom-listener-exit.err" 'service exited after startup with the preserved custom listener'
assert_contains "$TEST_DIR/custom-listener-exit.err" "$LOG_DIR/statlite.err"

printf '%s\n' 'TierHive recipe fixture passed.'
