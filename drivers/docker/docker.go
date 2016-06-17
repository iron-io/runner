package docker

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/fsouza/go-dockerclient"
	"github.com/heroku/docker-registry-client/registry"
	titancommon "github.com/iron-io/titan/common"
	"github.com/iron-io/titan/runner/agent"
	"github.com/iron-io/titan/runner/drivers"
	drivercommon "github.com/iron-io/titan/runner/drivers/common"
	"golang.org/x/net/context"
)

// We have different clients who have different ways of providing credentials
// to access private images. Some of these clients try to do multiple pulls
// with separate credentials and hope that one of them works.
type Auther interface {
	// Return a list of AuthConfigurations to try.
	// They are tried in sequence and the first one to work is picked.
	// If you want to try without credentials, return a one element config with a default AuthConfiguration.
	// If a zero-element array is returned, no Pull is performed!
	// Named with Docker because tasks may want to implement multiple auth interfaces.
	DockerAuth() []docker.AuthConfiguration
}

type DockerDriver struct {
	conf     *drivercommon.Config
	docker   *docker.Client
	hostname string

	// Map from image repository (everything except the tag) to an array of
	// credentials known to be OK to access the image.  This is never revoked, so
	// even if the image permission on the Registry is revoked, users can
	// continue to queue tasks.  This should not be a problem in practice because
	// customers should also revoke the user on the Tasker.
	authCacheLock sync.RWMutex
	authCache     map[string][]docker.AuthConfiguration
	*titancommon.Environment
}

func NewDocker(env *titancommon.Environment, conf *drivercommon.Config) *DockerDriver {
	hostname, err := os.Hostname()
	if err != nil {
		logrus.WithError(err).Fatal("couldn't resolve hostname")
	}

	// docker, err := docker.NewClient(conf.Docker)
	client, err := docker.NewClientFromEnv()
	if err != nil {
		logrus.WithError(err).Fatal("couldn't create docker client")
	}

	return &DockerDriver{
		conf:        conf,
		docker:      client,
		hostname:    hostname,
		authCache:   make(map[string][]docker.AuthConfiguration),
		Environment: env,
	}
}

// Run executes the docker container. If task runs, drivers.RunResult will be returned. If something fails outside the task (ie: Docker), it will return error.
// The docker driver will attempt to cast the task to a Auther. If that succeeds, private image support is available. See the Auther interface for how to implement this.
func (drv *DockerDriver) Run(ctx context.Context, task drivers.ContainerTask) (drivers.RunResult, error) {
	container, err := drv.startTask(task)
	if err != nil {
		return nil, err
	}
	defer drv.removeContainer(container)

	sentence := make(chan string, 1)

	go drv.nanny(ctx, container, task, sentence)

	mwOut, mwErr := task.Logger()

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

	status, err := drv.status(container, exitCode, sentence)

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
		HostConfig: &docker.HostConfig{
			LogConfig: docker.LogConfig{
				// NOTE: this will make a docker log with 1 line, using 'none' meant we could not get logs after attaching.
				// attaching to the container will still give the full task log output, this just keeps docker from doubly
				// logging the same thing (in their very inefficient format) internally.
				Type:   "json-file",
				Config: map[string]string{"max-size": "0"},
			},
		},
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

	err := drv.ensureUsableImage(task)
	if err != nil {
		return "", fmt.Errorf("ensureUsableImage: %v", err)
	}

	createTimer := drv.NewTimer("docker", "create_container", 1.0)
	c, err := drv.docker.CreateContainer(container)
	createTimer.Measure()
	if err != nil {
		logDockerContainerConfig(log, container)
		drv.Inc("docker", "container_create_error", 1, 1.0)
		return "", createContainerErrorf("docker.CreateContainer: %v", err)
	}

	return c.ID, nil
}

func createContainerErrorf(format string, err error) error {
	errmsg := fmt.Errorf(format, err)

	if err == docker.ErrConnectionRefused {
		return &agent.UnrecoverableError{errmsg}
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

func (drv *DockerDriver) pullImage(task drivers.ContainerTask) error {
	configs := usableConfigs(task)
	if len(configs) == 0 {
		return fmt.Errorf("No AuthConfigurations specified! Did not attempt pull. Bad Auther implementation?")
	}

	repo, tag := docker.ParseRepositoryTag(task.Image())
	pullTimer := drv.NewTimer("docker", "pull_image", 1.0)
	var err error
	for i, config := range configs {
		err = drv.docker.PullImage(docker.PullImageOptions{Repository: repo, Tag: tag}, config)
		// Don't leak config into logs! Go lookup array in user's credentials if user complains.
		logrus.WithFields(logrus.Fields{"config_index": i, "err": err}).Info("Trying to pull image")
		if err == nil {
			// If a pull with a default AuthConfiguration works, we will insert that
			// and the image will be considered publicly accessible.
			drv.acceptedCredentials(task.Image(), config)
			break
		}
	}
	pullTimer.Measure()
	return err
}

func normalizedImage(image string) (string, string) {
	repo, tag := docker.ParseRepositoryTag(image)
	// Officially sanctioned at https://github.com/docker/docker/blob/master/registry/session.go#L319 to deal with "Official Repositories".
	// Without this, token auth fails.
	if strings.Count(repo, "/") == 0 {
		repo = "library/" + repo
	}
	if tag == "" {
		tag = "latest"
	}
	return repo, tag
}

// Empty arrays cannot use any image; use a single element default AuthConfiguration for public.
// Returns true if any of the configs presented exist in the cached configs.
func (drv *DockerDriver) allowedToUseImage(image string, configs []docker.AuthConfiguration) bool {
	logrus.WithFields(logrus.Fields{"image": image, "check": configs}).Info("AllowedToUse called")
	// Tags are not part of the permission model.
	key, _ := normalizedImage(image)
	drv.authCacheLock.RLock()
	defer drv.authCacheLock.RUnlock()
	if _, exists := drv.authCache[key]; exists {
		cached := drv.authCache[key]
		for _, config := range configs {
			for _, knownConfig := range cached {
				logrus.WithFields(logrus.Fields{"image": image, "known": knownConfig, "check": config}).Info("Checking")
				if config.Email == knownConfig.Email &&
					config.Username == knownConfig.Username &&
					config.Password == knownConfig.Password &&
					config.ServerAddress == knownConfig.ServerAddress {
					logrus.WithFields(logrus.Fields{"image": image}).Info("Cached credentials matched")
					return true
				}
			}
		}
	} else {
		logrus.Info("Key does not exist")
	}

	return false
}

func (drv *DockerDriver) acceptedCredentials(image string, config docker.AuthConfiguration) {
	// Tags are not part of the permission model.
	key, _ := normalizedImage(image)
	drv.authCacheLock.Lock()
	defer drv.authCacheLock.Unlock()
	if _, exists := drv.authCache[key]; !exists {
		drv.authCache[key] = make([]docker.AuthConfiguration, 0, 1)
	}

	drv.authCache[key] = append(drv.authCache[key], config)
}

func usableConfigs(task drivers.ContainerTask) []docker.AuthConfiguration {
	var auther Auther
	if cast, ok := task.(Auther); ok {
		auther = cast
	}

	var configs []docker.AuthConfiguration
	if auther != nil {
		configs = auther.DockerAuth()
	} else {
		// If the task does not support auth, we don't want to skip pull entirely.
		configs = []docker.AuthConfiguration{docker.AuthConfiguration{}}
	}

	return configs
}

func (drv *DockerDriver) ensureUsableImage(task drivers.ContainerTask) error {
	repoImage := task.Image()
	_, err := drv.docker.InspectImage(repoImage)
	if err == docker.ErrNoSuchImage {
		// Attempt a pull with task's credentials, If credentials work, add them to the cached set (handled by pull).
		return drv.pullImage(task)
	} else if err != nil {
		return fmt.Errorf("something went wrong inspecting image: %v", err)
	}

	// Image is available locally. If the credentials presented by the tasks are
	// known to be good for this image, allow it, otherwise check with registry.
	configs := usableConfigs(task)
	if drv.allowedToUseImage(repoImage, configs) {
		return nil
	}

	return drv.checkAgainstRegistry(task, configs)
}

func (drv *DockerDriver) checkAgainstRegistry(task drivers.ContainerTask, configs []docker.AuthConfiguration) error {
	// We need to be able to support multiple registries, so just reconnect
	// each time (hope to not do this often actually, as this is mostly to
	// prevent abuse).
	// It may be worth optimising in the future to have a dedicated HTTP client for Docker Hub.
	drv.Inc("docker", "hit_registry_for_auth_check", 1, 1.0)
	logrus.WithFields(logrus.Fields{"task_id": task.Id()}).Info("Hitting registry to check image access permission.")

	var regClient *registry.Registry
	var err error
	// On authorization failure for the specific image, we try the next config,
	// on any other failure, we fail immediately.
	// This way things like being unable to connect to a registry due to some
	// weird reason (server format different etc.) pop up immediately and will
	// aide in debugging when users complain, instead of the error message being
	// lost in subsequent loops.
	for i, config := range configs {
		regClient, err = registryForConfig(config)
		if err != nil {
			logrus.WithFields(logrus.Fields{"config_index": i}).WithError(err).Error("Could not connect to registry")
			break
		}

		repo, tag := normalizedImage(task.Image())
		_, err = regClient.Manifest(repo, tag)

		if err != nil {
			if isAuthError(err) {
				logrus.WithFields(logrus.Fields{"config_index": i}).Info("Credentials not authorized, trying next.")
				continue
			}

			logrus.WithFields(logrus.Fields{"config_index": i}).WithError(err).Error("Error retrieving manifest")
			break
		}

		drv.acceptedCredentials(task.Image(), config)
		return nil
	}
	return fmt.Errorf("task not authorized to use image %s: %v", task.Image(), err)
}

// Only support HTTPS accessible registries for now.
func registryForConfig(config docker.AuthConfiguration) (*registry.Registry, error) {
	addr := config.ServerAddress
	if strings.Contains(addr, "docker.io") || addr == "" {
		addr = "index.docker.io"
	}
	reg, err := registry.New(fmt.Sprintf("https://%s", addr), config.Username, config.Password)
	if err == nil {
		reg.Logf = registry.Quiet
	}

	return reg, err
}

func isAuthError(err error) bool {
	// AARGH!
	if urlError, ok := err.(*url.Error); ok {
		if httpError, ok := urlError.Err.(*registry.HttpStatusError); ok {
			if httpError.Response.StatusCode == 401 {
				return true
			}
		}
	}

	return false
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

func (drv *DockerDriver) status(container string, exitCode int, sentence <-chan string) (string, error) {
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

			cinfo, err := drv.docker.InspectContainer(container)
			if err != nil {
				logrus.WithError(err).WithFields(logrus.Fields{"container": container}).Error("inspecting container to check for oom")
				drv.Inc("docker", "possible_oom_inspect_container_error", 1, 1.0)
			} else {
				if !cinfo.State.OOMKilled {
					// It is possible that the host itself is running out of memory and
					// the host kernel killed one of the container processes.
					// See: https://github.com/docker/docker/issues/15621
					logrus.WithFields(logrus.Fields{"container": container}).Info("Setting task as OOM killed, but docker disagreed.")
					drv.Inc("docker", "possible_oom_false_alarm", 1, 1.0)
					// TODO: This may be a situation where we would like to shut down the runner completely.
				}
			}

			status = drivers.StatusKilled
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
