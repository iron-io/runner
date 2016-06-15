package docker

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/fsouza/go-dockerclient"
	titancommon "github.com/iron-io/titan/common"
	"github.com/iron-io/titan/runner/agent"
	"github.com/iron-io/titan/runner/drivers"
	drivercommon "github.com/iron-io/titan/runner/drivers/common"
	"golang.org/x/net/context"
)

type DockerDriver struct {
	conf     *drivercommon.Config
	docker   *docker.Client
	hostname string
	*titancommon.Environment
}

func NewDocker(env *titancommon.Environment, conf *drivercommon.Config) *DockerDriver {
	hostname, err := os.Hostname()
	if err != nil {
		logrus.WithError(err).Fatal("couldn't resolve hostname")
	}

	// docker, err := docker.NewClient(conf.Docker)
	docker, err := docker.NewClientFromEnv()
	if err != nil {
		logrus.WithError(err).Fatal("couldn't create docker client")
	}

	return &DockerDriver{
		conf:        conf,
		docker:      docker,
		hostname:    hostname,
		Environment: env,
	}
}

// Run executes the docker container. If task runs, drivers.RunResult will be returned. If something fails outside the task (ie: Docker), it will return error.
// todo: pass in context
func (drv *DockerDriver) Run(ctx context.Context, task drivers.ContainerTask) (drivers.RunResult, error) {
	container, err := drv.startTask(task)
	if err != nil {
		return nil, err
	}
	defer drv.removeContainer(container)

	sentence := make(chan string, 1)

	go drv.nanny(ctx, container, task, sentence)

	outTasker, errTasker := task.Logger()

	// We may need to capture some lines here using the Head or Tail writers to find substrings regarding memory usage or other Docker errors.
	// IW-125
	mwOut := io.MultiWriter(outTasker)
	mwErr := io.MultiWriter(errTasker)

	// Docker sometimes fails to close the attach response connection even after
	// the container stops, leaving the runner stuck. We use a non-blocking
	// attach so we can sleep a bit after WaitContainer returns and then forcibly
	// close the connection.
	closer, err := drv.docker.AttachToContainerNonBlocking(docker.AttachToContainerOptions{
		Container: container, OutputStream: mwOut, ErrorStream: mwErr,
		Stream: true, Logs: true, Stdout: true, Stderr: true})
	defer closer.Close()
	if err != nil {
		return nil, fmt.Errorf("attach to container: %v", err)
	}

	// It's possible the execution could be finished here, then what? http://docs.docker.com.s3-website-us-east-1.amazonaws.com/engine/reference/api/docker_remote_api_v1.20/#wait-a-container
	exitCode, err := drv.docker.WaitContainer(container)
	time.Sleep(10 * time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("wait container: %v", err)
	}

	status, err := drv.status(exitCode, sentence)

	// TODO: Check stdout/stderr for driver-specific errors like OOM.

	// the err returned above is an error from running user code, so we don't return it from this method.
	return &runResult{
		StatusValue: status,
		Err:         err,
	}, nil
}

func (drv *DockerDriver) startTask(task drivers.ContainerTask) (dockerId string, err error) {
	cID, err := drv.createContainer(task)
	if err != nil {
		return "", err
	}

	startTimer := drv.NewTimer("docker", "start_container", 1.0)
	err = drv.docker.StartContainer(cID, nil)
	startTimer.Measure()
	if err != nil {
		if cID != "" {
			// Remove the created container since we couldn't start it.
			defer drv.removeContainer(cID)
		}
		drv.Inc("docker", "container_start_error", 1, 1.0)
		return "", fmt.Errorf("docker.StartContainer: %v", err)
	}
	return cID, nil
}

func (drv *DockerDriver) createContainer(task drivers.ContainerTask) (string, error) {
	log := logrus.WithFields(logrus.Fields{"image": task.Image()}) // todo: add context fields here, job id, etc.

	if task.Image() == "" {
		return "", errors.New("no image specified, this runner cannot run this")
	}

	var cmd []string
	if task.Command() != "" {
		// TODO: maybe check for spaces or shell meta characters?
		// There's a possibility that the container doesn't have sh.
		cmd = []string{"sh", "-c", task.Command()}
	}

	envvars := make([]string, 0, len(task.EnvVars())+4)
	for name, val := range task.EnvVars() {
		envvars = append(envvars, name+"="+val)
	}

	container := docker.CreateContainerOptions{
		Name: "task-" + task.Id(),
		Config: &docker.Config{
			Env:       envvars,
			Cmd:       cmd,
			Memory:    int64(drv.conf.Memory),
			CPUShares: drv.conf.CPUShares,
			Hostname:  drv.hostname,
			Image:     task.Image(),
			Volumes:   map[string]struct{}{},
		},
		HostConfig: &docker.HostConfig{},
	}

	volumes := task.Volumes()
	for _, mapping := range volumes {
		if len(mapping) != 2 {
			return "", fmt.Errorf("Invalid volume tuple: %v. Tuple must be 2-element", mapping)
		}

		hostDir := mapping[0]
		containerDir := mapping[1]
		container.Config.Volumes[containerDir] = struct{}{}
		mapn := fmt.Sprintf("%s:%s", hostDir, containerDir)
		container.HostConfig.Binds = append(container.HostConfig.Binds, mapn)
		log.WithFields(logrus.Fields{"volumes": mapn}).Debug("setting volumes")
	}

	if wd := task.WorkDir(); wd != "" {
		log.WithFields(logrus.Fields{"wd": wd}).Debug("setting work dir")
		container.Config.WorkingDir = wd
	}

	createTimer := drv.NewTimer("docker", "create_container", 1.0)
	c, err := drv.docker.CreateContainer(container)
	createTimer.Measure()

	if err == docker.ErrNoSuchImage {
		log.Info("could not create container due to missing image, trying to pull...")

		regHost := "docker.io"
		repo, tag := docker.ParseRepositoryTag(task.Image())
		split := strings.Split(repo, "/")
		if len(split) >= 3 {
			// then we have an explicit registry
			regHost = split[0]
		}
		// todo: we should probably move all this auth stuff up a level, don't need to do it for every job
		authConfig := docker.AuthConfiguration{}
		auth := task.Auth()
		if auth != "" {
			log.WithFields(logrus.Fields{"auth": auth}).Debug("Using auth")
			read := strings.NewReader(fmt.Sprintf(`{"%s":{"auth":"%s"}}`, regHost, auth))
			ac, err := docker.NewAuthConfigurations(read)
			if err != nil {
				return "", fmt.Errorf("failed to create auth configurations: %v", err)
			}
			authConfig = ac.Configs[regHost]
		}

		pullTimer := drv.NewTimer("docker", "pull_image", 1.0)
		err = drv.docker.PullImage(docker.PullImageOptions{Repository: repo, Tag: tag}, authConfig)
		pullTimer.Measure()
		if err != nil {
			return "", fmt.Errorf("docker.PullImage: %v", err)
		}

		// should have it now
		createTimer := drv.NewTimer("docker", "create_container", 1.0)
		c, err = drv.docker.CreateContainer(container)
		createTimer.Measure()
		if err != nil {
			logDockerContainerConfig(log, container)
			drv.Inc("docker", "container_create_error", 1, 1.0)
			return "", createContainerErrorf("docker.CreateContainer try 2: %v", err)
		}
	} else if err != nil {
		logDockerContainerConfig(log, container)
		drv.Inc("docker", "container_create_error", 1, 1.0)
		return "", createContainerErrorf("docker.CreateContainer: %v", err)
	}

	return c.ID, nil
}

func createContainerErrorf(format string, err error) error {
	errmsg := fmt.Errorf(format, err)

	if err == docker.ErrConnectionRefused {
		return agent.UnrecoverableError(errmsg)
	}

	return errmsg
}

func (drv *DockerDriver) removeContainer(container string) {
	removeTimer := drv.NewTimer("docker", "remove_container", 1.0)
	// TODO: trap error
	drv.docker.RemoveContainer(docker.RemoveContainerOptions{
		ID: container, Force: true, RemoveVolumes: true})
	removeTimer.Measure()
}

// watch for cancel or timeout and kill process.
func (drv *DockerDriver) nanny(ctx context.Context, container string, task drivers.ContainerTask, sentence chan<- string) {
	select {
	case <-ctx.Done():
		switch ctx.Err() {
		case context.DeadlineExceeded:
			sentence <- drivers.StatusTimeout
			drv.cancel(container)
		case context.Canceled:
			sentence <- drivers.StatusKilled
			drv.cancel(container)
		}
	}
}

func (drv *DockerDriver) status(exitCode int, sentence <-chan string) (string, error) {
	var status string
	var err error
	select {
	case status = <-sentence: // use this if killed / timed out
	default:
		switch exitCode {
		case 0:
			status = drivers.StatusSuccess
		case 137:
			// Probably an OOM kill

			// TODO: try harder to detect OOM kills. We can call
			// docker.InspectContainer and look at
			// container.State.OOMKilled, but this field isn't set
			// consistently.
			// See: https://github.com/docker/docker/issues/15621

			status = drivers.StatusKilled
			// TODO: better message; show memory limit
			err = drivers.ErrOutOfMemory
		default:
			status = drivers.StatusError
			err = fmt.Errorf("exit code %d", exitCode)
		}
	}
	return status, err
}

// TODO we _sure_ it's dead?
func (drv *DockerDriver) cancel(container string) {
	stopTimer := drv.NewTimer("docker", "stop_container", 1.0)
	drv.docker.StopContainer(container, 5)
	// We will get a large skew due to the by default 5 second wait. Should we log times after subtracting this?
	stopTimer.Measure()
}

func logDockerContainerConfig(log logrus.FieldLogger, container docker.CreateContainerOptions) {
	// envvars are left out because they could have private information.
	log.WithFields(logrus.Fields{
		"command":    container.Config.Cmd,
		"memory":     container.Config.Memory,
		"cpu_shares": container.Config.CPUShares,
		"hostname":   container.Config.Hostname,
		"image":      container.Config.Image,
		"volumes":    container.Config.Volumes,
		"binds":      container.HostConfig.Binds,
	}).Error("Could not create container")
}
