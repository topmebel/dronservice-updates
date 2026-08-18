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
if [ "$#" -ne 2 ]; then
	echo "usage: install-dronservice.sh /path/to/dronservice-linux-arm64 owner/repository" >&2
	exit 2
fi

binary=$1
repository=$2
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
version=$("$binary" --version)
printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
printf '%s\n' "$repository" | grep -Eq '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$'

release_dir="/usr/local/lib/dronservice/releases/${version}"
install -d -o root -g root -m 0755 "$release_dir" /usr/local/libexec /usr/local/etc /etc/dronservice
install -d -o admin -g admin -m 0750 /usr/local/etc/mediamtx
install -o root -g root -m 0755 "$binary" "${release_dir}/dronservice"
install -o root -g root -m 0755 "${script_dir}/update-dronservice.sh" /usr/local/libexec/dronservice-update
install -o root -g root -m 0755 "${script_dir}/install-mediamtx.sh" /usr/local/libexec/dronservice-install-mediamtx
install -o root -g root -m 0644 "${script_dir}/dronservice-release.pub" /usr/local/etc/dronservice-release.pub
install -o root -g root -m 0644 "${script_dir}/dronservice.service" /etc/systemd/system/dronservice.service
install -o root -g root -m 0644 "${script_dir}/dronservice-update.service" /etc/systemd/system/dronservice-update.service
install -o root -g root -m 0644 "${script_dir}/dronservice-update.path" /etc/systemd/system/dronservice-update.path
install -o root -g root -m 0644 "${script_dir}/dronservice-mediamtx-install.service" /etc/systemd/system/dronservice-mediamtx-install.service
install -o root -g root -m 0644 "${script_dir}/dronservice-mediamtx-install.path" /etc/systemd/system/dronservice-mediamtx-install.path
install -o root -g root -m 0644 "${script_dir}/mediamtx.service" /etc/systemd/system/mediamtx.service

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

next_current=/usr/local/lib/dronservice/.current-bootstrap
next_binary=/usr/local/bin/.dronservice-bootstrap
rm -f "$next_current" "$next_binary"
ln -s "$release_dir" "$next_current"
mv -Tf "$next_current" /usr/local/lib/dronservice/current
ln -s /usr/local/lib/dronservice/current/dronservice "$next_binary"
mv -Tf "$next_binary" /usr/local/bin/dronservice

systemctl daemon-reload
systemctl enable --now dronservice-update.path dronservice-mediamtx-install.path
systemctl enable dronservice.service
systemctl restart dronservice.service

attempt=1
while [ "$attempt" -le 30 ]; do
	if curl --fail --silent http://127.0.0.1/api/health >/dev/null 2>&1 && \
		curl --fail --silent http://127.0.0.1/api/version | grep -Fq "\"version\":\"${version}\""; then
		exit 0
	fi
	sleep 1
	attempt=$((attempt + 1))
done
exit 1
