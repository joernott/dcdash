// SPDX-License-Identifier: GPL-3.0-or-later

package proxmox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/joernott/dcdash/internal/audit"
)

// Job states and step states.
const (
	JobRunning = "running"
	JobDone    = "done"
	JobFailed  = "failed"

	StepPending = "pending"
	StepRunning = "running"
	StepDone    = "done"
	StepFailed  = "failed"
	StepSkipped = "skipped"
)

// Job limits.
const (
	maxJobsPerUser = 3
	maxJobs        = 20
	jobTimeout     = 30 * time.Minute
	jobRetention   = time.Hour
)

// JobStep is a step of a job: clone, configure, resize, create, dhcp, dns or
// start. Detail names what a dhcp or dns step creates.
type JobStep struct {
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	State  string `json:"state"`
	UPID   string `json:"upid,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Job creates a guest in several steps in the background (ADR 0004).
type Job struct {
	ID      string    `json:"id"`
	Cluster string    `json:"cluster"`
	Type    string    `json:"type"`
	VMID    int       `json:"vmid"`
	Node    string    `json:"node"`
	Name    string    `json:"name"`
	User    string    `json:"user"`
	State   string    `json:"state"`
	Error   string    `json:"error,omitempty"`
	Steps   []JobStep `json:"steps"`
	Created time.Time `json:"created"`
	Ended   time.Time `json:"ended,omitzero"`
}

// step is the work of a job step. It reports the ID of a Proxmox task it
// started with setUPID.
type step struct {
	name   string
	detail string
	run    func(ctx context.Context, setUPID func(string)) error
}

// taskStep is a step that starts a task (start returns its ID, or "" if the
// call was synchronous) and waits for it.
func taskStep(c *Cluster, name string, start func(ctx context.Context) (string, error)) step {
	return step{name: name, run: func(ctx context.Context, setUPID func(string)) error {
		upid, err := start(ctx)
		if err != nil || upid == "" {
			return err
		}
		setUPID(upid)
		return c.WaitTask(ctx, upid)
	}}
}

// jobStore keeps the jobs and the guest IDs reserved by running jobs.
type jobStore struct {
	mu       sync.Mutex
	jobs     map[string]*Job
	reserved map[string]map[int]bool // cluster -> vmid
	now      func() time.Time
	wg       sync.WaitGroup
}

func newJobStore() *jobStore {
	return &jobStore{jobs: map[string]*Job{}, reserved: map[string]map[int]bool{}, now: time.Now}
}

// errTooManyJobs means that the user or dcdash runs too many jobs.
var errTooManyJobs = errors.New("too many creations in progress")

// admit checks the job limits and prunes old jobs. Call with mu held.
func (s *jobStore) admit(user string) error {
	running, mine := 0, 0
	for id, j := range s.jobs {
		if j.State != JobRunning && s.now().Sub(j.Ended) > jobRetention {
			delete(s.jobs, id)
			continue
		}
		if j.State == JobRunning {
			running++
			if j.User == user {
				mine++
			}
		}
	}
	if running >= maxJobs || mine >= maxJobsPerUser {
		return errTooManyJobs
	}
	return nil
}

// isReserved reports whether a running job holds the ID.
func (s *jobStore) isReserved(cluster string, vmid int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reserved[cluster][vmid]
}

// reserve holds an ID for a job; it fails if the ID is held already.
func (s *jobStore) reserve(cluster string, vmid int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reserved[cluster] == nil {
		s.reserved[cluster] = map[int]bool{}
	}
	if s.reserved[cluster][vmid] {
		return false
	}
	s.reserved[cluster][vmid] = true
	return true
}

func (s *jobStore) release(cluster string, vmid int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.reserved[cluster], vmid)
}

// get returns a copy of a job.
func (s *jobStore) get(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return Job{}, false
	}
	c := *j
	c.Steps = append([]JobStep(nil), j.Steps...)
	return c, true
}

func newJobID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// start runs the steps of job j in the background. The ID j.VMID must be
// reserved; it is released when the job ends. entry is the audit entry,
// written with the result when the job ends.
func (s *jobStore) start(j *Job, steps []step, log *audit.Log, entry audit.Entry) (Job, error) {
	s.mu.Lock()
	if err := s.admit(j.User); err != nil {
		s.mu.Unlock()
		return Job{}, err
	}
	j.ID, j.State, j.Created = newJobID(), JobRunning, s.now()
	for _, st := range steps {
		j.Steps = append(j.Steps, JobStep{Name: st.name, Detail: st.detail, State: StepPending})
	}
	s.jobs[j.ID] = j
	s.mu.Unlock()
	snapshot, _ := s.get(j.ID)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.run(j, steps, log, entry)
	}()
	return snapshot, nil
}

// setStep changes a step under the lock.
func (s *jobStore) setStep(j *Job, i int, f func(*JobStep)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&j.Steps[i])
}

func (s *jobStore) run(j *Job, steps []step, log *audit.Log, entry audit.Entry) {
	ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
	defer cancel()
	defer s.release(j.Cluster, j.VMID)
	var failed error
	for i, st := range steps {
		if failed != nil {
			s.setStep(j, i, func(x *JobStep) { x.State = StepSkipped })
			continue
		}
		s.setStep(j, i, func(x *JobStep) { x.State = StepRunning })
		err := st.run(ctx, func(upid string) { s.setStep(j, i, func(x *JobStep) { x.UPID = upid }) })
		s.setStep(j, i, func(x *JobStep) {
			if err != nil {
				x.State, x.Error = StepFailed, err.Error()
			} else {
				x.State = StepDone
			}
		})
		failed = err
	}
	s.mu.Lock()
	j.Ended = s.now()
	if failed != nil {
		j.State, j.Error = JobFailed, failed.Error()
	} else {
		j.State = JobDone
	}
	result := audit.ResultSuccess
	if failed != nil {
		result = audit.ResultFailure
		entry.Details["error"] = failed.Error()
	}
	var tasks []string
	for _, st := range j.Steps {
		if st.UPID != "" {
			tasks = append(tasks, st.UPID)
		}
	}
	s.mu.Unlock()
	entry.Result = result
	entry.Details["tasks"] = tasks
	if log != nil {
		_ = log.Record(context.Background(), entry)
	}
}
