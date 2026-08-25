package jobs

import (
	"time"
)

type StatusMessage struct {
	Job        *Job
	Status     string    `json:"status"`
	LastUpdate time.Time `json:"updated"`
}

type ResultsMessage struct {
	JobID   string      `json:"jobID"`
	Results interface{} `json:"outputs"`
}

type MessageQueue struct {
	StatusChan chan StatusMessage
	JobDone    chan Job
}

// imageProvenanceCapturer is implemented by job types whose image can only be
// identified while the workload is running. Docker jobs capture theirs inline
// and do not need this.
type imageProvenanceCapturer interface {
	ensureImageProvenance()
}

// Job should not be a docker job
// This function should not block the routine as it is being called by message queue
func ProcessStatusMessageUpdate(sm StatusMessage) {

	// Multiple calls should not trigger multiple close or metadata routines
	// A better way to achieve this would be through locks.
	switch (*sm.Job).CurrentStatus() {
	case SUCCESSFUL, DISMISSED, FAILED, LOST:
		return
	}
	(*sm.Job).NewStatusUpdate(sm.Status, sm.LastUpdate)

	switch sm.Status {
	case RUNNING:
		// The workload is up, so record what it is running while the evidence
		// is still reachable. Doing this after the job finishes is too late for
		// AWS Batch, whose ECS task stops being describable soon afterwards.
		if capturer, ok := (*sm.Job).(imageProvenanceCapturer); ok {
			go capturer.ensureImageProvenance()
		}

	case SUCCESSFUL:
		go (*sm.Job).WriteMetaData()
		fallthrough
	case DISMISSED, FAILED:
		// swap the order of following if results are posted/written by the container
		// also then no need to do all of this together in a separate routine
		// we can do RunFinished in this routine but Close will still need to be run in a new routine
		// so that the message queue is not hanged up
		go func() {
			(*sm.Job).Close()
			(*sm.Job).RunFinished()
		}()
	}
}
