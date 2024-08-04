package manager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/docker/go-connections/nat"
	"github.com/golang-collections/collections/queue"
	"github.com/google/uuid"
	"github.com/qiansuo1/cube/node"
	"github.com/qiansuo1/cube/scheduler"
	"github.com/qiansuo1/cube/store"
	"github.com/qiansuo1/cube/task"
	"github.com/qiansuo1/cube/worker"
)

type Manager struct{
	Workers []string
	Pending queue.Queue
	TaskDb store.Store
	EventDb store.Store
	WorkerTaskMap map[string][]uuid.UUID
	TaskWorkerMap map[uuid.UUID]string
	LastWorker    int

	WorkerNodes   []*node.Node
	Scheduler     scheduler.Scheduler
}

func New(workers []string, schedulerType string,dbType string) *Manager {
	workerTaskMap := make(map[string][]uuid.UUID)
	taskWorkerMap := make(map[uuid.UUID]string)
	var nodes []*node.Node
    for worker := range workers {
        workerTaskMap[workers[worker]] = []uuid.UUID{}
        nAPI := fmt.Sprintf("http://%v", workers[worker])
        n := node.NewNode(workers[worker], nAPI, "worker")
        nodes = append(nodes, n)
    }
 
    var s scheduler.Scheduler
	switch schedulerType {
	case "greedy":
		s = &scheduler.Greedy{Name: "greedy"}
	case "roundrobin":
		s = &scheduler.RoundRobin{Name: "roundrobin"}
	default:
		s = &scheduler.Epvm{Name: "epvm"}
	}

	m := Manager{
		Pending:       *queue.New(),
		Workers:       workers,
		WorkerTaskMap: workerTaskMap,
		TaskWorkerMap: taskWorkerMap,
		WorkerNodes:   nodes,
		Scheduler:     s,
	}

 	var ts store.Store
	var es store.Store
	var err error
	switch dbType {
	case "memory":
		ts = store.NewInMemoryTaskStore()
		es = store.NewInMemoryTaskEventStore()
	case "persistent":
		ts, err = store.NewTaskStore("tasks.db", 0600, "tasks")
		if err != nil {
		log.Fatalf("unable to create task store: %v", err)
		}

		es, err = store.NewEventStore("events.db", 0600, "events")
			if err != nil {
		log.Fatalf("unable to create task event store: %v", err)
		}
	}
	m.TaskDb = ts
	m.EventDb = es
	return &m
}


func (m *Manager) SelectWorker(t task.Task) (*node.Node, error)  {
	candidates := m.Scheduler.SelectCandidateNodes(t, m.WorkerNodes)
	if candidates == nil {
		msg := fmt.Sprintf("No available candidates match resource request for task %v", t.Id)
		err := errors.New(msg)
		return nil, err
	}
	scores := m.Scheduler.Score(t, candidates)
	if scores == nil {
		return nil, fmt.Errorf("no scores returned to task %v", t)
	}
	selectedNode := m.Scheduler.Pick(scores, candidates)

	return selectedNode, nil
}

func (m *Manager) UpdateTasks() {
    for {
        log.Println("Checking for task updates from workers")
        m.updateTasks()
        log.Println("Task updates completed")
        log.Println("Sleeping for 15 seconds")
        time.Sleep(15 * time.Second)
    }
}
func (m *Manager) updateTasks() {
	for _, worker := range m.Workers {
		log.Printf("Checking worker %v for task updates\n", worker)
		url := fmt.Sprintf("http://%s/tasks", worker)
		resp, err := http.Get(url)
		if err != nil {
			log.Printf("Error connecting to %v: %v\n", worker, err)
		}

		if resp.StatusCode != http.StatusOK {
			log.Printf("Error sending request: %v\n", err)
		}

		d := json.NewDecoder(resp.Body)
		var tasks []*task.Task
		err = d.Decode(&tasks)
		if err != nil {
			log.Printf("Error unmarshalling tasks: %s\n", err.Error())
		}

		for _, t := range tasks {
			log.Printf("Attempting to update task %v\n", t.Id)

			result, err := m.TaskDb.Get(t.Id.String())
			if err != nil {
				log.Printf("Task with Id %s not found\n", t.Id)
				return
			}
			taskPersisted, ok := result.(*task.Task)
			if !ok {
				log.Printf("cannot convert result %v to task.Task type\n", result)
				continue
			}
			if taskPersisted.State != t.State {
				taskPersisted.State = t.State
			}
			taskPersisted.StartTime = t.StartTime
			taskPersisted.FinishTime = t.FinishTime
			taskPersisted.ContainerID = t.ContainerID
			taskPersisted.HostPorts = t.HostPorts
			m.TaskDb.Put(taskPersisted.Id.String(), taskPersisted)
		}

	}
}

func (m *Manager) SendWork() {
	if m.Pending.Len() > 0 {
		e := m.Pending.Dequeue()
		te := e.(task.TaskEvent)
		err := m.EventDb.Put(te.Id.String(), &te)
		if err != nil {
			log.Printf("error attempting to store task event %s: %s\n", te.Id.String(), err)
		}
		log.Printf("Pulled %v off pending queue", te)

		taskWorker, ok := m.TaskWorkerMap[te.Task.Id]
		if ok {
			result, err := m.TaskDb.Get(te.Task.Id.String())
			if err != nil {
				log.Printf("unable to schedule task: %s\n", err)
				return
			}

			persistedTask, ok := result.(*task.Task)
			if !ok {
				log.Printf("unable to convert task to task.Task type\n")
				return
			}

			if te.State == task.Completed && task.ValidStateTransition(persistedTask.State, te.State) {
				m.stopTask(taskWorker, te.Task.Id.String())
				return
			}

			log.Printf("invalid request: existing task %s is in state %v and cannot transition to the completed state\n", persistedTask.Id.String(), persistedTask.State)
			return
		}

		t := te.Task
		w, err := m.SelectWorker(t)
		if err != nil {
			log.Printf("error selecting worker for task %s: %v", t.Id, err)
			return
		}

		log.Printf("[manager] selected worker %s for task %s", w.Name, t.Id)

		m.WorkerTaskMap[w.Name] = append(m.WorkerTaskMap[w.Name], te.Task.Id)
		m.TaskWorkerMap[t.Id] = w.Name

		t.State = task.Scheduled
		m.TaskDb.Put(t.Id.String(), &t)

		data, err := json.Marshal(te)
		if err != nil {
			log.Printf("Unable to marshal task object: %v.", t)
		}

		url := fmt.Sprintf("http://%s/tasks", w.Name)
		resp, err := http.Post(url, "application/json", bytes.NewBuffer(data))
		if err != nil {
			log.Printf("[manager] Error connecting to %v: %v", w, err)
			m.Pending.Enqueue(t)
			return
		}

		d := json.NewDecoder(resp.Body)
		if resp.StatusCode != http.StatusCreated {
			e := worker.ErrResponse{}
			err := d.Decode(&e)
			if err != nil {
				fmt.Printf("Error decoding response: %s\n", err.Error())
				return
			}
			log.Printf("Response error (%d): %s", e.HttpStatusCode, e.Message)
			return
		}

		t = task.Task{}
		err = d.Decode(&t)
		if err != nil {
			fmt.Printf("Error decoding response: %s\n", err.Error())
			return
		}
		w.TaskCount++
		log.Printf("[manager] received response from worker: %#v\n", t)
	} else {
		log.Println("No work in the queue")
	}
	
}

func (m *Manager) AddTask(te task.TaskEvent) {
    m.Pending.Enqueue(te)
}

func (m *Manager) GetTasks() []*task.Task {
	taskList, err := m.TaskDb.List()
	if err != nil {
		log.Printf("error getting list of tasks: %v\n", err)
		return nil
	}

	return taskList.([]*task.Task)
}

func (m *Manager) ProcessTasks() {
	for {
		log.Println("Processing any tasks in the queue")
		m.SendWork()
		log.Println("Sleeping for 10 seconds")
		time.Sleep(10 * time.Second)
	}
}

func getHostPort(ports nat.PortMap) *string{
	for k := range ports {	
		return &ports[k][0].HostPort
	}
	return nil
}

func (m *Manager) checkTaskHealth(t task.Task) error{
	log.Printf("Calling health check for task %s: %s\n", t.Id, t.HealthCheck)
	w := m.TaskWorkerMap[t.Id]
	hostPort := getHostPort(t.HostPorts)
	if hostPort == nil {
		log.Printf("Have not collected task %s host port yet. Skipping.\n", t.Id)
		return nil
	}
	worker := strings.Split(w, ":")
	
	url := fmt.Sprintf("http://%s:%s%s", worker[0], *hostPort, t.HealthCheck)
	log.Printf("Calling health check for task %s: %s\n", t.Id, url)
	resp, err := http.Get(url)
	if err != nil {
		msg := fmt.Sprintf("Error connecting to health check %s", url)
		log.Println(msg)
		return errors.New(msg)
	}

	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("Error health check for task %s did not return 200\n", t.Id)
		log.Println(msg)
		return errors.New(msg)
	}

	log.Printf("Task %s health check response: %v\n", t.Id, resp.StatusCode)

	return nil
}

func (m *Manager) UpdateNodeStats() {
	for {
		for _, node := range m.WorkerNodes {
			log.Printf("Collecting stats for node %v", node.Name)
			_, err := node.GetStats()
			if err != nil {
				log.Printf("error updating node stats: %v", err)
			}
		}
		time.Sleep(15 * time.Second)
	}
}

func (m *Manager) DoHealthChecks() {
	for {
		log.Println("Performing task health check")
		m.doHealthChecks()
		log.Println("Task health checks completed")
		log.Println("Sleeping for 60 seconds")
		time.Sleep(60 * time.Second)
	}
}

func (m *Manager) doHealthChecks() {
	tasks := m.GetTasks()
	for _, t := range tasks {
		if t.State == task.Running && t.RestartCount < 3 {
			err := m.checkTaskHealth(*t)
			if err != nil {
				if t.RestartCount < 3 {
					m.restartTask(t)
				}
			}
		} else if t.State == task.Failed && t.RestartCount < 3 {
			m.restartTask(t)
		}
	}
}

func (m *Manager) restartTask(t *task.Task) {
	// Get the worker where the task was running
	w := m.TaskWorkerMap[t.Id]
	t.State = task.Scheduled
	t.RestartCount++
	// We need to overwrite the existing task to ensure it has
	// the current state
	m.TaskDb.Put(t.Id.String(), t)

	te := task.TaskEvent{
		Id:        uuid.New(),
		State:     task.Running,
		Timestamp: time.Now(),
		Task:      *t,
	}
	data, err := json.Marshal(te)
	if err != nil {
		log.Printf("Unable to marshal task object: %v.\n", t)
		return
	}

	url := fmt.Sprintf("http://%s/tasks", w)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		log.Printf("Error connecting to %v: %v\n", w, err)
		m.Pending.Enqueue(t)
		return
	}

	d := json.NewDecoder(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		e := worker.ErrResponse{}
		err := d.Decode(&e)
		if err != nil {
			fmt.Printf("Error decoding response: %s\n", err.Error())
			return
		}
		log.Printf("Response error (%d): %s\n", e.HttpStatusCode, e.Message)
		return
	}

	newTask := task.Task{}
	err = d.Decode(&newTask)
	if err != nil {
		fmt.Printf("Error decoding response: %s\n", err.Error())
		return
	}
	log.Printf("%#v\n", t)
}

func (m *Manager) stopTask(worker string, taskID string) {
	client := &http.Client{}
	url := fmt.Sprintf("http://%s/tasks/%s", worker, taskID)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		log.Printf("error creating request to delete task %s: %v", taskID, err)
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("error connecting to worker at %s: %v", url, err)
		return
	}

	if resp.StatusCode != 204 {
		log.Printf("Error sending request: %v", err)
		return
	}

	log.Printf("task %s has been scheduled to be stopped", taskID)
}

