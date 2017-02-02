package resharding

import (
	"fmt"

	log "github.com/golang/glog"

	"github.com/youtube/vitess/go/vt/concurrency"

	workflowpb "github.com/youtube/vitess/go/vt/proto/workflow"
)

type level int

const (
	// SEQUENTIAL means that the tasks will run sequentially.
	SEQUENTIAL level = 1 + iota
	//PARALLEL means that the tasks will run in parallel.
	PARALLEL
)

// ParallelRunner is used to control executing tasks concurrently.
// Each phase has its own ParallelRunner object.
type ParallelRunner struct {
	// TODO(yipeiw) : ParallelRunner should have fields for per-task controllable actions.
}

// Run is the entry point for controling task executions.
// tasks should be a copy of tasks with the expected execution order, the status of task should be
// both updated in this copy and the original one (checkpointer.Update does this). This is to avoid
// data racing situation.
func (p *ParallelRunner) Run(tasks []*workflowpb.Task, executeFunc func(map[string]string) error, cp *CheckpointWriter, concurrencyLevel level) error {
	var parallelNum int // default value is 0. The task will not run in this case.
	switch concurrencyLevel {
	case SEQUENTIAL:
		parallelNum = 1
	case PARALLEL:
		parallelNum = len(tasks)
	default:
		panic(fmt.Sprintf("BUG: Invalid concurrency level: %v", concurrencyLevel))
	}

	// TODO(yipeiw): Support retry, restart, pause actions. Wrap the execution to interleave with actions.
	// sem is a channel used to control the level of concurrency.
	sem := make(chan bool, parallelNum)
	var ec concurrency.AllErrorRecorder
	for _, task := range tasks {
		// TODO(yipeiw): Add checking logics to support retry, pause, restart actions when lauching tasks.
		if task.State == workflowpb.TaskState_TaskDone {
			continue
		}

		sem <- true
		go func(t *workflowpb.Task) {
			defer func() { <-sem }()
			status := workflowpb.TaskState_TaskDone
			if err := executeFunc(t.Attributes); err != nil {
				status = workflowpb.TaskState_TaskNotStarted
				t.Error = err.Error()
				ec.RecordError(err)
			}

			t.State = status
			// Only log the error passage rather then propograting it through ErrorRecorder. The reason is that error message in
			// ErrorRecorder will leads to stop of the workflow, which is unexpected if only checkpointing fails.
			// However, the checkpointing failure right after initializing the tasks should lead to a stop of the workflow.
			if err := cp.UpdateTask(t.Id, status); err != nil {
				log.Errorf("%v", err)
			}
		}(task)
	}
	// Wait until all running jobs are done.
	for i := 0; i < parallelNum; i++ {
		sem <- true
	}
	return ec.Error()
}
