#!/bin/sh
# Preserve site-specific systemd settings across release unit updates.

migrate_local_dronservice_settings() {
	unit_path=${1:-/etc/systemd/system/dronservice.service}
	dropin_dir=/etc/systemd/system/dronservice.service.d
	dropin=${dropin_dir}/local.conf
	env_file=/etc/dronservice/dronservice.env

	[ -r "$unit_path" ] || return 0
	install -d -m 0755 "$dropin_dir" /etc/dronservice

	if [ ! -e "$dropin" ]; then
		user=$(grep '^User=' "$unit_path" 2>/dev/null | head -1 | cut -d= -f2- || true)
		group=$(grep '^Group=' "$unit_path" 2>/dev/null | head -1 | cut -d= -f2- || true)
		if [ -n "$user" ] && [ "$user" != "admin" ]; then
			{
				printf '[Service]\nUser=%s\n' "$user"
				[ -n "$group" ] && printf 'Group=%s\n' "$group"
			} > "$dropin"
			chmod 0644 "$dropin"
		fi
	fi

	if [ ! -e "$env_file" ]; then
		: > "$env_file"
	fi

	for key in DRONSERVICE_HLS_PUBLIC_URL DRONSERVICE_ADDR MEDIAMTX_USER MEDIAMTX_PASSWORD DRONSERVICE_DISCOVERY_INTERFACE DRONSERVICE_DISCOVERY_LEGACY; do
		value=$(grep "^Environment=${key}=" "$unit_path" 2>/dev/null | head -1 | sed "s/^Environment=${key}=//" || true)
		[ -n "$value" ] || continue
		if [ "$key" = "DRONSERVICE_ADDR" ] && [ "$value" = ":80" ]; then
			continue
		fi
		if ! grep -q "^${key}=" "$env_file" 2>/dev/null; then
			printf '%s=%s\n' "$key" "$value" >> "$env_file"
		fi
	done

	chmod 0644 "$env_file" 2>/dev/null || true
}

dronservice_runtime_account() {
	dropin=/etc/systemd/system/dronservice.service.d/local.conf
	unit=/etc/systemd/system/dronservice.service
	user=$(grep '^User=' "$dropin" 2>/dev/null | head -1 | cut -d= -f2- || true)
	group=$(grep '^Group=' "$dropin" 2>/dev/null | head -1 | cut -d= -f2- || true)
	if [ -z "$user" ] && [ -r "$unit" ]; then
		user=$(grep '^User=' "$unit" 2>/dev/null | head -1 | cut -d= -f2- || true)
	fi
	if [ -z "$group" ] && [ -r "$unit" ]; then
		group=$(grep '^Group=' "$unit" 2>/dev/null | head -1 | cut -d= -f2- || true)
	fi
	if [ -z "$user" ] && [ -d /var/lib/dronservice ]; then
		user=$(stat -c '%U' /var/lib/dronservice 2>/dev/null || true)
		group=$(stat -c '%G' /var/lib/dronservice 2>/dev/null || true)
	fi
	[ -n "$user" ] || user=admin
	[ -n "$group" ] || group=admin
	if ! id "$user" >/dev/null 2>&1; then
		return 1
	fi
	if ! getent group "$group" >/dev/null 2>&1; then
		group=$user
	fi
	printf '%s:%s\n' "$user" "$group"
}

dronservice_health_base_url() {
	env_file=/etc/dronservice/dronservice.env
	unit_file=/etc/systemd/system/dronservice.service
	addr=":80"

	if [ -f "$env_file" ]; then
		file_addr=$(grep '^DRONSERVICE_ADDR=' "$env_file" 2>/dev/null | head -1 | cut -d= -f2- || true)
		[ -n "$file_addr" ] && addr="$file_addr"
	fi
	if [ "$addr" = ":80" ] && [ -r "$unit_file" ]; then
		unit_addr=$(grep '^Environment=DRONSERVICE_ADDR=' "$unit_file" 2>/dev/null | head -1 | cut -d= -f2- || true)
		[ -n "$unit_addr" ] && addr="$unit_addr"
	fi

	case "$addr" in
	:80) printf '%s\n' "http://127.0.0.1" ;;
	:*) printf '%s\n' "http://127.0.0.1${addr}" ;;
	*) printf '%s\n' "http://${addr}" ;;
	esac
}

ensure_update_configuration() {
	update_conf=/etc/dronservice/update.conf
	public_key=/usr/local/etc/dronservice-release.pub

	install -d -m 0755 /etc/dronservice /usr/local/libexec /usr/local/etc
	if [ ! -e "$update_conf" ] && printf '%s\n' "${DRONSERVICE_UPDATE_REPOSITORY:-}" | grep -Eq '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$'; then
		{
			printf 'DRONSERVICE_UPDATE_REPOSITORY=%s\n' "$DRONSERVICE_UPDATE_REPOSITORY"
			printf 'DRONSERVICE_UPDATE_PUBLIC_KEY=%s\n' "${DRONSERVICE_UPDATE_PUBLIC_KEY:-$public_key}"
		} > "$update_conf"
		chmod 0644 "$update_conf"
	fi
}
