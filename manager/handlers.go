package manager

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/qiansuo1/cube/task"
)

func(a *Api)StartTaskHandler(w http.ResponseWriter,r *http.Request){
	d := json.NewDecoder(r.Body)
	te := task.TaskEvent{}
	err := d.Decode(&te)
	if err != nil{
		msg := fmt.Sprintf("Error unmarshalling body: %v\n", err)
		log.Print(msg)
		w.WriteHeader(http.StatusBadRequest)
		e := ErrResponse{
			HTTPStatusCode: http.StatusBadRequest,
			Message:        msg,
		}
		json.NewEncoder(w).Encode(e)
		return
	}
	a.Manager.AddTask(te)
	log.Printf("Added task %v\n", te.Task.Id)
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(te.Task)

}

func (a *Api) GetTasksHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(a.Manager.GetTasks())
}

func (a *Api) StopTaskHandler(w http.ResponseWriter, r *http.Request) {
	taskId := chi.URLParam(r, "taskId")
	if taskId == "" {
		log.Printf("No taskID passed in request.\n")
		w.WriteHeader(400)
	}

	tId, _ := uuid.Parse(taskId)
	taskToStop, err := a.Manager.TaskDb.Get(tId.String())
	if err != nil {
		log.Printf("No task with ID %v found", tId)
		w.WriteHeader(404)
		return
	}


	te := task.TaskEvent{
		Id:        uuid.New(),
		State:     task.Completed,
		Timestamp: time.Now(),
	}
	// we need to make a copy so we are not modifying the task in the datastore
	taskCopy := taskToStop.(*task.Task)
	taskCopy.State = task.Completed
	te.Task = *taskCopy
	a.Manager.AddTask(te)

	log.Printf("Added task event %v to stop task %v\n", te.Id, taskCopy.Id)
	w.WriteHeader(204)
}

func (a *Api) GetNodesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(a.Manager.WorkerNodes)
}