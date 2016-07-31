# The New Runner

This directory contains the main.go and tasker for talking to swapi - the
IronWorker proprietary task scheduler.

## For reasonable people who value their time:

```sh
$ glide install
$ go build -o runner
$ CLUSTER_ID={CLUSTER_ID} CLUSTER_TOKEN={CLUSTER_TOKEN} JOBS_DIR=/tmp CONCURRENCY=4 ./runner
```

## Building as a Docker image

```sh
glide install
./build.sh
```

## Running

The common list of environment variables is in the [top-level
README](../README.md). The `DRIVER` variable is not respected, Docker is the
only one supported. The swapi client can be configured with:

* CLUSTER_ID - REQUIRED - Cluster ID for Hybrid and maybe public clusters. 
* CLUSTER_TOKEN - REQUIRED - Token given to Hybrid users when cluster is created and for our public clusters. 
  concurrency will automatically be set based on available memory. Default: 256MB. 
* JOBS_DIR - REQUIRED - Directory under which job code is downloaded and jobs are run.
  The runner must have write permissions to this.
* CACHE_SIZE - Maximum size in bytes for the code cache. Default: 1GB.

The runner can also be run as a Docker container.

```sh
docker run --privileged --rm -it -e "CLUSTER_ID={CLUSTER_ID}" -e "CLUSTER_TOKEN={CLUSTER_TOKEN}" iron/runner:beta
```

## Hybrid

You can find [Hybrid docs here](https://github.com/iron-io/docs/blob/gh-pages/worker/hybrid/README.md). 

## Notes about the swapi tasker

The swapi tasker is complicated compared to the Titan tasker. See the [tasker
implementation](swapi/gofer/swapi.go) for details.
