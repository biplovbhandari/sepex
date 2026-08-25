package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/batch"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/labstack/gommon/log"
)

type AWSBatchController struct {
	client    *batch.Batch
	ecsClient *ecs.ECS
}

// GetJobDefImage returns the container image reference declared by one specific
// job definition revision.
//
// Only exact references are accepted, meaning `name:revision` or a full ARN. An
// unpinned reference is recognized as unpinned by the caller without any API
// call, so this never has to list revisions to work out which one is latest.
// That also keeps this on the DescribeJobDefinitions parameter that takes exact
// references, which the API does not allow to be combined with a status filter.
func (c *AWSBatchController) GetJobDefImage(jobDef string) (string, error) {

	resp, err := c.client.DescribeJobDefinitions(&batch.DescribeJobDefinitionsInput{
		JobDefinitions: []*string{aws.String(jobDef)},
	})
	if err != nil {
		return "", err
	}

	if len(resp.JobDefinitions) != 1 {
		return "", fmt.Errorf("did not get an exact match for job definition %s", jobDef)
	}

	jd := resp.JobDefinitions[0]

	// Batch rejects a deregistered definition at submission, so report it as
	// deregistered rather than letting the caller believe it is usable.
	if status := aws.StringValue(jd.Status); status != "ACTIVE" {
		return "", fmt.Errorf("job definition %s is %s, not ACTIVE", jobDef, strings.ToLower(status))
	}

	// Multi node and EKS job definitions carry nodeProperties or eksProperties
	// instead, and dereferencing ContainerProperties on those panics.
	if jd.ContainerProperties == nil {
		return "", fmt.Errorf("job definition %s has no container properties, only container based job definitions are supported", jobDef)
	}

	return aws.StringValue(jd.ContainerProperties.Image), nil
}

// Region and credentials are both resolved by the SDK: the region from
// AWS_REGION, and credentials from the default chain, being environment
// variables (including AWS_SESSION_TOKEN), the shared credentials file, or the
// ECS/EC2 instance role. This lets deployments authenticate with an IAM role
// instead of static keys.
func NewAWSBatchController() (*AWSBatchController, error) {
	sess, err := session.NewSession()
	if err != nil {
		return nil, err
	}

	return &AWSBatchController{batch.New(sess), ecs.New(sess)}, nil
}

// returns the job id and an error
func (c *AWSBatchController) JobCreate(ctx context.Context,
	jobDef, jobName, jobQueue string, commandOverride []string,
	envVars map[string]string) (string, error) {

	envs := make([]*batch.KeyValuePair, len(envVars))
	var i int
	for k, v := range envVars {
		envs[i] = &batch.KeyValuePair{Name: aws.String(k), Value: aws.String(v)}
		i++
	}

	overrides := &batch.ContainerOverrides{
		Command:     aws.StringSlice(commandOverride),
		Environment: envs,
	}

	input := &batch.SubmitJobInput{
		JobDefinition:      aws.String(jobDef),
		JobName:            aws.String(jobName),
		JobQueue:           aws.String(jobQueue),
		ContainerOverrides: overrides,
	}

	output, err := c.client.SubmitJobWithContext(ctx, input)
	if err != nil {
		return "", err
	}

	return aws.StringValue(output.JobId), nil
}

// Get current status of the job from Batch and formats it according to OGC Specs, also get LogStreamName
func (c *AWSBatchController) JobMonitor(batchID string) (string, string, error) {
	input := &batch.DescribeJobsInput{Jobs: aws.StringSlice([]string{batchID})}
	output, err := c.client.DescribeJobs(input)
	if err != nil {
		return "", "", err
	}
	if len(output.Jobs) == 0 {
		return "", "", fmt.Errorf("no such job: %s", batchID)
	}

	status := aws.StringValue(output.Jobs[0].Status)
	lsn := aws.StringValue(output.Jobs[0].Container.LogStreamName)

	switch status {
	case "FAILED":
		reason := aws.StringValue(output.Jobs[0].StatusReason)
		// Non-standard reason used here to facilitate ogc implementation
		if reason == "DISMISSED" {
			return reason, lsn, nil
		} else {
			return status, lsn, nil
		}
	case "SUBMITTED":
		return "ACCEPTED", lsn, nil
	case "PENDING":
		return "ACCEPTED", lsn, nil
	case "RUNNABLE":
		return "ACCEPTED", lsn, nil
	case "STARTING":
		return "RUNNING", lsn, nil
	case "RUNNING":
		return status, lsn, nil
	case "SUCCEEDED":
		return "SUCCESSFUL", lsn, nil

	default:
		return "", lsn, fmt.Errorf("unrecognized status  %s", status)
	}
}

// combines JobTerminate and JobCancel by managing calls for you based on job status
func (c *AWSBatchController) JobKill(jobID string) (string, error) {
	input := &batch.DescribeJobsInput{Jobs: aws.StringSlice([]string{jobID})}

	output, err := c.client.DescribeJobs(input)
	if err != nil {
		return "", err
	}

	if len(output.Jobs) == 0 {
		return "", fmt.Errorf("no such job: %s", jobID)
	}

	if len(output.Jobs) > 1 {
		return "", fmt.Errorf("more than one job found for %s", jobID)
	}

	status := aws.StringValue(output.Jobs[0].Status)
	switch status {
	case "SUBMITTED", "PENDING", "RUNNABLE":
		output, err := c.JobCancel(jobID, "DISMISSED")
		if err != nil {
			return "", err
		}
		return output, nil

	case "STARTING", "RUNNING":
		output, err := c.JobTerminate(jobID, "DISMISSED")
		if err != nil {
			return "", err
		}
		return output, nil

	case "FAILED", "SUCCEEDED":
		return "", nil
	}

	// Add some mechanism to clean up s3 if needed

	return "", fmt.Errorf("unknown status for job %s: %s", jobID, status)
}

// for jobs with the following statuses: "STARTING", "jobs.RUNNING"
func (c *AWSBatchController) JobTerminate(jobID, reason string) (string, error) {
	input := &batch.TerminateJobInput{
		JobId:  aws.String(jobID),
		Reason: aws.String(reason),
	}

	output, err := c.client.TerminateJob(input)
	if err != nil {
		return "", err
	}

	return output.String(), nil
}

// for jobs with the following statuses: "SUBMITTED", "PENDING", "RUNNABLE"
func (c *AWSBatchController) JobCancel(jobID, reason string) (string, error) {
	input := &batch.CancelJobInput{
		JobId:  aws.String(jobID),
		Reason: aws.String(reason),
	}

	output, err := c.client.CancelJob(input)
	if err != nil {
		return "", err
	}

	return output.String(), nil
}

// GetJobImage returns the image reference the job's container was started from,
// and the digest ECS actually pulled when that can be determined.
//
// The reference comes from the job itself rather than from its job definition,
// so it describes what this job ran even if the definition has since gained new
// revisions. An empty digest is not an error: it means the caller should record
// the image without one and say so.
func (c *AWSBatchController) GetJobImage(batchID string) (imageURI string, imageDigest string, err error) {

	input := &batch.DescribeJobsInput{Jobs: aws.StringSlice([]string{batchID})}
	output, err := c.client.DescribeJobs(input)
	if err != nil {
		return "", "", err
	}
	if len(output.Jobs) == 0 {
		return "", "", fmt.Errorf("no such job: %s", batchID)
	}

	container := output.Jobs[0].Container
	if container == nil {
		return "", "", fmt.Errorf("job %s has no container details", batchID)
	}

	imageURI = aws.StringValue(container.Image)

	// A digest pinned reference already answers the question. Batch has no way
	// to override a job definition's image, and a digest is content addressed,
	// so ECS could only ever confirm what the reference already says. Returning
	// here keeps a fully pinned deployment from needing ecs:DescribeTasks at
	// all, and keeps the recorded source the same with or without it.
	if strings.Contains(imageURI, "@sha256:") {
		return imageURI, "", nil
	}

	// Otherwise the reference is a mutable tag, and Batch reports it as it was
	// written rather than resolved, so ask ECS about the task that ran it. This
	// only works while the task is still describable, which is why it is
	// attempted while the job is running rather than after it has finished.
	taskARN := aws.StringValue(container.TaskArn)
	if taskARN == "" {
		// EKS backed compute environments have no ECS task.
		log.Debugf("job %s has no ECS task arn, so no digest can be observed", batchID)
		return imageURI, "", nil
	}

	cluster, ok := clusterFromTaskARN(taskARN)
	if !ok {
		log.Debugf("job %s has a task arn without a cluster segment (%s), so no digest can be observed", batchID, taskARN)
		return imageURI, "", nil
	}

	tasks, err := c.ecsClient.DescribeTasks(&ecs.DescribeTasksInput{
		Cluster: aws.String(cluster),
		Tasks:   []*string{aws.String(taskARN)},
	})
	if err != nil {
		// Most likely the deployment has not been granted ecs:DescribeTasks.
		// Losing the digest is not a reason to fail the job's metadata, but it
		// is the only place the reason is visible, since the caller just sees
		// an empty digest and falls back to a weaker source.
		log.Debugf("could not describe ECS task for job %s, so no digest can be observed: %s", batchID, err)
		return imageURI, "", nil
	}

	for _, task := range tasks.Tasks {
		for _, tc := range task.Containers {
			if digest := aws.StringValue(tc.ImageDigest); digest != "" {
				return imageURI, digest, nil
			}
		}
	}

	log.Debugf("ECS reported no image digest for job %s, so no digest can be observed", batchID)

	return imageURI, "", nil
}

// clusterFromTaskARN pulls the cluster name out of a long form ECS task ARN,
// which looks like arn:aws:ecs:<region>:<account>:task/<cluster>/<task-id>.
// Short form ARNs predate the cluster segment and report false.
func clusterFromTaskARN(taskARN string) (string, bool) {
	_, resource, found := strings.Cut(taskARN, ":task/")
	if !found {
		return "", false
	}

	parts := strings.Split(resource, "/")
	if len(parts) != 2 || parts[0] == "" {
		return "", false
	}

	return parts[0], true
}

// Get job execution times
func (c *AWSBatchController) GetJobTimes(batchID string) (cp time.Time, cr time.Time, st time.Time, err error) {

	describeJobsInput := &batch.DescribeJobsInput{
		Jobs: []*string{aws.String(batchID)},
	}

	describeJobsOutput, err := c.client.DescribeJobs(describeJobsInput)
	if err != nil {
		return time.Time{}, time.Time{}, time.Time{}, fmt.Errorf("error describing jobs: %s", err)
	}

	if len(describeJobsOutput.Jobs) > 0 {
		job := describeJobsOutput.Jobs[0] // Assuming only one job is returned

		// Extract createdAt, startedAt, and completedAt times
		if job.CreatedAt != nil && job.StartedAt != nil && job.StoppedAt != nil {
			cr = time.UnixMilli(*job.CreatedAt)
			st = time.UnixMilli(*job.StartedAt)
			cp = time.UnixMilli(*job.StoppedAt)
		} else {
			return time.Time{}, time.Time{}, time.Time{}, fmt.Errorf("one of the job time value is nil")
		}
	} else {
		return time.Time{}, time.Time{}, time.Time{}, fmt.Errorf("no job information found")
	}

	return cr, st, cp, nil
}
