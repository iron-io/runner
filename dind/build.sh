set -ex

docker build -t iron/dind:latest .

cd go-dind
docker build -t iron/go-dind:latest .
