set -e

./build.sh

docker run --rm -v "$PWD":/app treeder/bump patch
version=`cat VERSION`
echo "version $version"

docker tag iron/dind:latest iron/dind:$version

cd go-dind
echo "go-dind pwd $PWD"
docker tag iron/go-dind:latest iron/go-dind:$version


docker push iron/dind
docker push iron/go-dind
