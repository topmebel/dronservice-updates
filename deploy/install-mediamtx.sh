#!/bin/sh
set -eu

version="$(tr -d '\r\n' < /var/lib/dronservice/install-mediamtx.request)"
case "$version" in v[0-9]*.[0-9]*.[0-9]*) ;; *) exit 2 ;; esac
archive="mediamtx_${version}_linux_arm64.tar.gz"
request_file="/var/lib/dronservice/install-mediamtx.request"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"; rm -f "$request_file"' EXIT

base_url="https://github.com/bluenviron/mediamtx/releases/download/${version}"
curl --fail --location --proto '=https' --tlsv1.2 "${base_url}/${archive}" --output "${work_dir}/${archive}"
curl --fail --location --proto '=https' --tlsv1.2 "${base_url}/checksums.sha256" --output "${work_dir}/checksums.sha256"
expected_checksum="$(awk -v name="$archive" '$2 == "*" name || $2 == name {print $1}' "${work_dir}/checksums.sha256")"
[ -n "$expected_checksum" ]
printf '%s  %s\n' "$expected_checksum" "${work_dir}/${archive}" | sha256sum --check --status
tar -xzf "${work_dir}/${archive}" -C "$work_dir" mediamtx
install -o root -g root -m 0755 "${work_dir}/mediamtx" /usr/local/bin/mediamtx
install -d -o root -g root -m 0755 /usr/local/etc

if [ ! -e /usr/local/etc/mediamtx.yml ]; then
  install -o root -g root -m 0644 /dev/null /usr/local/etc/mediamtx.yml
  printf '%s\n' \
    'logLevel: info' \
    'api: yes' \
    'apiAddress: 127.0.0.1:9997' \
    'rtsp: yes' \
    'rtspAddress: :554' \
    'hls: yes' \
    'hlsAddress: :8888' \
    'moq: no' \
    'paths:' \
    '  all_others:' > /usr/local/etc/mediamtx.yml
fi

systemctl daemon-reload
systemctl enable --now mediamtx.service
