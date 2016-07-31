set -ex

TITAN_DIR=$(dirname $(dirname $(dirname $(dirname $PWD))))
docker run --rm -v $TITAN_DIR:/go/src/github.com/iron-io/titan -w /go/src/github.com/iron-io/titan/runner/cmd/swapi/runner iron/go:dev sh -c 'go build'
docker build -t iron/runner:beta .
