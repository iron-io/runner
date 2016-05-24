#!/bin/sh
set -ex

# modified from: https://github.com/docker-library/docker/blob/866c3fbd87e8eeed524fdf19ba2d63288ad49cd2/1.11/dind/dockerd-entrypoint.sh

# if [ "$#" -eq 0 -o "${1:0:1}" = '-' ]; then
docker daemon \
		--host=unix:///var/run/docker.sock \
		--host=tcp://0.0.0.0:2375 \
		--storage-driver=vfs > /var/log/docker.log 2>&1 &
# fi

# if [ "$1" = 'docker' -a "$2" = 'daemon' ]; then
# 	# if we're running Docker, let's pipe through dind
# 	# (and we'll run dind explicitly with "sh" since its shebang is /bin/bash)
# 	set -- sh "$(which dind)" "$@"
# fi

exec "$@"
