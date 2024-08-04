package worker

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/qiansuo1/cube/task"
)

func(a *Api)StartTaskHandler(w http.ResponseWriter,r *http.Request){
	d:=json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	te := task.TaskEvent{}
	err := d.Decode(&te)
	if err != nil{
		msg := fmt.Sprintf("Error unmarshalling body :%v\n",err)
		log.Print(msg)
		w.WriteHeader(http.StatusBadRequest)
		e := ErrResponse{
			HttpStatusCode: http.StatusBadRequest,
			Message: msg,
		}
		json.NewEncoder(w).Encode(e)
		return
	}
	a.Worker.AddTask(te.Task)
	log.Printf("Added task %v\n", te.Task.Id)
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(te.Task)
}

func (a *Api) GetTasksHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(a.Worker.GetTasks())
}

func (a *Api)StopTaskHandler(w http.ResponseWriter,r *http.Request){
	taskId := chi.URLParam(r,"task.Id")
	if taskId == "" {
		log.Printf("No taskID passed in request.\n")
		w.WriteHeader(http.StatusBadRequest)
	}
	tId,_ := uuid.Parse(taskId)
	taskToStop, err := a.Worker.Db.Get(tId.String())
	if err != nil {
		log.Printf("No task with ID %v found", tId)
		w.WriteHeader(404)
	}

	// we need to make a copy so we are not modifying the task in the datastore
	taskCopy := *taskToStop.(*task.Task)
	taskCopy.State = task.Completed
	a.Worker.AddTask(taskCopy)

	log.Printf("Added task %v to stop container %v\n", taskCopy.Id.String(), taskCopy.ContainerID)
	w.WriteHeader(204)

}

func (a *Api)GetStatsHandler(w http.ResponseWriter, r *http.Request){
	w.Header().Set("Content-Type","application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(a.Worker.Stats)
}