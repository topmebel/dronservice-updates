#!/bin/sh
set -u

request_file=/var/lib/dronservice/update-dronservice.request
status_file=/var/lib/dronservice/update-dronservice.status.json
lock_dir=/run/dronservice-update/lock
asset=dronservice-linux-arm64
checksums=checksums.sha256
signature=dronservice-linux-arm64.sig
work_dir=
previous_target=
switched=false

write_status() {
	state=$1
	status_version=$2
	message=$3
	temporary="${status_file}.tmp"
	printf '{"state":"%s","version":"%s","message":"%s","updatedAt":"%s"}\n' \
		"$state" "$status_version" "$message" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$temporary" || return 1
	chmod 0644 "$temporary" || return 1
	mv -f "$temporary" "$status_file"
}

cleanup() {
	[ -z "$work_dir" ] || rm -rf "$work_dir"
	rm -f "$request_file"
	rmdir "$lock_dir" 2>/dev/null || true
}

rollback() {
	[ "$switched" = true ] || return 0
	previous_release=$(dirname "$previous_target")
	rollback_link=/usr/local/lib/dronservice/.current-rollback
	bin_link=/usr/local/bin/.dronservice-rollback
	rm -f "$rollback_link" "$bin_link"
	ln -s "$previous_release" "$rollback_link" || return 1
	mv -Tf "$rollback_link" /usr/local/lib/dronservice/current || return 1
	ln -s /usr/local/lib/dronservice/current/dronservice "$bin_link" || return 1
	mv -Tf "$bin_link" /usr/local/bin/dronservice || return 1
	systemctl restart dronservice.service
}

fail_update() {
	message=$1
	rollback || true
	write_status failed "$version" "$message" || true
	exit 1
}

trap cleanup EXIT INT TERM

if ! mkdir "$lock_dir" 2>/dev/null; then
	exit 0
fi

version=
attempt=1
while [ "$attempt" -le 10 ]; do
	version=$(tr -d '\r\n' < "$request_file" 2>/dev/null || true)
	printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' && break
	sleep 0.1
	attempt=$((attempt + 1))
done
if ! printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'; then
	write_status failed "" invalid-version || true
	exit 2
fi
if ! printf '%s\n' "${DRONSERVICE_UPDATE_REPOSITORY:-}" | grep -Eq '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$'; then
	write_status failed "$version" invalid-repository || true
	exit 2
fi
public_key=${DRONSERVICE_UPDATE_PUBLIC_KEY:-/usr/local/etc/dronservice-release.pub}
[ -r "$public_key" ] || fail_update missing-public-key

for command in curl openssl sha256sum; do
	command -v "$command" >/dev/null 2>&1 || fail_update missing-required-command
done

work_dir=$(mktemp -d) || fail_update temporary-directory-failed
base_url="https://github.com/${DRONSERVICE_UPDATE_REPOSITORY}/releases/download/${version}"
write_status downloading "$version" downloading || fail_update status-write-failed
for file in "$asset" "$checksums" "$signature"; do
	curl --fail --location --silent --show-error --proto '=https' --tlsv1.2 \
		"${base_url}/${file}" --output "${work_dir}/${file}" || fail_update download-failed
done

write_status verifying "$version" verifying || fail_update status-write-failed
expected_checksum=$(awk -v name="$asset" '$2 == name || $2 == "*" name {print $1}' "${work_dir}/${checksums}")
printf '%s\n' "$expected_checksum" | grep -Eq '^[0-9a-fA-F]{64}$' || fail_update invalid-checksum
printf '%s  %s\n' "$expected_checksum" "${work_dir}/${asset}" | sha256sum --check --status || fail_update checksum-mismatch
openssl dgst -sha256 -verify "$public_key" -signature "${work_dir}/${signature}" "${work_dir}/${asset}" >/dev/null 2>&1 || fail_update signature-mismatch
chmod 0755 "${work_dir}/${asset}" || fail_update chmod-failed
downloaded_version=$("${work_dir}/${asset}" --version 2>/dev/null || true)
[ "$downloaded_version" = "$version" ] || fail_update binary-version-mismatch

required_kb=$((($(stat -c %s "${work_dir}/${asset}") + 1023) / 1024 * 3))
available_kb=$(df -Pk /usr/local/lib/dronservice | awk 'NR == 2 {print $4}')
[ "$available_kb" -ge "$required_kb" ] || fail_update insufficient-disk-space

write_status installing "$version" installing || fail_update status-write-failed
release_dir="/usr/local/lib/dronservice/releases/${version}"
previous_target=$(readlink -f /usr/local/bin/dronservice) || fail_update current-release-unavailable
install -d -o root -g root -m 0755 "$release_dir" || fail_update create-release-directory-failed
if [ -e "${release_dir}/dronservice" ]; then
	installed_checksum=$(sha256sum "${release_dir}/dronservice" | awk '{print $1}')
	[ "$installed_checksum" = "$expected_checksum" ] || fail_update immutable-release-conflict
else
	install -o root -g root -m 0755 "${work_dir}/${asset}" "${release_dir}/dronservice" || fail_update install-binary-failed
fi

next_current=/usr/local/lib/dronservice/.current-next
next_binary=/usr/local/bin/.dronservice-next
rm -f "$next_current" "$next_binary"
ln -s "$release_dir" "$next_current" || fail_update link-release-failed
mv -Tf "$next_current" /usr/local/lib/dronservice/current || fail_update switch-release-failed
ln -s /usr/local/lib/dronservice/current/dronservice "$next_binary" || fail_update link-binary-failed
mv -Tf "$next_binary" /usr/local/bin/dronservice || fail_update switch-binary-failed
switched=true

write_status restarting "$version" restarting || fail_update status-write-failed
systemctl restart dronservice.service || fail_update restart-failed
healthy=false
attempt=1
while [ "$attempt" -le 30 ]; do
	if curl --fail --silent --show-error http://127.0.0.1/api/health >/dev/null 2>&1 && \
		curl --fail --silent --show-error http://127.0.0.1/api/version | grep -Fq "\"version\":\"${version}\""; then
		healthy=true
		break
	fi
	sleep 1
	attempt=$((attempt + 1))
done
[ "$healthy" = true ] || fail_update health-check-failed
write_status succeeded "$version" installed || fail_update status-write-failed
exit 0
