#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
	echo "run as root" >&2
	exit 1
fi

if [ ! -x /usr/bin/ffmpeg ]; then
	if ! command -v apt-get >/dev/null 2>&1; then
		echo "apt-get is required to install the analog video runtime" >&2
		exit 2
	fi
	apt-get update
	DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ffmpeg
fi

[ -x /usr/bin/ffmpeg ]
/usr/bin/ffmpeg -hide_banner -version >/dev/null

if ! /usr/bin/ffmpeg -hide_banner -encoders 2>/dev/null | grep -q '[[:space:]]libx264[[:space:]]'; then
	echo "ffmpeg must provide the libx264 encoder" >&2
	exit 3
fi
if ! /usr/bin/ffmpeg -hide_banner -demuxers 2>/dev/null | grep -q '[[:space:]]video4linux2,v4l2[[:space:]]'; then
	echo "ffmpeg must provide the V4L2 input demuxer" >&2
	exit 3
fi
if ! /usr/bin/ffmpeg -hide_banner -muxers 2>/dev/null | grep -q '[[:space:]]rtsp[[:space:]]'; then
	echo "ffmpeg must provide the RTSP output muxer" >&2
	exit 3
fi
