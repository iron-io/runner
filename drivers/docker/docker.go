package docker

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	// runtimeDir string
}

func NewDocker(conf *common.Config, hostname string) (*DockerDriver, error) {
	// docker, err := docker.NewClient(conf.Docker)
	docker, err := docker.NewClientFromEnv()
	if err != nil {
		return nil, err
	}

	// create local directory to store log files
	err = os.Mkdir(conf.JobsDir, 0777)
	if err != nil {
		log.Errorln("could not create", conf.JobsDir, "directory!")
		return nil, err
	}

	return &DockerDriver{
		conf:     conf,
		docker:   docker,
		hostname: hostname,
	}, nil
}

func (drv *DockerDriver) Run(task drivers.ContainerTask, isCancelled chan bool) drivers.RunResult {
	defer os.RemoveAll(drv.taskDir(task))

	if err := drv.ensureTaskDir(task); err != nil {
		return drv.error(err)
	}

	container, err := drv.startTask(task)
	if container != "" {
		// It is possible that startTask created a container but could not start it. So always try to remove a valid container.
		defer drv.removeContainer(container)
	}

	if err != nil {
		return drv.error(err)
	}

	sentence := make(chan string, 1)

	done := make(chan struct{})
	defer close(done)
	go drv.nanny(container, task, sentence, done, isCancelled)

	log, err := drv.ensureLogFile(task)
	if err != nil {
		return drv.error(err)
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
		return drv.error(err)
	}

	exitCode, err := drv.docker.WaitContainer(container)
	time.Sleep(10 * time.Millisecond)

	done <- struct{}{}
	if err != nil {
		return drv.error(err)
	}
	status, err := drv.status(exitCode, sentence)
	return &runResult{
		StatusValue: status,
		LogData:     log,
		Err:         err,
	}
}

func (drv *DockerDriver) taskDir(task drivers.ContainerTask) string {
	return drv.conf.JobsDir + "/" + task.Id()
}

func (drv *DockerDriver) ensureTaskDir(task drivers.ContainerTask) error {
	return os.Mkdir(drv.taskDir(task), 0777)
}

func (drv *DockerDriver) ensureLogFile(task drivers.ContainerTask) (*os.File, error) {
	log, err := os.Create(drv.taskDir(task) + "/" + logFile)
	if err != nil {
		return nil, fmt.Errorf("%v %v", "couldn't open task log", err)
	}
	return log, nil
}

func (drv *DockerDriver) error(err error) *runResult {
	return &runResult{
		Err:         err,
		StatusValue: drivers.StatusError,
	}
}

func (drv *DockerDriver) startTask(task drivers.ContainerTask) (dockerId string, err error) {
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

	hostTaskDir := drv.taskDir(task)

	// config := hostTaskDir + "/" + configFile
	// payload := hostTaskDir + "/" + payloadFile
	// err = writeFile(config, task.Config())
	// if err != nil {
	// return "", err
	// }

	// err = writeFile(payload, task.Payload())
	// if err != nil {
	// return "", err
	// }

	envvars := make([]string, 0, len(task.EnvVars())+4)
	for name, val := range task.EnvVars() {
		envvars = append(envvars, name+"="+val)
	}
	envvars = append(envvars, "JOB_ID="+task.Id())
	envvars = append(envvars, "PAYLOAD="+task.Payload())
	// envvars = append(envvars, "PAYLOAD_FILE="+runtimePath(payloadFile))
	// envvars = append(envvars, "TASK_DIR="+runtimePath(taskDir))
	// envvars = append(envvars, "CONFIG_FILE="+runtimePath(configFile))
	absTaskDir, err := filepath.Abs(hostTaskDir)
	if err != nil {
		return "", err
	}

	cID, err := drv.createContainer(envvars, cmd, task.Image(), absTaskDir)
	if err != nil {
		return "", err
	}

	err = drv.docker.StartContainer(cID, nil)
	return cID, err
}

// func runtimePath(s ...string) string {
// return path.Join(append([]string{runtimeDir}, s...)...)
// }

func writeFile(name, body string) error {
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, strings.NewReader(body))
	return err
}

func (drv *DockerDriver) createContainer(envvars, cmd []string, image string, absTaskDir string) (string, error) {
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

		repo, tag := docker.ParseRepositoryTag(image)

		err = drv.docker.PullImage(docker.PullImageOptions{Repository: repo, Tag: tag}, docker.AuthConfiguration{}) // TODO AuthConfig from code
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
func (drv *DockerDriver) nanny(container string, task drivers.ContainerTask, sentence chan<- string, done chan struct{}, isCancelledSignal chan bool) {
	t := time.Duration(drv.conf.DefaultTimeout) * time.Second
	if task.Timeout() != 0 {
		t = time.Duration(task.Timeout()) * time.Second
	}
	// Just in case we make a calculation mistake.
	// TODO: trap this condition
	// if t > 24*time.Hour {
	// 	ctx.Warn("task has really long timeout of greater than a day")
	// }
	// Log task timeout values so we can get a good idea of what people generally set it to.
	timeout := time.After(t)

	select {
	case <-done:
		return
	case <-timeout:
		sentence <- drivers.StatusTimeout
		drv.cancel(container)
	case isCancelled := <-isCancelledSignal:
		if isCancelled {
			sentence <- drivers.StatusKilled
			drv.cancel(container)
		}
	}

	<-done
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
