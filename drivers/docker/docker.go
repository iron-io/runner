package docker

import (
	"errors"
	"fmt"
	"net/http"
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

type ErrImageNotAllowed struct {
	msg string
}

func (e *ErrImageNotAllowed) Error() string { return e.msg }

func NewErrImageNotAllowed(msg string) error {
	return &ErrImageNotAllowed{
		msg: msg,
	}
}

// Clients may need to impose some restrictions on what images a task is
// allowed to execute. If the image is not locally available, there is
// a significant I/O cost to getting more information about the image, which is
// why we have a 2 step process.
// ContainerTask implementations can opt into this by implementing the
// AllowImager interface.
type AllowImager interface {
	// AllowImagePull controls whether pulling this particular image is allowed.
	// - If the image pull is to be allowed return nil.
	// - If the image can be accessed, but the pull is not allowed, return
	//   ErrImageNotAllowed.
	// - If the image could not be accessed, or some other reason, return some
	//   other error.
	AllowImagePull(repo string, authConfig docker.AuthConfiguration) error

	// AllowImage is always called before running a task.
	// This may be used to implement restrictions on execution of certain images.
	// Checks implemented here should be a superset of checks in AllowImagePull
	// to ensure reliable behavior.
	// To clarify, say TaskA and TaskB share the same image `foo/bar`. TaskA
	// enforces a limit of 50MB, TaskB enforces 10MB, `foo/bar` is 20MB. If TaskA
	// runs first, AllowImagePull for TaskA returns nil,  image is now in the
	// cache and TaskA is allowed to run. If TaskB is queued now, we need to
	// perform a check with TaskB's limits, even though AllowImagePull will not
	// be called for TaskB.
	//
	// Docker seems to have a bug where the output of docker inspect does not
	// always fill in the RepoTags argument. So we pass the original
	// repository name separately.
	AllowImage(repo string, info *docker.Image) error
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

	t := drv.conf.DefaultTimeout
	if n := task.Timeout(); n != 0 {
		t = n
	}
	if t == 0 {
		t = drivercommon.DefaultConfig().DefaultTimeout
		logrus.WithFields(logrus.Fields{"task_id": task.Id()}).Warn("Task timeout or runner configuration was set to zero, using default")
	}
	// TODO: make sure tasks don't have excessive timeouts? 24h?
	// TODO: record task timeout values so we can get a good idea of what people generally set it to.
	ctx, _ = context.WithTimeout(ctx, time.Duration(t)*time.Second)

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
	// TODO: Need a way to call down into docker, read the return error, compare it against Unrecoverable error and stop, otherwise continue, while still being able to add context to errors.
	cID, err := drv.createContainer(task)
	if err != nil {
		return "", fmt.Errorf("createContainer: %v", err)
	}

	startTimer := drv.NewTimer("docker", "start_container", 1.0)
	logrus.WithFields(logrus.Fields{"task_id": task.Id(), "container": cID}).Info("Starting container execution")
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
		return "", errors.New("no image specified")
	}

	var cmd []string
	if task.Command() != "" {
		// TODO We may have to move the sh part out to swapi tasker so it can decide between sh-ing .runtask directly vs using the -c form with a command.
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

func (drv *DockerDriver) pullImage(task drivers.ContainerTask) (*docker.Image, error) {
	configs := usableConfigs(task)
	if len(configs) == 0 {
		return nil, fmt.Errorf("No AuthConfigurations specified! Did not attempt pull. Bad Auther implementation?")
	}

	log := logrus.WithFields(logrus.Fields{"task_id": task.Id(), "image": task.Image()})

	repo, tag := normalizedImage(task.Image())
	repoImage := fmt.Sprintf("%s:%s", repo, tag)

	pullTimer := drv.NewTimer("docker", "pull_image", 1.0)
	defer pullTimer.Measure()
	// try all user creds until we get one that works
	for i, config := range configs {
		if allower, ok := task.(AllowImager); ok {
			err := allower.AllowImagePull(repoImage, config)
			if _, ok := err.(*ErrImageNotAllowed); ok {
				// if we could authenticate but the image is simply too big, tell the user
				return nil, err
			} else if err != nil {
				// Could be due to a login error, so we have to try them all :(
				log.WithFields(logrus.Fields{"config_index": i, "username": config.Username}).WithError(err).Error("Tried to verify size, continuing")
				continue
			}
		}

		err := drv.docker.PullImage(docker.PullImageOptions{Repository: repo, Tag: tag}, config)
		if err != nil {
			// Don't leak config into logs! Go lookup array in user's credentials if user complains.
			log.WithFields(logrus.Fields{"config_index": i, "username": config.Username}).WithError(err).Info("Tried to pull image")
			continue
		}
		return drv.docker.InspectImage(repoImage)
	}
	return nil, errors.New("no credentials could successfully pull image")
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
	log := logrus.WithFields(logrus.Fields{"image": image})
	// Tags are not part of the permission model.
	key, _ := normalizedImage(image)
	drv.authCacheLock.RLock()
	defer drv.authCacheLock.RUnlock()
	if cached, exists := drv.authCache[key]; exists {
		// For public images, the first, empty AuthConfiguration will be first in
		// both lists and match quickly. For others, the sets are likely to be
		// small.
		for configI, config := range configs {
			for knownConfigI, knownConfig := range cached {
				if config.Email == knownConfig.Email &&
					config.Username == knownConfig.Username &&
					config.Password == knownConfig.Password &&
					config.ServerAddress == knownConfig.ServerAddress {
					log.WithFields(logrus.Fields{"stored_set_index": knownConfigI, "task_set_index": configI}).Info("Cached credentials matched")
					return true
				}
			}
		}
		log.Info("No credentials matched.")
	} else {
		log.Info("Key does not exist")
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
	repo, tag := normalizedImage(task.Image())
	repoImage := fmt.Sprintf("%s:%s", repo, tag)

	imageInfo, err := drv.docker.InspectImage(repoImage)
	if err == docker.ErrNoSuchImage {
		// Attempt a pull with task's credentials, If credentials work, add them to the cached set (handled by pull).
		imageInfo, err = drv.pullImage(task)
	}
	if err != nil {
		return fmt.Errorf("error getting image %v: %v", repoImage, err)
	}

	if allower, ok := task.(AllowImager); ok {
		if err := allower.AllowImage(repoImage, imageInfo); err != nil {
			return fmt.Errorf("task not allowed to use image: %v", err)
		}
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
	repo, tag := normalizedImage(task.Image())

	// On authorization failure for the specific image, we try the next config,
	// on any other failure, we fail immediately.
	// This way things like being unable to connect to a registry due to some
	// weird reason (server format different etc.) pop up immediately and will
	// aide in debugging when users complain, instead of the error message being
	// lost in subsequent loops.
	for i, config := range configs {
		regClient = registryForConfig(config)
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
func registryForConfig(config docker.AuthConfiguration) *registry.Registry {
	addr := config.ServerAddress
	if strings.Contains(addr, "docker.io") || addr == "" {
		addr = "index.docker.io"
	}

	// Use this instead of registry.New to avoid the Ping().
	url := strings.TrimSuffix(fmt.Sprintf("https://%s", addr), "/")
	transport := registry.WrapTransport(http.DefaultTransport, url, config.Username, config.Password)
	reg := &registry.Registry{
		URL: url,
		Client: &http.Client{
			Transport: transport,
		},
		Logf: registry.Quiet,
	}
	return reg
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
			sentence <- drivers.StatusCancelled
			drv.cancel(container)
		}
	}
}

func (drv *DockerDriver) status(container string, exitCode int, sentence <-chan string) (string, error) {
	var status string
	var err error
	select {
	case status = <-sentence: // use this if timed out
	default:
		switch exitCode {
		case 0:
			status = drivers.StatusSuccess
		case 137:
			// Probably an OOM kill

			cinfo, inspectErr := drv.docker.InspectContainer(container)
			if inspectErr != nil {
				logrus.WithError(inspectErr).WithFields(logrus.Fields{"container": container}).Error("inspecting container to check for oom")
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
