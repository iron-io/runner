## Runner Options

Options can be passed in via env vars, the following options are available:

* API_URL - REQUIRED - URL with which the tasker should communicate. Must be a full URL with scheme. Default: http://localhost:8080/
* DRIVER - Container driver to use. Default: docker.
* CONCURRENCY - Number of concurrent jobs to run on this machine. Default: the maximum amount based on MEMORY_PER_JOB and available memory. 
* MEMORY_PER_JOB - Maximum amount of memory a job will get. If you use this without CONCURRENCY, 
concurrency will automatically be set based on available memory. Default: 256MB. 
* LOG_LEVEL - Set logging level, for debugging. Default: info.
* LOG_TO - Set log destination. Default: stderr. format: [scheme://][host][:port][/path] -- possible schemes: { udp, tcp, file } -- examples:
* INSTANCE_ID - Optionally set the instance id instead of discovering cloud id or using hostname.
```
file:////mnt/logs
udp://logs.papertrailapp.com:port
stderr
```
* LOG_PREFIX - Set prefix for syslog logs. Default: hostname
