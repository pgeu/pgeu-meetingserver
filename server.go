package main

import (
	"flag"
	"fmt"
	"github.com/gorilla/websocket"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"sync"
	"syscall"
)

var config = struct {
	verify_origin string
	db_url        string
	behindproxy   bool
}{}

var (
	_meetings             = make(map[int]*Meeting)
	_meetings_mutex       sync.RWMutex
	_meeting_remover_chan = make(chan int, 10)
)

func EnsureAndGetMeeting(meetingid int) *Meeting {
	_meetings_mutex.RLock()
	/* Cannot use defer on the unlock since we have to unlock/relock later */

	meeting, ok := _meetings[meetingid]

	if ok {
		_meetings_mutex.RUnlock()
		return meeting
	}

	meeting = NewMeeting(meetingid)
	if meeting == nil {
		_meetings_mutex.RUnlock()
		return nil
	}

	/*
	* Before we can modifyt he meeting struct, we need to lock it for writes.
	* and once we've done that, we also need to make sure nobody else stuck a
	* new meeting in wile we were waiting for a lock.
	 */
	_meetings_mutex.RUnlock()
	_meetings_mutex.Lock()
	defer _meetings_mutex.Unlock()
	_, ok = _meetings[meetingid]
	if ok {
		return _meetings[meetingid]
	}

	/*
	* Nobody put anythign in while we were running, so put our meeting
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
	_meetings_mutex.Lock()
	defer _meetings_mutex.Unlock()

	if _, ok := _meetings[id]; ok {
		log.Printf("Removing meeting %d", id)
		delete(_meetings, id)
	}
}
func MeetingRemover() {
	for {
		id := <-_meeting_remover_chan
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
		if config.verify_origin == "*" {
			log.Printf("Allowing origin %s, all origins are allowed", origin[0])
			return true
		}
		return (config.verify_origin == origin[0])
	},
}

var wsUrlPattern = regexp.MustCompile("^/ws/meeting/(\\d+)/([A-Za-z0-9_-]{54})/(\\d+)")

func wsHandler(w http.ResponseWriter, r *http.Request) {
	match := wsUrlPattern.FindStringSubmatch(r.URL.Path)
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
var _shutting_down bool = false

/* Register signal handlers to clean up unix socket, if unix socket is used */
func CleanupSocketOnExit(listener net.Listener) {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	go func(c chan os.Signal) {
		sig := <-c
		log.Printf("Caught signal %s: shutting down.", sig)
		/* Flag that we're shutting down, so we will not report an error */
		_shutting_down = true
		listener.Close()
		os.Exit(0)
	}(sigc)
}

func main() {
	flag.StringVar(&config.verify_origin, "origin", "", "Origin to verify")
	flag.StringVar(&config.db_url, "dburl", "postgres:///postgresqleu", "PostgreSQL connection URL")
	flag.BoolVar(&config.behindproxy, "behindproxy", false, "Behind proxy, decode x-forwarded-for")
	listen := flag.String("listen", "127.0.0.1:8199", "Host and port to listen to")

	flag.Parse()

	if config.verify_origin == "" {
		fmt.Println("Must specify a value for origin veritication")
		flag.Usage()
		return
	}

	/* Start generic background goroutines */
	go MeetingRemover()

	/* Setup the http handlers and listener */
	http.HandleFunc("/ws/meeting/", wsHandler)
	http.HandleFunc("/__meetingstatus", StatusHandler)

	/* Listener starting with / indicates it's a Unix socket */
	if string((*listen)[0]) == "/" {
		listener, err := net.Listen("unix", *listen)
		if err != nil {
			log.Fatalf("Could not listen on unix socket %s: %s", *listen, err)
			return
		}

		CleanupSocketOnExit(listener)

		err = http.Serve(listener, nil)
		if err != nil && !_shutting_down {
			log.Fatalf("Could not serve on unix socket %s: %s", *listen, err)
		}
	} else {
		err := http.ListenAndServe(*listen, nil)
		if err != nil {
			log.Fatalf("Could not listen on %s: %s", *listen, err)
		}
	}
}
