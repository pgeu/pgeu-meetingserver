package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"sync"
	"syscall"

	"github.com/gorilla/websocket"
)

var config = struct {
	verifyOrigin string
	dbURL        string
	behindproxy  bool
}{}

var (
	_meetings           = make(map[int]*Meeting)
	_meetingsMutex      sync.RWMutex
	_meetingRemoverChan = make(chan int, 10)
)

func EnsureAndGetMeeting(meetingid int) *Meeting {
	_meetingsMutex.RLock()
	/* Cannot use defer on the unlock since we have to unlock/relock later */

	meeting, ok := _meetings[meetingid]

	if ok {
		_meetingsMutex.RUnlock()
		return meeting
	}

	meeting = NewMeeting(meetingid)
	if meeting == nil {
		_meetingsMutex.RUnlock()
		return nil
	}

	/*
	* Before we can modifyt he meeting struct, we need to lock it for writes.
	* and once we've done that, we also need to make sure nobody else stuck a
	* new meeting in wile we were waiting for a lock.
	 */
	_meetingsMutex.RUnlock()
	_meetingsMutex.Lock()
	defer _meetingsMutex.Unlock()
	_, ok = _meetings[meetingid]
	if ok {
		return _meetings[meetingid]
	}

	/*
	* Nobody put anything in while we were running, so put our meeting
	* in the array and start the goroutine for it
	 */
	_meetings[meetingid] = meeting
	go meeting.Run()
	log.Printf("Started meeting %d", meetingid)
	return meeting
}
func RemoveMeeting(id int) {
	/*
	* Delete a meeting from the list. If it doesn't exist, it just means
	* it's already been deleted, so ignore that.
	 */
	_meetingsMutex.Lock()
	defer _meetingsMutex.Unlock()

	if _, ok := _meetings[id]; ok {
		log.Printf("Removing meeting %d", id)
		delete(_meetings, id)
	}
}
func MeetingRemover() {
	for {
		id := <-_meetingRemoverChan
		RemoveMeeting(id)
	}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header["Origin"]
		if len(origin) == 0 {
			log.Printf("Allowing connection with empty origin")
			return true
		}
		if config.verifyOrigin == "*" {
			log.Printf("Allowing origin %s, all origins are allowed", origin[0])
			return true
		}
		return (config.verifyOrigin == origin[0])
	},
}

var wsURLPattern = regexp.MustCompile(`^/ws/meeting/(\d+)/([A-Za-z0-9_-]{54})/(\d+)`)

func wsHandler(w http.ResponseWriter, r *http.Request) {
	match := wsURLPattern.FindStringSubmatch(r.URL.Path)
	if len(match) == 0 {
		http.NotFound(w, r)
		return
	}

	meetingid, err := strconv.Atoi(match[1])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	token := match[2]
	first, err := strconv.Atoi(match[3])
	if err != nil {
		http.NotFound(w, r)
		return
	}

	meeting := EnsureAndGetMeeting(meetingid)
	if meeting == nil {
		http.NotFound(w, r)
		return
	}

	/* Parsed correctly. Future error messages sre sent as websocket messages */
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	var remote string
	if config.behindproxy {
		forwarder := r.Header.Get("X-Forwarded-For")
		remote = fmt.Sprintf("%s (%s)", forwarder, r.RemoteAddr)
	} else {
		remote = r.RemoteAddr
	}
	user := newUser(meeting, conn, token, first, remote)

	/* Start the background workers so we can accept both traffic and disconnect signals */
	go user.writer()
	go user.reader()

	/* Register the user with he meeting, which will perform the permissions check */
	meeting.Register <- user
}

/* Are we currently shutting down? In that case we avoid some error logging. */
var _shuttingDown bool = false

/* Listen on a specific host:port or unix socket, serving it using the specified mux */
func doListenAndServe(listen string, mux *http.ServeMux) {
	/* Listener starting with / indicates it's a Unix socket */
	if string((listen)[0]) == "/" {
		listener, err := net.Listen("unix", listen)
		if err != nil {
			log.Fatalf("Could not listen on unix socket %s: %s", listen, err)
			return
		}

		CleanupSocketOnExit(listener)

		err = http.Serve(listener, mux)
		if err != nil && !_shuttingDown {
			log.Fatalf("Could not serve on unix socket %s: %s", listen, err)
		}
	} else {
		err := http.ListenAndServe(listen, mux)
		if err != nil {
			log.Fatalf("Could not listen on %s: %s", listen, err)
		}
	}
}

/* Register signal handlers to clean up unix socket, if unix socket is used */
func CleanupSocketOnExit(listener net.Listener) {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	go func(c chan os.Signal) {
		sig := <-c
		log.Printf("Caught signal %s: shutting down.", sig)
		/* Flag that we're shutting down, so we will not report an error */
		_shuttingDown = true
		listener.Close()
		os.Exit(0)
	}(sigc)
}

func main() {
	flag.StringVar(&config.verifyOrigin, "origin", "", "Origin to verify")
	flag.StringVar(&config.dbURL, "dburl", "postgres:///postgresqleu", "PostgreSQL connection URL")
	flag.BoolVar(&config.behindproxy, "behindproxy", false, "Behind proxy, decode x-forwarded-for")
	listen := flag.String("listen", "127.0.0.1:8199", "Host and port to listen to")
	profilelisten := flag.String("profilelisten", "", "Host to listen for go pprof connections")

	flag.Parse()

	if config.verifyOrigin == "" {
		fmt.Println("Must specify a value for origin veritication")
		flag.Usage()
		return
	}

	/* Start generic background goroutines */
	go MeetingRemover()

	/* Start the profile listener if there is one */
	if profilelisten != nil && *profilelisten != "" {
		go doListenAndServe(*profilelisten, http.DefaultServeMux)
	}

	/* Setup the http handlers and listener */
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/meeting/", wsHandler)
	mux.HandleFunc("/__meetingstatus", StatusHandler)

	doListenAndServe(*listen, mux)
}
