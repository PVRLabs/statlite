#!/bin/sh
set -eu

REPO=PVRLabs/statlite
INSTALL_DIR=/usr/local/bin
CONFIG_FILE=/etc/statlite/statlite.yaml
ENV_FILE=/etc/conf.d/statlite
DATA_DIR=/var/lib/statlite
LOG_DIR=/var/log/statlite
INIT_FILE=/etc/init.d/statlite

fail() {
	printf 'StatLite TierHive recipe: %s\n' "$*" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

validate_version() {
	printf '%s\n' "$1" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' ||
		fail "invalid statlite_version: $1 (expected vX.Y.Z)"
}

validate_yaml_input() {
	input_name=$1
	input_value=$2
	case "$input_value" in
		*'
'*) fail "$input_name contains an unsafe control character" ;;
	esac
	if printf '%s' "$input_value" | LC_ALL=C grep -q '[[:cntrl:]]'; then
		fail "$input_name contains an unsafe control character"
	fi
}

validate_direct_yaml_input() {
	input_name=$1
	input_value=$2
	validate_yaml_input "$input_name" "$input_value"
	case "$input_value" in
		*'$'*) fail "$input_name must not contain a dollar sign because StatLite expands environment variables before parsing YAML" ;;
	esac
}

yaml_double_quote() {
	printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

yaml_double_quoted_scalar() {
	printf '"%s"' "$(yaml_double_quote "$1")"
}

shell_single_quote() {
	printf '%s' "$1" | sed "s/'/'\\\\''/g"
}

set_file_owner() {
	owner=$1
	path=$2
	if [ "${STATLITE_TIERHIVE_TESTING:-0}" != 1 ]; then
		chown "$owner" "$path"
	fi
}

assert_installed_version() {
	selected_version=$1
	reported_version=$2
	expected_version="statlite $selected_version"
	[ "$reported_version" = "$expected_version" ] ||
		fail "installed binary reported '$reported_version' (expected '$expected_version')"
}

resolve_version() {
	requested_version=$1
	if [ -n "$requested_version" ]; then
		validate_version "$requested_version"
		printf '%s\n' "$requested_version"
		return
	fi

	latest_url=$(curl -fsSLo /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest")
	resolved_version=${latest_url##*/}
	validate_version "$resolved_version"
	printf '%s\n' "$resolved_version"
}

install_release() {
	selected_version=$1
	installer_file=$(mktemp)
	trap 'rm -f "$installer_file"' EXIT HUP INT TERM

	if [ -n "${statlite_version:-}" ]; then
		installer_url="https://raw.githubusercontent.com/${REPO}/${selected_version}/install.sh"
	else
		installer_url="https://raw.githubusercontent.com/${REPO}/main/install.sh"
	fi

	curl -fsSL "$installer_url" -o "$installer_file"
	STATLITE_INSTALL_DIR=$INSTALL_DIR STATLITE_VERSION=$selected_version sh "$installer_file"
	reported_version=$("$INSTALL_DIR/statlite" --version)
	assert_installed_version "$selected_version" "$reported_version"

	rm -f "$installer_file"
	trap - EXIT HUP INT TERM
}

ensure_runtime_prerequisites() {
	need apk
	if ! command -v curl >/dev/null 2>&1 || [ ! -r /etc/ssl/certs/ca-certificates.crt ]; then
		apk add --no-cache ca-certificates curl
	fi
	if ! command -v rc-service >/dev/null 2>&1 || ! command -v rc-update >/dev/null 2>&1; then
		apk add --no-cache openrc
	fi
	need curl
	need tar
	need mktemp
	need uname
	need awk
	need sed
	need grep
	need sha256sum
	need install
	need rc-service
	need rc-update
}

validate_platform() {
	[ "$(id -u)" -eq 0 ] || fail "run this recipe as root"
	[ -r /etc/alpine-release ] || fail "this recipe supports Alpine Linux only"
	case "$(uname -m)" in
		x86_64 | amd64 | aarch64 | arm64) ;;
		*) fail "unsupported architecture: $(uname -m)" ;;
	esac
}

validate_account_fields() {
	account_uid=$1
	account_group=$2
	account_shell=$3
	case "$account_uid" in
		'' | *[!0-9]*) fail "existing statlite account has an invalid UID: $account_uid" ;;
	esac
	[ "$account_uid" -gt 0 ] && [ "$account_uid" -lt 1000 ] ||
		fail "existing statlite account is not a non-root system account (UID $account_uid)"
	[ "$account_group" = statlite ] ||
		fail "existing statlite account must use statlite as its primary group"
	[ "$account_shell" = /sbin/nologin ] ||
		fail "existing statlite account must use /sbin/nologin"
}

validate_existing_account() {
	account_uid=$(id -u statlite)
	account_group=$(id -gn statlite)
	account_shell=$(awk -F: '$1 == "statlite" { print $7; found = 1; exit } END { if (!found) exit 1 }' /etc/passwd) ||
		fail "could not inspect existing statlite account"
	validate_account_fields "$account_uid" "$account_group" "$account_shell"
}

ensure_account_and_directories() {
	if id statlite >/dev/null 2>&1; then
		validate_existing_account
	else
		if ! grep -q '^statlite:' /etc/group; then
			addgroup -S statlite
		fi
		adduser -S -D -H -s /sbin/nologin -G statlite statlite
		validate_existing_account
	fi

	install -d -o root -g statlite -m 0750 "$(dirname "$CONFIG_FILE")"
	install -d -o root -g root -m 0755 "$(dirname "$ENV_FILE")"
	install -d -o statlite -g statlite -m 0750 "$DATA_DIR" "$LOG_DIR"
}

write_credentials() {
	username=$1
	password=$2
	encoded_username=$(yaml_double_quoted_scalar "$username")
	encoded_password=$(yaml_double_quoted_scalar "$password")
	temporary_file=$(mktemp "${ENV_FILE}.tmp.XXXXXX")
	(
		umask 077
		printf "export STATLITE_ACTUATOR_USERNAME='%s'\n" "$(shell_single_quote "$encoded_username")"
		printf "export STATLITE_ACTUATOR_PASSWORD='%s'\n" "$(shell_single_quote "$encoded_password")"
	) >"$temporary_file"
	set_file_owner root:root "$temporary_file"
	chmod 0600 "$temporary_file"
	mv "$temporary_file" "$ENV_FILE"
}

write_config() {
	app_name=$1
	actuator_url=$2
	with_auth=$3
	temporary_file=$(mktemp "${CONFIG_FILE}.tmp.XXXXXX")
	quoted_name=$(yaml_double_quote "$app_name")
	quoted_url=$(yaml_double_quote "$actuator_url")

	(
		umask 027
		printf '%s\n' \
			'server:' \
			'  listen: "127.0.0.1:9090"' \
			'' \
			'storage:' \
			'  sqlite_path: "/var/lib/statlite/statlite.sqlite"' \
			'' \
			'polling:' \
			'  interval: "30s"' \
			'' \
			'targets:' \
			"  - name: \"$quoted_name\"" \
			'    type: "spring"' \
			"    url: \"$quoted_url\"" \
			'    metrics_source: "auto"'
		if [ "$with_auth" = true ]; then
			printf '%s\n' \
				'    auth:' \
				'      type: "basic"' \
				'      username: ${STATLITE_ACTUATOR_USERNAME}' \
				'      password: ${STATLITE_ACTUATOR_PASSWORD}'
		fi
	) >"$temporary_file"
	set_file_owner root:statlite "$temporary_file"
	chmod 0640 "$temporary_file"
	mv "$temporary_file" "$CONFIG_FILE"
}

create_initial_config() {
	actuator_url=${spring_actuator_url:-}
	app_name=${spring_app_name:-my-spring-app}
	username=${spring_auth_username:-}
	password=${spring_auth_password:-}
	[ -n "$app_name" ] || app_name=my-spring-app

	[ -n "$actuator_url" ] || fail "spring_actuator_url is required for a fresh installation"
	validate_direct_yaml_input spring_actuator_url "$actuator_url"
	validate_direct_yaml_input spring_app_name "$app_name"

	if { [ -n "$username" ] && [ -z "$password" ]; } ||
		{ [ -z "$username" ] && [ -n "$password" ]; }; then
		fail "spring_auth_username and spring_auth_password must be supplied together"
	fi

	with_auth=false
	if [ -n "$username" ]; then
		validate_yaml_input spring_auth_username "$username"
		validate_yaml_input spring_auth_password "$password"
		write_credentials "$username" "$password"
		with_auth=true
	fi
	write_config "$app_name" "$actuator_url" "$with_auth"
}

validate_initial_config_inputs() {
	actuator_url=${spring_actuator_url:-}
	app_name=${spring_app_name:-my-spring-app}
	username=${spring_auth_username:-}
	password=${spring_auth_password:-}
	[ -n "$app_name" ] || app_name=my-spring-app

	[ -n "$actuator_url" ] || fail "spring_actuator_url is required for a fresh installation"
	validate_direct_yaml_input spring_actuator_url "$actuator_url"
	validate_direct_yaml_input spring_app_name "$app_name"
	if { [ -n "$username" ] && [ -z "$password" ]; } ||
		{ [ -z "$username" ] && [ -n "$password" ]; }; then
		fail "spring_auth_username and spring_auth_password must be supplied together"
	fi
	if [ -n "$username" ]; then
		validate_yaml_input spring_auth_username "$username"
		validate_yaml_input spring_auth_password "$password"
	fi
}

preserve_or_create_config() {
	if [ -e "$CONFIG_FILE" ]; then
		[ -f "$CONFIG_FILE" ] || fail "existing config is not a regular file: $CONFIG_FILE"
		set_file_owner root:statlite "$CONFIG_FILE"
		chmod 0640 "$CONFIG_FILE"
		if [ -e "$ENV_FILE" ]; then
			[ -f "$ENV_FILE" ] || fail "existing credential environment is not a regular file: $ENV_FILE"
			set_file_owner root:root "$ENV_FILE"
			chmod 0600 "$ENV_FILE"
		fi
		printf 'Preserved existing StatLite configuration: %s\n' "$CONFIG_FILE"
	else
		create_initial_config
		printf 'Created StatLite configuration: %s\n' "$CONFIG_FILE"
	fi
}

write_openrc_service() {
	temporary_file=$(mktemp "${INIT_FILE}.tmp.XXXXXX")
	cat >"$temporary_file" <<'EOF'
#!/sbin/openrc-run

name="StatLite monitoring service"
command="/usr/local/bin/statlite"
command_args="--config /etc/statlite/statlite.yaml"
command_user="statlite:statlite"
directory="/var/lib/statlite"
command_background="yes"
pidfile="/run/${RC_SVCNAME}.pid"
output_log="/var/log/statlite/statlite.log"
error_log="/var/log/statlite/statlite.err"
retry="SIGTERM/20/SIGKILL/5"
required_files="/usr/local/bin/statlite /etc/statlite/statlite.yaml"
required_dirs="/var/lib/statlite /var/log/statlite"

depend() {
	need net
}

start_pre() {
	checkpath --directory --owner statlite:statlite --mode 0750 /var/lib/statlite
	checkpath --directory --owner statlite:statlite --mode 0750 /var/log/statlite
	if [ -f /etc/conf.d/statlite ]; then
		. /etc/conf.d/statlite
	fi
}
EOF
	set_file_owner root:root "$temporary_file"
	chmod 0755 "$temporary_file"
	mv "$temporary_file" "$INIT_FILE"
}

config_uses_generated_listener() {
	grep -Fqx '  listen: "127.0.0.1:9090"' "$CONFIG_FILE"
}

report_service_failure() {
	reason=$1
	printf 'StatLite service failure: %s\n' "$reason" >&2
	rc-service statlite status >&2 || true
	printf 'Inspect service logs at %s/statlite.log and %s/statlite.err.\n' "$LOG_DIR" "$LOG_DIR" >&2
	fail "$reason"
}

enable_and_restart_service() {
	rc-update add statlite default
	if rc-service statlite status >/dev/null 2>&1; then
		service_action=restart
	else
		service_action=start
	fi
	if ! rc-service statlite "$service_action"; then
		report_service_failure "could not $service_action the OpenRC service"
	fi
}

verify_health() {
	attempt=1
	max_attempts=15
	while [ "$attempt" -le "$max_attempts" ]; do
		if curl -fsS --max-time 2 -o /dev/null http://127.0.0.1:9090/healthz 2>/dev/null; then
			printf '%s\n' 'StatLite health check passed: http://127.0.0.1:9090/healthz'
			return
		fi
		if [ "$attempt" -lt "$max_attempts" ]; then
			sleep 1
		fi
		attempt=$((attempt + 1))
	done

	printf '%s\n' 'StatLite did not become healthy at http://127.0.0.1:9090/healthz.' >&2
	report_service_failure "local health verification failed"
}

verify_health_when_safe() {
	if [ "$1" = fresh ] || config_uses_generated_listener; then
		verify_health
	else
		sleep 1
		if ! rc-service statlite status >/dev/null 2>&1; then
			report_service_failure "service exited after startup with the preserved custom listener"
		fi
		printf '%s\n' 'Preserved configuration uses a different listener; automatic health verification was skipped.'
		printf '%s\n' 'After confirming its address, run: curl -fsS http://HOST:PORT/healthz'
	fi
}

main() {
	validate_platform
	need grep
	if [ ! -e "$CONFIG_FILE" ]; then
		config_state=fresh
		validate_initial_config_inputs
	else
		config_state=preserved
	fi
	ensure_runtime_prerequisites
	selected_version=$(resolve_version "${statlite_version:-}")
	install_release "$selected_version"
	ensure_account_and_directories
	preserve_or_create_config
	write_openrc_service
	enable_and_restart_service
	verify_health_when_safe "$config_state"
	printf 'Installed StatLite %s and started its TierHive service.\n' "$selected_version"
}

if [ "${STATLITE_TIERHIVE_TESTING:-0}" != 1 ]; then
	main "$@"
fi
