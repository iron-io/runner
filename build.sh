set -ex

docker run --rm -v "$(dirname $PWD)":/go/src/github.com/iron-io/titan -w /go/src/github.com/iron-io/titan -w /go/src/github.com/iron-io/titan iron/go:dev sh -c 'cd runner && go build'
docker build -t iron/titan-runner:latest .
