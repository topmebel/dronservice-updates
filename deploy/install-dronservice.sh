#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
	echo "run as root" >&2
	exit 1
fi
case "$(uname -m)" in
	aarch64|arm64) ;;
	*) echo "DronService requires Linux ARM64/aarch64" >&2; exit 1 ;;
esac
id admin >/dev/null 2>&1 || { echo "required user admin does not exist" >&2; exit 1; }
getent group admin >/dev/null 2>&1 || { echo "required group admin does not exist" >&2; exit 1; }
for command in ip arping; do
	command_path=$(command -v "$command" 2>/dev/null || true)
	[ -n "$command_path" ] && [ -x "$command_path" ] || { echo "missing required camera network command: $command (install iproute2 and iputils-arping)" >&2; exit 1; }
done
if [ "$#" -ne 2 ]; then
	echo "usage: install-dronservice.sh /path/to/dronservice-linux-arm64 owner/repository" >&2
	exit 2
fi

binary=$1
repository=$2
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "${script_dir}/migrate-local-settings.sh"
helper_binary="$(dirname -- "$binary")/dronservice-camera-network-helper"
[ -x "$helper_binary" ] || { echo "missing dronservice-camera-network-helper next to application binary" >&2; exit 2; }
version=$("$binary" --version)
printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
printf '%s\n' "$repository" | grep -Eq '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$'

release_dir="/usr/local/lib/dronservice/releases/${version}"
install -d -o root -g root -m 0755 "$release_dir" /usr/local/libexec /usr/local/etc /etc/dronservice
install -d -o admin -g admin -m 0750 /var/lib/dronservice
install -d -o admin -g admin -m 0750 /usr/local/etc/mediamtx
[ ! -d /var/lib/dronservice/camera-network.lock ] || rmdir /var/lib/dronservice/camera-network.lock || { echo "cannot migrate stale camera network lock directory" >&2; exit 1; }
install -o root -g root -m 0755 "$binary" "${release_dir}/dronservice"
install -o root -g root -m 0755 "$helper_binary" /usr/local/libexec/dronservice-camera-network-helper
helper_owner=$(stat -c '%U:%G' /usr/local/libexec/dronservice-camera-network-helper)
helper_mode=$(stat -c '%a' /usr/local/libexec/dronservice-camera-network-helper)
[ "$helper_owner" = "root:root" ] && [ "$helper_mode" = "755" ] || { echo "invalid camera network helper ownership or mode: $helper_owner $helper_mode" >&2; exit 1; }
existing_unit=/etc/systemd/system/dronservice.service
if [ -e "$existing_unit" ]; then
	temp_unit=$(mktemp)
	cp -p "$existing_unit" "$temp_unit"
	migrate_local_dronservice_settings "$temp_unit"
	rm -f "$temp_unit"
fi
install -o root -g root -m 0755 "${script_dir}/update-dronservice.sh" /usr/local/libexec/dronservice-update
install -o root -g root -m 0755 "${script_dir}/migrate-local-settings.sh" /usr/local/libexec/dronservice-migrate-local-settings
install -o root -g root -m 0755 "${script_dir}/install-mediamtx.sh" /usr/local/libexec/dronservice-install-mediamtx
install -o root -g root -m 0644 "${script_dir}/dronservice-release.pub" /usr/local/etc/dronservice-release.pub
install -o root -g root -m 0644 "${script_dir}/dronservice.service" /etc/systemd/system/dronservice.service
install -o root -g root -m 0644 "${script_dir}/dronservice-update.service" /etc/systemd/system/dronservice-update.service
install -o root -g root -m 0644 "${script_dir}/dronservice-update.path" /etc/systemd/system/dronservice-update.path
install -o root -g root -m 0644 "${script_dir}/dronservice-mediamtx-install.service" /etc/systemd/system/dronservice-mediamtx-install.service
install -o root -g root -m 0644 "${script_dir}/dronservice-mediamtx-install.path" /etc/systemd/system/dronservice-mediamtx-install.path
install -o root -g root -m 0644 "${script_dir}/mediamtx.service" /etc/systemd/system/mediamtx.service
install -o root -g root -m 0644 "${script_dir}/dronservice-camera-network.service" /etc/systemd/system/dronservice-camera-network.service
install -o root -g root -m 0644 "${script_dir}/dronservice-camera-network.path" /etc/systemd/system/dronservice-camera-network.path
if [ ! -e /etc/dronservice/camera-network.conf ]; then
	install -o root -g root -m 0644 "${script_dir}/dronservice-camera-network.conf" /etc/dronservice/camera-network.conf
fi

if [ ! -e /usr/local/etc/mediamtx/mediamtx.yml ]; then
	if [ -e /usr/local/etc/mediamtx.yml ]; then
		install -o admin -g admin -m 0660 /usr/local/etc/mediamtx.yml /usr/local/etc/mediamtx/mediamtx.yml
	else
		install -o admin -g admin -m 0660 "${script_dir}/mediamtx.yml" /usr/local/etc/mediamtx/mediamtx.yml
	fi
fi

if [ ! -e /etc/dronservice/update.conf ]; then
	printf '%s\n' \
		"DRONSERVICE_UPDATE_REPOSITORY=${repository}" \
		'DRONSERVICE_UPDATE_PUBLIC_KEY=/usr/local/etc/dronservice-release.pub' > /etc/dronservice/update.conf
	chmod 0644 /etc/dronservice/update.conf
fi
if [ ! -e /etc/dronservice/dronservice.env ] && [ -e "${script_dir}/dronservice.env.example" ]; then
	install -o root -g root -m 0644 "${script_dir}/dronservice.env.example" /etc/dronservice/dronservice.env
fi

next_current=/usr/local/lib/dronservice/.current-bootstrap
next_binary=/usr/local/bin/.dronservice-bootstrap
rm -f "$next_current" "$next_binary"
ln -s "$release_dir" "$next_current"
mv -Tf "$next_current" /usr/local/lib/dronservice/current
ln -s /usr/local/lib/dronservice/current/dronservice "$next_binary"
mv -Tf "$next_binary" /usr/local/bin/dronservice

systemctl daemon-reload
systemctl enable --now dronservice-update.path dronservice-mediamtx-install.path dronservice-camera-network.path
state_owner=$(stat -c '%U:%G' /var/lib/dronservice)
state_mode=$(stat -c '%a' /var/lib/dronservice)
[ "$state_owner" = "admin:admin" ] && [ "$state_mode" = "750" ] || { echo "invalid /var/lib/dronservice ownership or mode: $state_owner $state_mode" >&2; exit 1; }
runuser -u admin -- test -r /var/lib/dronservice -a -w /var/lib/dronservice || { echo "camera network helper user cannot access state directory" >&2; exit 1; }
check_dir=$(mktemp -d /var/lib/dronservice/.helper-check.XXXXXX)
chown admin:admin "$check_dir"
runuser -u admin -- env DRONSERVICE_DATA_DIR="$check_dir" DRONSERVICE_CAMERA_NETWORK_INTERFACES=eth0 /usr/local/libexec/dronservice-camera-network-helper || { rm -f "$check_dir/camera-network.lock"; rmdir "$check_dir"; echo "camera network helper no-op check failed" >&2; exit 1; }
rm -f "$check_dir/camera-network.lock"
rmdir "$check_dir"
systemctl is-active --quiet dronservice-camera-network.path || { echo "camera network path unit is not active" >&2; exit 1; }
helper_user=$(systemctl show dronservice-camera-network.service -p User --value)
helper_caps=$(systemctl show dronservice-camera-network.service -p CapabilityBoundingSet --value)
helper_caps_upper=$(printf '%s' "$helper_caps" | tr '[:lower:]' '[:upper:]')
[ "$helper_user" = "admin" ] || { echo "camera network helper has unexpected user: $helper_user" >&2; exit 1; }
case "$helper_caps_upper" in *CAP_NET_ADMIN*CAP_NET_RAW*|*CAP_NET_RAW*CAP_NET_ADMIN*) ;; *) echo "camera network helper lacks required capabilities: $helper_caps" >&2; exit 1 ;; esac
systemctl enable dronservice.service
systemctl restart dronservice.service

health_base=$(dronservice_health_base_url)
attempt=1
while [ "$attempt" -le 30 ]; do
	if curl --fail --silent "${health_base}/api/health" >/dev/null 2>&1 && \
		curl --fail --silent "${health_base}/api/version" | grep -Fq "\"version\":\"${version}\""; then
		exit 0
	fi
	sleep 1
	attempt=$((attempt + 1))
done
exit 1
