package docker

import (
	"fmt"
	"io"
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
	//   a UserVisible error
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

// Certain Docker errors are unrecoverable in the sense that the daemon is
// having problems and this driver can no longer continue.
type dockerError struct {
	error
}

func (d *dockerError) Unrecoverable() bool {
	// Allow nests so we don't have to reason about the error tree.
	if sub, ok := d.error.(*dockerError); ok {
		return sub.Unrecoverable()
	}

	// We don't have anything unrecoverable yet.
	return false
}

func (d *dockerError) UserVisible() bool { return agent.IsUserVisibleError(d.error) }
func (d *dockerError) UserError() error  { return d.error.(agent.UserVisibleError).UserError() }

type runResult struct {
	Err         error
	StatusValue string
}

func (runResult *runResult) Error() error   { return runResult.Err }
func (runResult *runResult) Status() string { return runResult.StatusValue }

type DockerDriver struct {
	conf     drivers.Config
	docker   dockerClient // retries on *docker.Client, restricts ad hoc *docker.Client usage / retries
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

func NewDocker(env *titancommon.Environment, conf drivers.Config) *DockerDriver {
	hostname, err := os.Hostname()
	if err != nil {
		logrus.WithError(err).Fatal("couldn't resolve hostname")
	}

	return &DockerDriver{
		conf:        conf,
		docker:      newClient(),
		hostname:    hostname,
		authCache:   make(map[string][]docker.AuthConfiguration),
		Environment: env,
	}
}

// Run executes the docker container. If task runs, drivers.RunResult will be returned. If something fails outside the task (ie: Docker), it will return error.
// The docker driver will attempt to cast the task to a Auther. If that succeeds, private image support is available. See the Auther interface for how to implement this.
func (drv *DockerDriver) Run(ctx context.Context, task drivers.ContainerTask) (drivers.RunResult, error) {
	log := titancommon.Logger(ctx)
	container, err := drv.startTask(ctx, task)
	if err != nil {
		return nil, err
	}

	taskTimer := drv.NewTimer("docker", "container_runtime", 1)

	t := drv.conf.DefaultTimeout
	if n := task.Timeout(); n != 0 {
		t = n
	}
	if t == 0 {
		t = 3600 // TODO we really should panic, or reconsider how this gets here
		log.Warn("Task timeout or runner configuration was set to zero, using default")
	}
	// TODO: make sure tasks don't have excessive timeouts? 24h?
	// TODO: record task timeout values so we can get a good idea of what people generally set it to.
	ctx, cancel := context.WithTimeout(ctx, time.Duration(t)*time.Second)
	defer cancel() // do this so that after Run exits, nanny and collect stop

	sentence := make(chan string, 1)
	go drv.nanny(ctx, container, task, sentence)
	go drv.collectStats(ctx, container, task)

	mwOut, mwErr := task.Logger()

	// Docker sometimes fails to close the attach response connection even after
	// the container stops, leaving the runner stuck. We use a non-blocking
	// attach so we can sleep a bit after WaitContainer returns and then forcibly
	// close the connection.
	timer := drv.NewTimer("docker", "attach_container", 1)
	closer, err := drv.docker.AttachToContainerNonBlocking(docker.AttachToContainerOptions{
		Container: container, OutputStream: mwOut, ErrorStream: mwErr,
		Stream: true, Logs: true, Stdout: true, Stderr: true})
	defer closer.Close()
	if err != nil {
		return nil, &dockerError{err}
	}
	timer.Measure()

	// It's possible the execution could be finished here, then what? http://docs.docker.com.s3-website-us-east-1.amazonaws.com/engine/reference/api/docker_remote_api_v1.20/#wait-a-container
	exitCode, err := drv.docker.WaitContainer(container)
	taskTimer.Measure()
	time.Sleep(10 * time.Millisecond)
	if err != nil {
		return nil, &dockerError{err}
	}

	status, err := drv.status(ctx, container, exitCode, sentence)

	// the err returned above is an error from running user code, so we don't return it from this method.
	r := &runResult{
		StatusValue: status,
		Err:         err,
	}
	return r, nil
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

func (drv *DockerDriver) collectStats(ctx context.Context, container string, task drivers.ContainerTask) {
	done := make(chan bool)
	defer close(done)
	dstats := make(chan *docker.Stats, 1)
	go func() {
		// NOTE: docker automatically streams every 1s. we can skip or avg samples if we'd like but
		// the memory overhead is < 1MB for 3600 stat points so this seems fine, seems better to stream
		// (internal docker api streams) than open/close stream for 1 sample over and over.
		// must be called in goroutine, docker.Stats() blocks
		err := drv.docker.Stats(docker.StatsOptions{
			ID:     container,
			Stats:  dstats,
			Stream: true,
			Done:   done, // A flag that enables stopping the stats operation
		})
		if err != nil {
			select {
			case <-ctx.Done():
				// Container is stopped, errors are spurious.
			default:
				logrus.WithError(err).WithFields(logrus.Fields{"container": container, "task_id": task.Id()}).Error("error streaming docker stats for task")
			}
			return
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case ds, ok := <-dstats:
			if !ok {
				return
			}
			task.WriteStat(cherryPick(ds))
		}
	}
}

func cherryPick(ds *docker.Stats) drivers.Stat {
	// TODO cpu % is as a % of the whole system... cpu is weird since we're sharing it
	// across a bunch of containers and it scales based on how many we're sharing with,
	// do we want users to see as a % of system?
	systemDelta := float64(ds.CPUStats.SystemCPUUsage - ds.PreCPUStats.SystemCPUUsage)
	cores := float64(len(ds.CPUStats.CPUUsage.PercpuUsage))
	var cpuUser, cpuKernel, cpuTotal float64
	if systemDelta > 0 {
		// TODO we could leave these in docker format and let hud/viz tools do this instead of us... like net is, could do same for mem, too. thoughts?
		cpuUser = (float64(ds.CPUStats.CPUUsage.UsageInUsermode-ds.PreCPUStats.CPUUsage.UsageInUsermode) / systemDelta) * cores * 100.0
		cpuKernel = (float64(ds.CPUStats.CPUUsage.UsageInKernelmode-ds.PreCPUStats.CPUUsage.UsageInKernelmode) / systemDelta) * cores * 100.0
		cpuTotal = (float64(ds.CPUStats.CPUUsage.TotalUsage-ds.PreCPUStats.CPUUsage.TotalUsage) / systemDelta) * cores * 100.0
	}
	return drivers.Stat{
		Timestamp: ds.Read,
		Metrics: map[string]uint64{
			// source: https://godoc.org/github.com/fsouza/go-dockerclient#Stats
			// ex (for future expansion): {"read":"2016-08-03T18:08:05Z","pids_stats":{},"network":{},"networks":{"eth0":{"rx_bytes":508,"tx_packets":6,"rx_packets":6,"tx_bytes":508}},"memory_stats":{"stats":{"cache":16384,"pgpgout":281,"rss":8826880,"pgpgin":2440,"total_rss":8826880,"hierarchical_memory_limit":536870912,"total_pgfault":3809,"active_anon":8843264,"total_active_anon":8843264,"total_pgpgout":281,"total_cache":16384,"pgfault":3809,"total_pgpgin":2440},"max_usage":8953856,"usage":8953856,"limit":536870912},"blkio_stats":{"io_service_bytes_recursive":[{"major":202,"op":"Read"},{"major":202,"op":"Write"},{"major":202,"op":"Sync"},{"major":202,"op":"Async"},{"major":202,"op":"Total"}],"io_serviced_recursive":[{"major":202,"op":"Read"},{"major":202,"op":"Write"},{"major":202,"op":"Sync"},{"major":202,"op":"Async"},{"major":202,"op":"Total"}]},"cpu_stats":{"cpu_usage":{"percpu_usage":[47641874],"usage_in_usermode":30000000,"total_usage":47641874},"system_cpu_usage":8880800500000000,"throttling_data":{}},"precpu_stats":{"cpu_usage":{"percpu_usage":[44946186],"usage_in_usermode":30000000,"total_usage":44946186},"system_cpu_usage":8880799510000000,"throttling_data":{}}}
			// TODO could prefix these with net_ or make map[string]map[string]uint64 to group... thoughts?

			// net
			"rx_dropped": ds.Network.RxDropped,
			"rx_bytes":   ds.Network.RxBytes,
			"rx_errors":  ds.Network.RxErrors,
			"tx_packets": ds.Network.TxPackets,
			"tx_dropped": ds.Network.TxDropped,
			"rx_packets": ds.Network.RxPackets,
			"tx_errors":  ds.Network.TxErrors,
			"tx_bytes":   ds.Network.TxBytes,
			// mem
			"mem_limit": ds.MemoryStats.Limit,
			"mem_usage": ds.MemoryStats.Usage,
			// i/o
			// TODO weird format in lib... probably ok to omit disk stats for a while, memory is main one
			// cpu
			"cpu_user":   uint64(cpuUser),
			"cpu_total":  uint64(cpuTotal),
			"cpu_kernel": uint64(cpuKernel),
			// TODO probably don't show cpu throttling? ;)
		},
	}
}

func containerID(task drivers.ContainerTask) string { return "task-" + task.Id() }

func (drv *DockerDriver) startTask(ctx context.Context, task drivers.ContainerTask) (dockerId string, err error) {
	log := titancommon.Logger(ctx)
	cID := containerID(task)

	startTimer := drv.NewTimer("docker", "start_container", 1.0)
	log.WithFields(logrus.Fields{"container": cID}).Debug("Starting container execution")
	err = drv.docker.StartContainer(cID, nil)
	startTimer.Measure()
	if err != nil {
		// Remove the created container since we couldn't start it.
		drv.Inc("docker", "container_start_error", 1, 1.0)
		return "", &dockerError{err}
	}
	return cID, nil
}

func (drv *DockerDriver) Prepare(ctx context.Context, task drivers.ContainerTask) (io.Closer, error) {
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
		Name: containerID(task),
		Config: &docker.Config{
			Env:       envvars,
			Cmd:       cmd,
			Memory:    int64(drv.conf.Memory),
			CPUShares: drv.conf.CPUShares,
			Hostname:  drv.hostname,
			Image:     task.Image(),
			Volumes:   map[string]struct{}{},
			Labels:    task.Labels(),
		},
		HostConfig: &docker.HostConfig{},
	}

	volumes := task.Volumes()
	for _, mapping := range volumes {
		hostDir := mapping[0]
		containerDir := mapping[1]
		container.Config.Volumes[containerDir] = struct{}{}
		mapn := fmt.Sprintf("%s:%s", hostDir, containerDir)
		container.HostConfig.Binds = append(container.HostConfig.Binds, mapn)
		logrus.WithFields(logrus.Fields{"volumes": mapn, "task_id": task.Id()}).Debug("setting volumes")
	}

	if wd := task.WorkDir(); wd != "" {
		logrus.WithFields(logrus.Fields{"wd": wd, "task_id": task.Id()}).Debug("setting work dir")
		container.Config.WorkingDir = wd
	}

	err := drv.EnsureUsableImage(ctx, task)
	if err != nil {
		return nil, err
	}

	createTimer := drv.NewTimer("docker", "create_container", 1.0)
	c, err := drv.docker.CreateContainer(container)
	createTimer.Measure()
	if err != nil {
		logrus.WithFields(logrus.Fields{"task_id": task.Id(), "command": container.Config.Cmd, "memory": container.Config.Memory,
			"cpu_shares": container.Config.CPUShares, "hostname": container.Config.Hostname, "name": container.Name,
			"image": container.Config.Image, "volumes": container.Config.Volumes, "binds": container.HostConfig.Binds,
		}).WithError(err).Error("Could not create container")
		drv.Inc("docker", "container_create_error", 1, 1.0)
		// TODO basically no chance that creating a container failing is a user's fault, though it may be possible
		// via certain invalid args, tbd
		return nil, &dockerError{err}
	}

	// discard removal error
	return &closer{func() error { drv.removeContainer(c.ID); return nil }}, nil
}

type closer struct {
	close func() error
}

func (c *closer) Close() error { return c.close() }

func (drv *DockerDriver) removeContainer(container string) error {
	removeTimer := drv.NewTimer("docker", "remove_container", 1.0)
	defer removeTimer.Measure()
	err := drv.docker.RemoveContainer(docker.RemoveContainerOptions{
		ID: container, Force: true, RemoveVolumes: true})

	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{"container": container}).Error("error removing container")
	}
	return nil
}

func (drv *DockerDriver) pullImage(ctx context.Context, task drivers.ContainerTask) (*docker.Image, error) {
	configs := usableConfigs(task)
	if len(configs) == 0 {
		return nil, fmt.Errorf("No AuthConfigurations specified! Did not attempt pull. Bad Auther implementation?")
	}

	log := titancommon.Logger(ctx)

	repo, tag := normalizedImage(task.Image())
	repoImage := fmt.Sprintf("%s:%s", repo, tag)

	pullTimer := drv.NewTimer("docker", "pull_image", 1.0)
	defer pullTimer.Measure()
	// try all user creds until we get one that works
	for i, config := range configs {
		if allower, ok := task.(AllowImager); ok {
			err := allower.AllowImagePull(repoImage, config)
			if agent.IsUserVisibleError(err) {
				// if we could authenticate but the image is simply too big, tell the user
				return nil, err
			} else if err != nil {
				// Could be due to a login error, so we have to try them all :(
				log.WithFields(logrus.Fields{"config_index": i, "username": config.Username}).WithError(err).Error("Tried to verify size, continuing")
				continue
			}
		}

		err := drv.docker.PullImage(docker.PullImageOptions{Repository: repo, Tag: tag}, config)
		if err == docker.ErrConnectionRefused {
			// If we couldn't connect to Docker, bail immediately.
			return nil, &dockerError{err}
		} else if err != nil {
			// Don't leak config into logs! Go lookup array in user's credentials if user complains.
			log.WithFields(logrus.Fields{"config_index": i, "username": config.Username}).WithError(err).Info("Tried to pull image")
			continue
		}
		return drv.docker.InspectImage(repoImage)
	}

	// It is possible that errors other than ErrConnectionRefused or "image not
	// found" (also means auth failed) errors can occur. These should not be bubbled up.
	err := fmt.Errorf("Image '%s' does not exist or authentication failed.", repo)
	return nil, agent.UserError(err, err)
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
func (drv *DockerDriver) allowedToUseImage(ctx context.Context, image string, configs []docker.AuthConfiguration) bool {
	log := titancommon.Logger(ctx)
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

func (drv *DockerDriver) EnsureUsableImage(ctx context.Context, task drivers.ContainerTask) error {
	repo, tag := normalizedImage(task.Image())
	repoImage := fmt.Sprintf("%s:%s", repo, tag)

	imageInfo, err := drv.docker.InspectImage(repoImage)
	if err == docker.ErrNoSuchImage {
		// Attempt a pull with task's credentials, If credentials work, add them to the cached set (handled by pull).
		imageInfo, err = drv.pullImage(ctx, task)
	}

	if err != nil {
		return &dockerError{err}
	}

	if allower, ok := task.(AllowImager); ok {
		if err := allower.AllowImage(repoImage, imageInfo); err != nil {
			return err
		}
	}

	// Image is available locally. If the credentials presented by the tasks are
	// known to be good for this image, allow it, otherwise check with registry.
	configs := usableConfigs(task)
	if drv.allowedToUseImage(ctx, repoImage, configs) {
		return nil
	}

	return drv.checkAgainstRegistry(ctx, task, configs)
}

func (drv *DockerDriver) checkAgainstRegistry(ctx context.Context, task drivers.ContainerTask, configs []docker.AuthConfiguration) error {
	log := titancommon.Logger(ctx)

	// We need to be able to support multiple registries, so just reconnect
	// each time (hope to not do this often actually, as this is mostly to
	// prevent abuse).
	// It may be worth optimising in the future to have a dedicated HTTP client for Docker Hub.
	drv.Inc("docker", "hit_registry_for_auth_check", 1, 1.0)
	log.Info("Hitting registry to check image access permission.")

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

			log.WithFields(logrus.Fields{"config_index": i}).WithError(err).Error("Error retrieving manifest")
			break
		}

		drv.acceptedCredentials(task.Image(), config)
		return nil
	}

	err = fmt.Errorf("Image '%s' does not exist or authentication failed.", repo)
	return agent.UserError(err, err)
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

func (drv *DockerDriver) status(ctx context.Context, container string, exitCode int, sentence <-chan string) (string, error) {
	var (
		status string
		err    error

		log = titancommon.Logger(ctx)
	)
	select {
	case status = <-sentence: // use this if timed out / canceled
		return status, nil
	default:
	}
	switch exitCode {
	default:
		status = drivers.StatusError
		err = fmt.Errorf("exit code %d", exitCode)
	case 0:
		status = drivers.StatusSuccess
	case 137: // OOM
		cinfo, inspectErr := drv.docker.InspectContainer(container)
		if inspectErr != nil {
			drv.Inc("docker", "possible_oom_inspect_container_error", 1, 1.0)

			d := &dockerError{inspectErr}
			log.WithFields(logrus.Fields{"container": container}).WithError(d).Error("Inspecting container for OOM check")
		} else {
			if !cinfo.State.OOMKilled {
				// It is possible that the host itself is running out of memory and
				// the host kernel killed one of the container processes.
				// See: https://github.com/docker/docker/issues/15621
				log.WithFields(logrus.Fields{"container": container}).Info("Setting task as OOM killed, but docker disagreed.")
				drv.Inc("docker", "possible_oom_false_alarm", 1, 1.0)
			}
		}

		status = drivers.StatusKilled
		err = drivers.ErrOutOfMemory
	}
	return status, err
}

func (drv *DockerDriver) cancel(container string) {
	for {
		stopTimer := drv.NewTimer("docker", "stop_container", 1.0)
		err := drv.docker.StopContainer(container, 30)
		stopTimer.Measure()
		_, notRunning := err.(*docker.ContainerNotRunning)
		if err == nil || notRunning {
			// 204=ok, 304=stopped already, 404=no container -> OK
			// TODO if docker dies this might run forever but we kind of want it to?
			break
		}
		time.Sleep(1 * time.Second) // TODO plumb clock?
		logrus.WithError(err).WithFields(logrus.Fields{"container": container}).Error("could not kill container, retrying indefinitely")
	}
}
