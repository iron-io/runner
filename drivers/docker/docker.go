package docker

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/context"

	log "github.com/Sirupsen/logrus"
	"github.com/fsouza/go-dockerclient"
	"github.com/iron-io/titan/runner/drivers"
	"github.com/iron-io/titan/runner/drivers/common"
)

const (
	// configFile  = ".task_config"
	logFile = "job.log"
	// payloadFile = ".task_payload"
	// runtimeDir  = "/mnt"
	// taskDir     = "/task"
)

type DockerDriver struct {
	conf     *common.Config
	docker   *docker.Client
	hostname string
	rand     *rand.Rand
	// runtimeDir string
}

func NewDocker(conf *common.Config, hostname string) (*DockerDriver, error) {
	// docker, err := docker.NewClient(conf.Docker)
	docker, err := docker.NewClientFromEnv()
	if err != nil {
		return nil, err
	}

	// create local directory to store log files
	// use MkdirAll() to avoid failure if dir already exists.
	err = os.MkdirAll(conf.JobsDir, 0777)
	if err != nil {
		log.Errorln("could not create", conf.JobsDir, "directory!")
		return nil, err
	}

	return &DockerDriver{
		conf:     conf,
		docker:   docker,
		hostname: hostname,
		rand:     rand.New(rand.NewSource(time.Now().Unix())),
	}, nil
}

// Run executes the docker container. If task runs, drivers.RunResult will be returned. If something fails outside the task (ie: Docker), it will return error.
// todo: pass in context
func (drv *DockerDriver) Run(ctx context.Context, task drivers.ContainerTask) (drivers.RunResult, error) {
	// Can't remove taskDir at the end of this, log file in there, caller should deal with it.
	taskDirName := drv.newTaskDirName(task)

	if err := os.Mkdir(taskDirName, 0777); err != nil {
		return nil, err
	}

	container, err := drv.startTask(task, taskDirName)
	if container != "" {
		// It is possible that startTask created a container but could not start it. So always try to remove a valid container.
		defer drv.removeContainer(container)
	}
	if err != nil {
		return nil, err
	}

	sentence := make(chan string, 1)

	go drv.nanny(ctx, container, task, sentence)

	log := task.Logger()
	if log == nil {
		return nil, fmt.Errorf("Received nil logger")
	}

	w := &limitedWriter{W: log, N: 8 * 1024 * 1024 * 1024} // TODO get max log size from somewhere

	// Docker sometimes fails to close the attach response connection even after
	// the container stops, leaving the runner stuck. We use a non-blocking
	// attach so we can sleep a bit after WaitContainer returns and then forcibly
	// close the connection.
	closer, err := drv.docker.AttachToContainerNonBlocking(docker.AttachToContainerOptions{
		Container: container, OutputStream: w, ErrorStream: w,
		Stream: true, Logs: true, Stdout: true, Stderr: true})
	defer closer.Close()
	if err != nil {
		return nil, err
	}

	// It's possible the execution could be finished here, then what? http://docs.docker.com.s3-website-us-east-1.amazonaws.com/engine/reference/api/docker_remote_api_v1.20/#wait-a-container
	exitCode, err := drv.docker.WaitContainer(container)
	time.Sleep(10 * time.Millisecond)
	if err != nil {
		return nil, err
	}
	status, err := drv.status(exitCode, sentence)
	// the err returned above is an error from running user code, so we don't return it from this method.
	return &runResult{
		StatusValue: status,
		Dir:         taskDirName,
		Err:         err,
	}, nil
}

func (drv *DockerDriver) newTaskDirName(task drivers.ContainerTask) string {
	// Add a random suffix so that in the rare/erroneous case that we get a repeat job ID, we don't behave badly.
	gen := drv.rand.Uint32()
	return filepath.Join(drv.conf.JobsDir, fmt.Sprintf("%s-%d", task.Id(), gen))
}

func (drv *DockerDriver) error(err error) *runResult {
	return &runResult{
		Err:         err,
		StatusValue: drivers.StatusError,
	}
}

func (drv *DockerDriver) startTask(task drivers.ContainerTask, hostTaskDir string) (dockerId string, err error) {
	if task.Image() == "" {
		// TODO support for old
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
	envvars = append(envvars, "JOB_ID="+task.Id())
	envvars = append(envvars, "PAYLOAD="+task.Payload())
	absTaskDir, err := filepath.Abs(hostTaskDir)
	if err != nil {
		return "", err
	}

	cID, err := drv.createContainer(envvars, cmd, task.Image(), absTaskDir, task.Auth())
	if err != nil {
		return "", err
	}

	err = drv.docker.StartContainer(cID, nil)
	return cID, err
}

func writeFile(name, body string) error {
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, strings.NewReader(body))
	return err
}

func (drv *DockerDriver) createContainer(envvars, cmd []string, image string, absTaskDir string, auth string) (string, error) {
	l := log.WithFields(log.Fields{
		"image": image,
		// todo: add context fields here, job id, etc.
	})
	container := docker.CreateContainerOptions{
		Config: &docker.Config{
			Env:       envvars,
			Cmd:       cmd,
			Memory:    drv.conf.Memory,
			CPUShares: drv.conf.CPUShares,
			Hostname:  drv.hostname,
			Image:     image,
			// Volumes: map[string]struct{}{
			// drv.runtimeDir: {},
			// },
		},
		// HostConfig: &docker.HostConfig{
		// Binds: []string{absTaskDir + ":" + drv.runtimeDir},
		// },
	}

	c, err := drv.docker.CreateContainer(container)
	if err != nil {
		if err != docker.ErrNoSuchImage {
			return "", err
		}
		l.WithError(err).Infoln("could not create container, trying to pull...")

		regHost := "docker.io"
		repo, tag := docker.ParseRepositoryTag(image)
		split := strings.Split(repo, "/")
		if len(split) >= 3 {
			// then we have an explicit registry
			regHost = split[0]
		}
		// todo: we should probably move all this auth stuff up a level, don't need to do it for every job
		authConfig := docker.AuthConfiguration{}
		if auth != "" {
			l.Debugln("Using auth", auth)
			read := strings.NewReader(fmt.Sprintf(`{"%s":{"auth":"%s"}}`, regHost, auth))
			ac, err := docker.NewAuthConfigurations(read)
			if err != nil {
				return "", err
			}
			authConfig = ac.Configs[regHost]
		}

		err = drv.docker.PullImage(docker.PullImageOptions{Repository: repo, Tag: tag}, authConfig)
		if err != nil {
			return "", err
		}

		// should have it now
		c, err = drv.docker.CreateContainer(container)
		if err != nil {
			return "", err
		}
	}
	return c.ID, nil
}

func (drv *DockerDriver) removeContainer(container string) {
	// TODO: trap error
	drv.docker.RemoveContainer(docker.RemoveContainerOptions{
		ID: container, Force: true, RemoveVolumes: true})
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
			err = drivers.ErrUnknown
		}
	}
	return status, err
}

// TODO we _sure_ it's dead?
func (drv *DockerDriver) cancel(container string) { drv.docker.StopContainer(container, 5) }
