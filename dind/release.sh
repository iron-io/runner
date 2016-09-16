set -e

./build.sh

docker run --rm -v "$PWD":/app treeder/bump patch
version=`cat VERSION`
echo "version $version"

docker tag iron/dind:latest iron/dind:$version

docker push iron/dind:latest
docker push iron/dind:$version
