set -ex

TITAN_DIR=$(dirname $PWD)
docker run --rm -v $TITAN_DIR:/go/src/github.com/iron-io/titan -w /go/src/github.com/iron-io/titan -w /go/src/github.com/iron-io/titan iron/go:dev sh -c 'cd runner && go build'
docker build -t iron/titan-runner:latest .
