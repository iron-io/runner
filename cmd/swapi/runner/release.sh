#!/bin/bash
set -ex

service="runner"
version_file="main.go"
tag="beta"

if [ $(git rev-parse --abbrev-ref HEAD) != "prod" ]; then
  echo "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
  exit 1
fi

if [ -z $(grep -Eo "[0-9]+\.[0-9]+\.[0-9]+" $version_file) ]; then
  echo "did not find semantic version in $version_file"
  exit 1
fi

git config --global user.name "Release Man" && git config --global user.email "deploys@iron.io"
git checkout master
git pull origin master
perl -i -pe 's/\d+\.\d+\.\K(\d+)/$1+1/e' $version_file
version=$(grep -Eo "[0-9]+\.[0-9]+\.[0-9]+" $version_file)
echo "Version: $version"
git add -u
git commit -m "$service: $version release"
git tag -a "$version" -m "runner version $version"
git push origin master
git push --tags

./build.sh

# Finally tag and push docker images
docker tag iron/runner:$tag iron/runner:$version
docker push iron/runner:$version
docker push iron/runner:$tag
