#!/bin/sh
set -eu
request_file="/var/lib/dronservice/update-zerotier.request"
trap 'rm -f "$request_file"' EXIT
if ! dpkg-query -W zerotier-one >/dev/null 2>&1; then
  work_dir="$(mktemp -d)"
  trap 'rm -rf "$work_dir"; rm -f "$request_file"' EXIT
  curl --fail --location --proto '=https' --tlsv1.2 https://install.zerotier.com/ --output "${work_dir}/install.sh"
  sh "${work_dir}/install.sh"
else
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y --only-upgrade zerotier-one
fi
systemctl enable --now zerotier-one.service
