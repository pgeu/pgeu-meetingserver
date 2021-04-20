package main

import (
	"github.com/gorilla/websocket"
	_ "github.com/lib/pq"
	"log"
	"strings"
	"time"
)

/* User data from db, and data "owned" by the meeting the user is in */
type UserInfo struct {
	keyid       int
	authid      int
	name        string
	admin       bool
	connected   bool
	rejoined    bool
	allowrejoin bool
	proxyname   *string
	color       string
}

type User struct {
	meeting      *Meeting
	conn         *websocket.Conn
	Send         chan interface{}
	Disconnect   chan string
	token        string
	firstmessage int
	remote       string
	/* User data from db, and data "owned" by the meeting the user is in */
	Info UserInfo
}

func newUser(meeting *Meeting, conn *websocket.Conn, token string, firstmessage int, remote string) *User {
	return &User{
		meeting:      meeting,
		conn:         conn,
		token:        token,
		firstmessage: firstmessage,
		Send:         make(chan interface{}, 100),
		Disconnect:   make(chan string, 1),
		remote:       remote,
	}
}

func (u *User) Token() string {
	return u.token
}

func (u *User) FirstMessage() int {
	return u.firstmessage
}

func (u *User) Remote() string {
	return u.remote
}

func (u *User) sendError(msg string) {
	u.Send <- MakeError(msg)
}

func (u *User) adminCheck(what string) {
	/* Called in the user goroutine, but admin and name is set on startup and can never be changed, so we can safely read it */
	if !u.Info.admin {
		log.Printf("Attempt by non-admin %s to %s", u.Info.name, what)
		u.sendError("Permission denied")
	}
}

func (u *User) receiveMessage(data map[string]interface{}) {
	message, ok := data["message"].(string)
	if !ok {
		log.Println("Malformatted json in message")
		return
	}

	/* Don't send an empty message */
	if message != "" {
		u.meeting.Useraction <- MeetingUseraction{action: ActionMessage, user: u, message: strings.TrimSpace(message)}
	}
}

func (u *User) newPoll(data map[string]interface{}) {
	question, ok := data["question"].(string)
	if !ok {
		u.sendError("Invalid or no question")
		return
	}

	minutes, ok := data["minutes"].(float64)
	if !ok {
		u.sendError("Invalid or no minutes")
		return
	}

	rawanswers, ok := data["answers"].([]interface{})
	if !ok {
		u.sendError("Invalid or no answers")
		return
	}

	if len(rawanswers) > 5 {
		u.sendError("Too many answers")
		return
	}

	var answers []string
	for _, a := range rawanswers {
		aa, ok := a.(string)
		if !ok {
			u.sendError("Invalid or unparsable answer")
			return
		}
		answers = append(answers, aa)
	}

	u.meeting.Useraction <- MeetingUseraction{action: ActionNewPoll, message: question, answers: answers, minutes: int(minutes)}
}

func (u *User) kickUser(data map[string]interface{}) {
	targetuser, ok := data["user"].(float64)
	if !ok {
		u.sendError("Invalid user in json")
		return
	}

	canrejoin, ok := data["canrejoin"].(bool)
	if !ok {
		u.sendError("Invalid canrejoin in json")
		return
	}

	u.meeting.Useraction <- MeetingUseraction{action: ActionKickUser, user: u, targetuserid: int(targetuser), open: canrejoin}
}

func (u *User) receiveVote(data map[string]interface{}) {
	question, ok := data["question"].(string)
	if !ok {
		log.Println("Malformatted json in vote")
		return
	}
	vote, ok := data["vote"].(float64)
	if !ok {
		log.Println("Malformatted vote json in vote")
		return
	}

	u.meeting.Useraction <- MeetingUseraction{action: ActionVote, message: question, vote: int(vote), user: u}
}

func (u *User) receiveData(j interface{}) {
	root, ok := j.(map[string]interface{})
	if !ok {
		log.Printf("Unable to get map from json object %v", j)
		return
	}

	t, ok := root["type"]
	if !ok {
		log.Printf("Unable to get type from json object %v", j)
		return
	}

	switch t {
	case "message":
		u.receiveMessage(root)
	case "vote":
		u.receiveVote(root)
	case "open":
		{
			u.adminCheck("open/close meeting")
			u.meeting.Useraction <- MeetingUseraction{action: ActionOpenFinish, user: u, open: true}
		}
	case "finish":
		{
			u.adminCheck("open/close meeting")
			u.meeting.Useraction <- MeetingUseraction{action: ActionOpenFinish, user: u, open: false}
		}
	case "newpoll":
		{
			u.adminCheck("create new peoll")
			u.newPoll(root)
		}
	case "abortpoll":
		{
			u.adminCheck("abort running poll")
			u.meeting.Useraction <- MeetingUseraction{action: ActionAbortPoll, user: u}
		}
	case "kick":
		{
			u.adminCheck("kick another user")
			u.kickUser(root)
		}
	default:
		log.Println("Unknown object type ", t)
	}
}

/*
 * Package shuffling gorutines
 */
func (u *User) writer() {
	/* Ticker for ping messaegs */
	ticker := time.NewTicker(60 * time.Second)

	defer func() {
		ticker.Stop()
		u.conn.Close()
		log.Println("Connection closed in writing for token ", u.token)
	}()

	for {
		select {
		case message, ok := <-u.Disconnect:
			log.Printf("Disconnecting user with token %s with message %s", u.token, message)
			u.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

			if !ok {
				u.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			/* Send a disconnect message and then return which will closet he socket */
			err := u.conn.WriteJSON(DisconnectMessage(message))
			if err != nil {
				log.Printf("Error writing socket disconnect message for token %s: %v", u.token, err)
			}
			return
		case message, ok := <-u.Send:
			if message == nil {
				continue
			}

			u.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

			if !ok {
				/* Channel closed from controller */
				u.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			err := u.conn.WriteJSON(message)

			if err != nil {
				log.Printf("Error writing to socket for token %s: %v\n", u.token, err)
				return
			}
		case <-ticker.C:
			u.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := u.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (u *User) reader() {
	defer func() {
		u.meeting.Unregister <- u
		u.conn.Close()
		log.Println("Connection closed in reading for token ", u.token)
	}()

	u.conn.SetReadLimit(10240)
	u.conn.SetReadDeadline(time.Now().Add(90 * time.Second))

	u.conn.SetPongHandler(func(string) error {
		u.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})

	for {
		var data interface{}
		err := u.conn.ReadJSON(&data)
		if err != nil {
			log.Printf("Failed to read json on connection for token %s: %v", u.token, err)
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("Websocket error for token %s: %v\n", u.token, err)
			}
			break
		}
		u.receiveData(data)
	}
}
