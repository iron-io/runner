#!/bin/sh
set -ex

# modified from: https://github.com/docker-library/docker/blob/866c3fbd87e8eeed524fdf19ba2d63288ad49cd2/1.11/dind/dockerd-entrypoint.sh
# this will run either overlay or aufs as the docker fs driver, if the OS has both, overlay is preferred.
# rewrite overlay to use overlay2 (docker 1.12, linux >=4.x required), see https://docs.docker.com/engine/userguide/storagedriver/selectadriver/#overlay-vs-overlay2

fsdriver=$(grep -Eh -w -m1 "overlay|aufs" /proc/filesystems | cut -f2)

if [ $fsdriver == "overlay" ]; then
  fsdriver="overlay2"
fi

docker daemon \
		--host=unix:///var/run/docker.sock \
		--host=tcp://0.0.0.0:2375 \
		--storage-driver=$fsdriver &

# wait for daemon to initialize
sleep 3

exec "$@"
