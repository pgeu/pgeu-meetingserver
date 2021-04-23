package main

import (
	"encoding/json"
	"net/http"
	"runtime"
	"time"
)

type MemberStatus struct {
	UID    int    `json:"uid"`
	Name   string `json:"name"`
	Admin  bool   `json:"admin"`
	Remote string `json:"remote"`
}
type MeetingStatus struct {
	ID                  int            `json:"id"`
	State               string         `json:"state"`
	Members             []MemberStatus `json:"members"`
	DisconnectedMembers []MemberStatus `json:"disconnectedmembers"`
}
type Status struct {
	Upsince   time.Time `json:"upsince"`
	Timestamp time.Time `json:"timestamp"`
	Runtime   struct {
		Goroutines int    `json:"goroutines"`
		Cpus       int    `json:"cpus"`
		Goversion  string `json:"goversion"`
	} `json:"runtime"`
	Meetings []*MeetingStatus `json:"meetings"`
}

/* Time of server start, for status reports */
var startTime time.Time = time.Now()

func StatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/__meetingstatus" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	status := Status{
		Upsince:   startTime,
		Timestamp: time.Now(),
	}
	status.Runtime.Goroutines = runtime.NumGoroutine()
	status.Runtime.Cpus = runtime.NumCPU()
	status.Runtime.Goversion = runtime.Version()

	statchan := make(chan *MeetingStatus)
	for _, m := range _meetings {
		m.Statusquery <- statchan
		status.Meetings = append(status.Meetings, <-statchan)
	}
	if status.Meetings == nil {
		status.Meetings = make([]*MeetingStatus, 0)
	}

	j, err := json.Marshal(status)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Could not marshal json"))
		return
	}
	_, _ = w.Write(j)
}
