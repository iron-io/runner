package docker

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
	manifest "github.com/docker/distribution/manifest/schema1"
	"github.com/fsouza/go-dockerclient"
	"github.com/heroku/docker-registry-client/registry"
	"github.com/iron-io/runner/common"
	"github.com/iron-io/runner/common/stats"
	"github.com/iron-io/runner/drivers"
)

const hubURL = "https://registry.hub.docker.com"

var registryClient = &http.Client{
	Transport: &http.Transport{
		Dial: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 2 * time.Minute,
		}).Dial,
		TLSClientConfig: &tls.Config{
			ClientSessionCache: tls.NewLRUClientSessionCache(8192),
		},
		TLSHandshakeTimeout:   10 * time.Second,
		MaxIdleConnsPerHost:   32, // TODO tune; we will likely be making lots of requests to same place
		Proxy:                 http.ProxyFromEnvironment,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          512,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

// A drivers.ContainerTask should implement the Auther interface if it would
// like to use not-necessarily-public docker images for any or all task
// invocations.
type Auther interface {
	// DockerAuth should return docker auth credentials that will authenticate
	// against a docker registry for a given drivers.ContainerTask.Image(). An
	// error may be returned which will cause the task not to be run, this can be
	// useful for an implementer to do things like testing auth configurations
	// before returning them; e.g. if the implementer would like to impose
	// certain restrictions on images or if credentials must be acquired right
	// before runtime and there's an error doing so. If these credentials don't
	// work, the docker pull will fail and the task will be set to error status.
	DockerAuth() (docker.AuthConfiguration, error)
}

type runResult struct {
	error
	StatusValue string
}

func (r *runResult) Error() string {
	if r.error == nil {
		return ""
	}
	return r.error.Error()
}

func (r *runResult) Status() string    { return r.StatusValue }
func (r *runResult) UserVisible() bool { return common.IsUserVisibleError(r.error) }

type DockerDriver struct {
	conf     drivers.Config
	docker   dockerClient // retries on *docker.Client, restricts ad hoc *docker.Client usage / retries
	hostname string

	*common.Environment
}

// implements drivers.Driver
func NewDocker(env *common.Environment, conf drivers.Config) *DockerDriver {
	hostname, err := os.Hostname()
	if err != nil {
		logrus.WithError(err).Fatal("couldn't resolve hostname")
	}

	return &DockerDriver{
		conf:        conf,
		docker:      newClient(env),
		hostname:    hostname,
		Environment: env,
	}
}

// CheckRegistry will return a sizer, which can be used to check the size of an
// image if the returned error is nil. If the error returned is nil, then
// authentication against the given credentials was successful, if the
// configuration does not specify a config.ServerAddress,
// https://hub.docker.com will be tried.  CheckRegistry is a package level
// method since rkt can also use docker images, we may be interested in using
// rkt w/o a docker driver configured; also, we don't have to tote around a
// driver in any tasker that may be interested in registry information (2/2
// cases thus far).
func CheckRegistry(image string, config docker.AuthConfiguration) (Sizer, error) {
	registry, repo, tag := drivers.ParseImage(image)

	reg, err := registryForConfig(config, registry)
	if err != nil {
		return nil, err
	}

	mani, err := reg.Manifest(repo, tag)
	if err != nil {
		logrus.WithFields(logrus.Fields{"username": config.Username, "server": config.ServerAddress, "image": image}).WithError(err).Error("Credentials not authorized, trying next.")
		//if !isAuthError(err) {
		//  // TODO we might retry this, since if this was the registry that was supposed to
		//  // auth the task will erroneously be set to 'error'
		//}

		return nil, err
	}

	return &sizer{mani, reg, repo}, nil
}

// Sizer returns size information. This interface is liable to contain more
// than a size at some point, change as needed.
type Sizer interface {
	Size() (int64, error)
}

type sizer struct {
	mani *manifest.SignedManifest
	reg  *registry.Registry
	repo string
}

func (s *sizer) Size() (int64, error) {
	var sum int64
	for _, r := range s.mani.References() {
		desc, err := s.reg.LayerMetadata(s.repo, r.Digest)
		if err != nil {
			return 0, err
		}
		sum += desc.Size
	}
	return sum, nil
}

func registryURL(addr string) (string, error) {
	if addr == "" || strings.Contains(addr, "hub.docker.com") || strings.Contains(addr, "index.docker.io") {
		return hubURL, nil
	}

	url, err := url.Parse(addr)
	if err != nil {
		// TODO we could error the task out from this with a user error but since
		// we have a list of auths to check, just return the error so as to be
		// skipped... horrible api as it is
		logrus.WithFields(logrus.Fields{"auth_addr": addr}).WithError(err).Error("error parsing server address url, skipping")
		return "", err
	}

	if url.Scheme == "" {
		url.Scheme = "https"
	}
	url.Path = strings.TrimSuffix(url.Path, "/")
	url.Path = strings.TrimPrefix(url.Path, "/v2")
	url.Path = strings.TrimPrefix(url.Path, "/v1") // just try this, if it fails it fails, not supporting v1
	return url.String(), nil
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

func registryForConfig(config docker.AuthConfiguration, reg string) (*registry.Registry, error) {
	if reg == "" {
		reg = config.ServerAddress
	}

	var err error
	config.ServerAddress, err = registryURL(reg)
	if err != nil {
		return nil, err
	}

	// Use this instead of registry.New to avoid the Ping().
	transport := registry.WrapTransport(registryClient.Transport, reg, config.Username, config.Password)
	r := &registry.Registry{
		URL: config.ServerAddress,
		Client: &http.Client{
			Transport: transport,
		},
		Logf: registry.Quiet,
	}
	return r, nil
}

func (drv *DockerDriver) Prepare(ctx context.Context, task drivers.ContainerTask) (io.Closer, error) {
	var cmd []string
	if task.Command() != "" {
		// NOTE: this is hyper-sensitive and may not be correct like this even, but it passes old tests
		// task.Command() in swapi is always "sh /mnt/task/.runtask" so fields is safe
		cmd = strings.Fields(task.Command())
		logrus.WithFields(logrus.Fields{"task_id": task.Id(), "cmd": cmd, "len": len(cmd)}).Debug("docker command")
	}

	envvars := make([]string, 0, len(task.EnvVars()))
	for name, val := range task.EnvVars() {
		envvars = append(envvars, name+"="+val)
	}

	container := docker.CreateContainerOptions{
		Name: containerID(task),
		Config: &docker.Config{
			Env:         envvars,
			Cmd:         cmd,
			Memory:      int64(drv.conf.Memory),
			CPUShares:   drv.conf.CPUShares,
			Hostname:    drv.hostname,
			Image:       task.Image(),
			Volumes:     map[string]struct{}{},
			Labels:      task.Labels(),
			OpenStdin:   true,
			AttachStdin: true,
			StdinOnce:   true,
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

	err := drv.ensureImage(ctx, task)
	if err != nil {
		return nil, err
	}

	createTimer := drv.NewTimer("docker", "create_container", 1.0)
	_, err = drv.docker.CreateContainer(container)
	createTimer.Measure()
	if err != nil {
		// since we retry under the hood, if the container gets created and retry fails, we can just ignore error
		if err != docker.ErrContainerAlreadyExists {
			logrus.WithFields(logrus.Fields{"task_id": task.Id(), "command": container.Config.Cmd, "memory": container.Config.Memory,
				"cpu_shares": container.Config.CPUShares, "hostname": container.Config.Hostname, "name": container.Name,
				"image": container.Config.Image, "volumes": container.Config.Volumes, "binds": container.HostConfig.Binds,
			}).WithError(err).Error("Could not create container")
			// TODO basically no chance that creating a container failing is a user's fault, though it may be possible
			// via certain invalid args, tbd
			return nil, err
		}
	}

	// discard removal error
	return &closer{func() error { drv.removeContainer(containerID(task)); return nil }}, nil
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

func (drv *DockerDriver) ensureImage(ctx context.Context, task drivers.ContainerTask) error {
	reg, _, _ := drivers.ParseImage(task.Image())

	// ask for docker creds before looking for image, as the tasker may need to
	// validate creds even if the image is downloaded.

	var config docker.AuthConfiguration // default, tries docker hub w/o user/pass
	if task, ok := task.(Auther); ok {
		var err error
		config, err = task.DockerAuth()
		if err != nil {
			return err
		}
	}

	if reg != "" {
		config.ServerAddress = reg
	}

	// see if we already have it, if not, pull it
	_, err := drv.docker.InspectImage(task.Image())
	if err == docker.ErrNoSuchImage {
		err = drv.pullImage(ctx, task, config)
	}

	return err
}

func (drv *DockerDriver) pullImage(ctx context.Context, task drivers.ContainerTask, config docker.AuthConfiguration) error {
	log := common.Logger(ctx)

	reg, repo, tag := drivers.ParseImage(task.Image())
	globalRepo := path.Join(reg, repo)

	pullTimer := drv.NewTimer("docker", "pull_image", 1.0)
	defer pullTimer.Measure()

	drv.Inc("docker", "pull_image_count."+stats.AsStatField(task.Image()), 1, 1)

	if reg != "" {
		config.ServerAddress = reg
	}

	var err error
	config.ServerAddress, err = registryURL(config.ServerAddress)
	if err != nil {
		return err
	}

	log.WithFields(logrus.Fields{"registry": config.ServerAddress, "username": config.Username, "image": task.Image()}).Info("Pulling image")

	err = drv.docker.PullImage(docker.PullImageOptions{Repository: globalRepo, Tag: tag}, config)
	if err != nil {
		drv.Inc("task", "error.pull."+stats.AsStatField(task.Image()), 1, 1)
		log.WithFields(logrus.Fields{"registry": config.ServerAddress, "username": config.Username, "image": task.Image()}).WithError(err).Error("Failed to pull image")

		// TODO need to inspect for hub or network errors and pick.
		return common.UserError(fmt.Errorf("Failed to pull image '%s': %s", task.Image(), err))

		// TODO what about a case where credentials were good, then credentials
		// were invalidated -- do we need to keep the credential cache docker
		// driver side and after pull for this case alone?
	}

	return nil
}

// Run executes the docker container. If task runs, drivers.RunResult will be returned. If something fails outside the task (ie: Docker), it will return error.
// The docker driver will attempt to cast the task to a Auther. If that succeeds, private image support is available. See the Auther interface for how to implement this.
func (drv *DockerDriver) Run(ctx context.Context, task drivers.ContainerTask) (drivers.RunResult, error) {
	log := common.Logger(ctx)
	container := containerID(task)
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
	var complete bool
	defer func() { complete = true }() // run before cancel is called
	ctx = context.WithValue(ctx, completeKey, &complete)

	sentence := make(chan string, 1)
	go drv.nanny(ctx, container, task, sentence)
	go drv.collectStats(ctx, container, task)

	mwOut, mwErr := task.Logger()

	timer := drv.NewTimer("docker", "attach_container", 1)
	waiter, err := drv.docker.AttachToContainerNonBlocking(docker.AttachToContainerOptions{
		Container: container, OutputStream: mwOut, ErrorStream: mwErr,
		Stream: true, Logs: true, Stdout: true, Stderr: true,
		Stdin: true, InputStream: task.Input()})
	timer.Measure()
	if err != nil {
		return nil, err
	}

	_, err = drv.startTask(ctx, task)
	if err != nil {
		return nil, err
	}

	taskTimer := drv.NewTimer("docker", "container_runtime", 1)

	// can discard error, inspect will tell us about the task and wait will retry under the hood
	drv.docker.WaitContainer(ctx, container)
	taskTimer.Measure()

	waiter.Close()
	err = waiter.Wait()
	if err != nil {
		// TODO need to make sure this error isn't just a context error or something we can ignore
		log.WithError(err).Error("attach to container returned error, task may be missing logs")
	}

	// TODO need to ensure task is no longer running
	status, err := drv.status(ctx, container, sentence)
	return &runResult{
		StatusValue: status,
		error:       err,
	}, nil
}

const completeKey = "complete"

// watch for cancel or timeout and kill process.
func (drv *DockerDriver) nanny(ctx context.Context, container string, task drivers.ContainerTask, sentence chan<- string) {
	select {
	case <-ctx.Done():
		if *(ctx.Value(completeKey).(*bool)) {
			return
		}
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

func (drv *DockerDriver) cancel(container string) {
	stopTimer := drv.NewTimer("docker", "stop_container", 1.0)
	err := drv.docker.StopContainer(container, 30)
	stopTimer.Measure()
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{"container": container, "errType": fmt.Sprintf("%T", err)}).Error("something managed to escape our retries web, could not kill container")
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

		// the Stats implementation has a bug where it launches multiple goroutines
		// that send stats out over a pipe. When the Done channel is closed, the
		// read end of the pipe is closed immediately. The Stats() code path may
		// still read from the pipe, or the HTTP stream may still write to the
		// pipe. Either of these will result in a io.ErrClosedPipe. Also see
		// https://github.com/fsouza/go-dockerclient/issues/423.
		if err != nil && err != io.ErrClosedPipe {
			logrus.WithError(err).WithFields(logrus.Fields{"container": container, "task_id": task.Id()}).Error("error streaming docker stats for task")
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
	log := common.Logger(ctx)
	cID := containerID(task)

	startTimer := drv.NewTimer("docker", "start_container", 1.0)
	log.WithFields(logrus.Fields{"container": cID}).Debug("Starting container execution")
	err = drv.docker.StartContainer(cID, nil)
	startTimer.Measure()
	if err != nil {
		dockerErr, ok := err.(*docker.Error)
		_, containerAlreadyRunning := err.(*docker.ContainerAlreadyRunning)
		if containerAlreadyRunning || (ok && dockerErr.Status == 304) {
			// 304=container already started -- so we can ignore error
		} else {
			return "", err
		}
	}
	return cID, nil
}

func (drv *DockerDriver) status(ctx context.Context, container string, sentence <-chan string) (status string, err error) {
	log := common.Logger(ctx)

	select {
	case status := <-sentence: // use this if timed out / canceled
		return status, nil
	default:
	}

	cinfo, err := drv.docker.InspectContainer(container)
	if err != nil {
		// this is pretty sad, but better to say we had an error than to not.
		// task has run to completion and logs will be uploaded, user can decide
		log.WithFields(logrus.Fields{"container": container}).WithError(err).Error("Inspecting container")
		return drivers.StatusError, err
	}
	exitCode := cinfo.State.ExitCode
	if cinfo.State.Running {
		log.Warn("getting status of task that is still running, need to fix this")
		return "", nil
	}

	switch exitCode {
	default:
		// TODO exit code error masks log output if made user friendly, but if there is no log output then message will be left blank (is it ok?)
		return drivers.StatusError, fmt.Errorf("exit code %d", exitCode)
	case 0:
		return drivers.StatusSuccess, nil
	case 137: // OOM
		drv.Inc("docker", "oom", 1, 1)
		if !cinfo.State.OOMKilled {
			// It is possible that the host itself is running out of memory and
			// the host kernel killed one of the container processes.
			// See: https://github.com/docker/docker/issues/15621
			// TODO reed: isn't an OOM an OOM? this is wasting space imo
			log.WithFields(logrus.Fields{"container": container}).Info("Setting task as OOM killed, but docker disagreed.")
			drv.Inc("docker", "possible_oom_false_alarm", 1, 1.0)
		}

		return drivers.StatusKilled, drivers.ErrOutOfMemory
	}
}
