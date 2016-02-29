package swagger

import (
    "strings"
    "fmt"
    "github.com/dghubble/sling"
)

type JobsApi struct {
    basePath  string
}

func NewJobsApi() *JobsApi{
    return &JobsApi {
        basePath:   "https://192.168.99.100:8080/v1",
    }
}

func NewJobsApiWithBasePath(basePath string) *JobsApi{
    return &JobsApi {
        basePath:   basePath,
    }
}

/**
 * Gets job by id
 * Gets a job by id.
 * @param Id Job id
 * @return JobWrapper
 */
//func (a JobsApi) JobIdGet (Id string) (JobWrapper, error) {
func (a JobsApi) JobIdGet (Id string) (JobWrapper, error) {

    _sling := sling.New().Get(a.basePath)

    // create path and map variables
    path := "/v1/job/{id}"
    path = strings.Replace(path, "{" + "id" + "}", fmt.Sprintf("%v", Id), -1)

    _sling = _sling.Path(path)

    // accept header
    accepts := []string { "application/json" }
    for key := range accepts {
        _sling = _sling.Set("Accept", accepts[key])
        break // only use the first Accept
    }



    response := new(JobWrapper)
    _, err := _sling.ReceiveSuccess(response)
    //fmt.Println("JobIdGet response: ", response, resp, err)
    return *response, err
}
/**
 * Update a job
 * Typically used to update status on error/completion. TODO: only allow &#39;status&#39; field.
 * @param Id Job id
 * @param Body Job data to post
 * @return JobWrapper
 */
//func (a JobsApi) JobIdPatch (Id string, Body JobWrapper) (JobWrapper, error) {
func (a JobsApi) JobIdPatch (Id string, Body JobWrapper) (JobWrapper, error) {

    _sling := sling.New().Patch(a.basePath)

    // create path and map variables
    path := "/v1/job/{id}"
    path = strings.Replace(path, "{" + "id" + "}", fmt.Sprintf("%v", Id), -1)

    _sling = _sling.Path(path)

    // accept header
    accepts := []string { "application/json" }
    for key := range accepts {
        _sling = _sling.Set("Accept", accepts[key])
        break // only use the first Accept
    }

// body params
    _sling = _sling.BodyJSON(Body)


    response := new(JobWrapper)
    _, err := _sling.ReceiveSuccess(response)
    //fmt.Println("JobIdPatch response: ", response, resp, err)
    return *response, err
}
/**
 * Cancel a job.
 * This will prevent a job from running. TODO: should we attempt to kill a running job?
 * @param Id Job id
 * @return JobWrapper
 */
//func (a JobsApi) JobIdCancelPost (Id string) (JobWrapper, error) {
func (a JobsApi) JobIdCancelPost (Id string) (JobWrapper, error) {

    _sling := sling.New().Post(a.basePath)

    // create path and map variables
    path := "/v1/job/{id}/cancel"
    path = strings.Replace(path, "{" + "id" + "}", fmt.Sprintf("%v", Id), -1)

    _sling = _sling.Path(path)

    // accept header
    accepts := []string { "application/json" }
    for key := range accepts {
        _sling = _sling.Set("Accept", accepts[key])
        break // only use the first Accept
    }



    response := new(JobWrapper)
    _, err := _sling.ReceiveSuccess(response)
    //fmt.Println("JobIdCancelPost response: ", response, resp, err)
    return *response, err
}
/**
 * Retry a job.
 * If a job fails, you can retry the job with the original payload.
 * @param Id Job id
 * @return JobWrapper
 */
//func (a JobsApi) JobIdRetryPost (Id string) (JobWrapper, error) {
func (a JobsApi) JobIdRetryPost (Id string) (JobWrapper, error) {

    _sling := sling.New().Post(a.basePath)

    // create path and map variables
    path := "/v1/job/{id}/retry"
    path = strings.Replace(path, "{" + "id" + "}", fmt.Sprintf("%v", Id), -1)

    _sling = _sling.Path(path)

    // accept header
    accepts := []string { "application/json" }
    for key := range accepts {
        _sling = _sling.Set("Accept", accepts[key])
        break // only use the first Accept
    }



    response := new(JobWrapper)
    _, err := _sling.ReceiveSuccess(response)
    //fmt.Println("JobIdRetryPost response: ", response, resp, err)
    return *response, err
}
/**
 * Get next job.
 * Gets the next job in the queue, ready for processing.
 * @return []JobArray
 */
//func (a JobsApi) JobsGet () ([]JobArray, error) {
func (a JobsApi) JobsGet () ([]JobArray, error) {

    _sling := sling.New().Get(a.basePath)

    // create path and map variables
    path := "/v1/jobs"

    _sling = _sling.Path(path)

    // accept header
    accepts := []string { "application/json" }
    for key := range accepts {
        _sling = _sling.Set("Accept", accepts[key])
        break // only use the first Accept
    }



    response := new([]JobArray)
    _, err := _sling.ReceiveSuccess(response)
    //fmt.Println("JobsGet response: ", response, resp, err)
    return *response, err
}
/**
 * Enqueue Job
 * Enqueues a job.
 * @param Body Array of jobs to post.
 * @return JobArray
 */
//func (a JobsApi) JobsPost (Body NewJobArray) (JobArray, error) {
func (a JobsApi) JobsPost (Body NewJobArray) (JobArray, error) {

    _sling := sling.New().Post(a.basePath)

    // create path and map variables
    path := "/v1/jobs"

    _sling = _sling.Path(path)

    // accept header
    accepts := []string { "application/json" }
    for key := range accepts {
        _sling = _sling.Set("Accept", accepts[key])
        break // only use the first Accept
    }

// body params
    _sling = _sling.BodyJSON(Body)


    response := new(JobArray)
    _, err := _sling.ReceiveSuccess(response)
    //fmt.Println("JobsPost response: ", response, resp, err)
    return *response, err
}
