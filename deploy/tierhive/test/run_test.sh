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
LATEST_COUNT=$TEST_DIR/latest-count
SELECTED_VERSION_FILE=$TEST_DIR/selected-version
INSTALLER_URL_FILE=$TEST_DIR/installer-url
export LATEST_COUNT SELECTED_VERSION_FILE INSTALLER_URL_FILE
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

printf '%s\n' 'TierHive recipe fixture passed.'
