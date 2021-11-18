package main

import (
	"database/sql"
	"fmt"
	_ "github.com/lib/pq"
	"log"
	"time"
)

// Type of action requested through Useraction channel */
const (
	ActionMessage = iota
	ActionVote
	ActionOpenFinish
	ActionNewPoll
	ActionAbortPoll
	ActionKickUser
)

/* Action passed to the Useraction channel */
type MeetingUseraction struct {
	action       int
	user         *User
	message      string
	vote         int
	open         bool
	answers      []string
	minutes      int
	targetuserid int
}

/* Represents one individual meeting */
type Meeting struct {
	Useraction  chan MeetingUseraction
	Register    chan *User
	Unregister  chan *User
	meetingid   int
	state       int
	users       map[string]*User
	polltimer   chan *Poll
	stopchannel chan bool
	db          *sql.DB
	colors      *ColorAssigner
	activepoll  *Poll
	Statusquery chan chan *MeetingStatus
}

/* State of a meeting */
const (
	MeetingStatePreOpen  = 0
	MeetingStateOpen     = 1
	MeetingStateFinished = 2
	MeetingStateClosed   = 3
)

var MeetingStateMap = map[int]string{
	MeetingStatePreOpen:  "pending",
	MeetingStateOpen:     "open",
	MeetingStateFinished: "finished",
	MeetingStateClosed:   "closed",
}

func NewMeeting(meetingid int) *Meeting {
	db, err := sql.Open("postgres", config.db_url)
	if err != nil {
		panic(err)
	}
	db.Exec("SET application_name='pgeu meeting server'")

	var state int
	row := db.QueryRow("SELECT state FROM membership_meeting WHERE id=$1", meetingid)
	if err := row.Scan(&state); err != nil {
		log.Println("Could not find/parse meeting:", err)
		db.Close()
		return nil
	}

	if state == MeetingStateClosed {
		log.Println("Attempt to reopen a closed meeting: ", meetingid)
		db.Close()
		return nil
	}

	return &Meeting{
		meetingid:   meetingid,
		state:       state,
		users:       make(map[string]*User),
		Useraction:  make(chan MeetingUseraction, 10),
		Register:    make(chan *User),
		Unregister:  make(chan *User),
		polltimer:   make(chan *Poll),
		db:          db,
		colors:      newColorAssigner(),
		Statusquery: make(chan chan *MeetingStatus),
		stopchannel: make(chan bool, 1),
	}
}

func (m *Meeting) Run() {
	defer func() {
		/* The meeting owns the db connection, so turn out the lights before we leave */
		m.db.Close()

		/* When we're done running, trigger the routine that removes us fromt he global array */
		_meeting_remover_chan <- m.meetingid
	}()

	for {
		select {
		case action := <-m.Useraction:
			/* Incoming action from a user that's in the meeting */
			{
				switch action.action {
				case ActionMessage:
					m.storeAndBroadcast(action.message, action.user)
				case ActionVote:
					m.castVote(action.message, action.vote, action.user)
				case ActionOpenFinish:
					m.openOrFinishMeeting(action.user, action.open)
				case ActionNewPoll:
					m.newPoll(action.user, action.message, action.answers, action.minutes)
				case ActionAbortPoll:
					m.abortPoll(action.user)
				case ActionKickUser:
					m.kickUser(action.user, action.targetuserid, action.open)
				}
			}
		case user := <-m.Register:
			m.register(user)
		case responsechan := <-m.Statusquery:
			m.reportStatus(responsechan)
		case user := <-m.Unregister:
			m.unregister(user)
		case poll := <-m.polltimer:
			m.pollTimerFired(poll)
		case _ = <-m.stopchannel:
			return
		}
	}
}

/***********************************************************************
 * Attendee registration and unregistration
 ***********************************************************************/
func (m *Meeting) register(user *User) {
	row := m.db.QueryRow(`SELECT user_id, mk.id,
fullname,
EXISTS (SELECT 1 FROM membership_meeting_meetingadmins a WHERE a.meeting_id=$1 AND a.member_id=m.user_id) AS isadmin,
allowrejoin,
proxyname
FROM membership_member m
INNER JOIN membership_membermeetingkey mk ON m.user_id=mk.member_id
WHERE mk.meeting_id=$1 AND mk.key=$2`,
		m.meetingid, user.Token())

	/*
	 * Since this user is not "attached" yet, we can modify the members from
	 * this goroutine, even though they're technically "owned" by the
	 * user one.
	 */
	if err := row.Scan(&user.Info.authid, &user.Info.keyid, &user.Info.name, &user.Info.admin, &user.Info.allowrejoin, &user.Info.proxyname); err != nil {
		/* If it's just no rows found that's not really an error */
		if err != sql.ErrNoRows {
			log.Println("Failed to check user record in db:", err)
			user.Disconnect <- "Connection error"
		} else {
			user.Disconnect <- "You are not allowed to enter this meeting"
		}
		return
	}

	if !user.Info.admin {
		/* Admins are always allowed to join, but other users might not be */
		if m.state == MeetingStateFinished {
			user.Disconnect <- "This meeting is already finished and can no longer be joined."
			return
		}
		if m.state == MeetingStateOpen && !user.Info.allowrejoin {
			user.Disconnect <- "This meeting is already in progress and can no longer be joined."
			return
		}
	}

	/* Track the previous user to know if this was a re-join or a first-join */
	prevuser := m.users[user.Token()]
	m.users[user.Token()] = user

	restr := ""
	if prevuser != nil {
		user.Info.rejoined = true
		restr = "re-"
		user.Info.color = prevuser.Info.color
	} else {
		user.Info.color = m.colors.Get(user.Info.keyid)
	}

	if prevuser != nil && prevuser.Info.connected {
		prevuser.Disconnect <- "You have connected from a different session. This session is disconnected."
	}

	user.Info.connected = true

	log.Printf("Member %s %sjoined meeting %d", user.Info.name, restr, m.meetingid)

	/* Once the user is in, the default is to allow them to rejoin if the happen to be disconnected */
	user.Info.allowrejoin = true
	_, err := m.db.Exec("UPDATE membership_membermeetingkey SET allowrejoin=true WHERE meeting_id=$1 AND key=$2 AND NOT allowrejoin", m.meetingid, user.Token())
	if err != nil {
		log.Println("Failed to set user to allow re-login", err)
		/* We will just continue because this was not a vital operation */
	}

	/* Send initial information about the meeting */
	m.broadcastUserJoinLeave(user, true)
	m.sendUserListTo(user)
	m.sendMeetingStateTo(user)
	m.sendPollStatusTo(user)

	/* Send initial messages, if we joined an already running meeting */
	m.sendInitialMessagesTo(user)

	/* Announce the joining */
	if user.Info.proxyname != nil {
		m.storeAndBroadcast(fmt.Sprintf("Member %s %sjoined the meeting (through proxy %s)", user.Info.name, restr, *user.Info.proxyname), nil)
	} else {
		m.storeAndBroadcast(fmt.Sprintf("Member %s %sjoined the meeting", user.Info.name, restr), nil)
	}
}

func (m *Meeting) unregister(user *User) {
	if _, ok := m.users[user.Token()]; ok {
		user.Info.connected = false
	}

	m.broadcastUserJoinLeave(user, false)

	/* Notify the user is going out */
	if user.Info.name != "" {
		m.storeAndBroadcast(fmt.Sprintf("Member %s left the meeting", user.Info.name), nil)
		log.Printf("Member %s left meeting %d", user.Info.name, m.meetingid)
	}

	/* If the meeting is finished and this was the last user in it, shut down processing fo it */
	if m.state == MeetingStateFinished {
		found := false
		for _, u := range m.users {
			if u.Info.connected {
				found = true
				break
			}
		}
		if !found {
			log.Printf("Last member left, meeting is finished, switching to Closed")
			_, err := m.db.Exec("UPDATE membership_meeting SET state=$1 WHERE id=$2 AND state != $1", MeetingStateClosed, m.meetingid)
			if err != nil {
				log.Printf("Failed to set meeting status to Closed: %s", err)
				/* We proceed and remove it here anyway */
			}
			m.stopchannel <- true
		}
	}
}

func (m *Meeting) sendInitialMessagesTo(to *User) {
	rows, err := m.db.Query(`SELECT ml.id,
t,
mk.id,
COALESCE(fullname, ''),
message
FROM membership_meetingmessagelog ml
LEFT JOIN membership_member ON membership_member.user_id=ml.sender_id
LEFT JOIN membership_membermeetingkey mk ON mk.member_id=ml.sender_id AND mk.meeting_id=$2
WHERE ml.id > $1 AND ml.meeting_id=$2
ORDER BY ml.id`,
		to.FirstMessage(), m.meetingid)
	if err != nil {
		log.Println("Failed to query old messages:", err)
		return
	}
	defer rows.Close()

	data := []msgMessage{}
	for rows.Next() {
		var t time.Time
		var senderid sql.NullInt64
		msg := msgMessage{}

		err = rows.Scan(&msg.Id, &t, &senderid, &msg.FromName, &msg.Message)
		if err != nil {
			log.Println("Failed to parse row in old messages:", err)
			return
		}
		msg.Time = t.Format("15:04:05")
		msg.Date = t.Format("2006-01-02")
		msg.Color = m.colors.GetWithNull(senderid)
		if senderid.Valid {
			msg.From = senderid.Int64
		} else {
			msg.From = -1
		}
		data = append(data, msg)
	}
	m.sendJsonTo(to, MakeMessage("messages", data))
}

/***********************************************************************
 * Sending and broadcasting infrastructure
 ***********************************************************************/

/* Broadcast a json structure to all users, optionally filtered by if they are admins or users */
func (m *Meeting) broadcastJson(toadmin bool, touser bool, v interface{}, excludeuser *User) {
	for _, user := range m.users {
		if user == excludeuser {
			continue
		}
		if !user.Info.connected {
			continue
		}

		if user.Info.admin && !toadmin {
			continue
		}
		if !user.Info.admin && !touser {
			continue
		}

		select {
		case user.Send <- v:
		default: /* User channel is full */
			log.Printf("Send channel full for member %s", user.Info.name)
		}
	}
}

/* Send a json structure to one individual user */
func (m *Meeting) sendJsonTo(to *User, v interface{}) {
	to.Send <- v
}

/* Send an error message to one individual user */
func (m *Meeting) sendErrorTo(user *User, message string) {
	m.sendJsonTo(user, MakeError(message))
}

/*
 * Store a message in the database, and re-broadcast it to all connected users.
 * If a from user is specfied, flag that user as sender, or use nil to indicate system message.
 */
func (m *Meeting) storeAndBroadcast(message string, from *User) {
	var time time.Time
	var id int
	var fromname string
	var fromid sql.NullInt64
	var fromidval int64
	var color string

	if message == "" {
		log.Println("Can't send empty message")
		return
	}

	if from == nil {
		fromname = ""
		fromid = sql.NullInt64{Valid: false}
		color = ""
	} else {
		fromname = from.Info.name
		fromid = sql.NullInt64{Int64: int64(from.Info.authid), Valid: true}
		color = from.Info.color
	}

	row := m.db.QueryRow("INSERT INTO membership_meetingmessagelog(meeting_id, t, sender_id, message) VALUES ($1, CURRENT_TIMESTAMP, $2, $3) RETURNING id, t", m.meetingid, fromid, message)
	if err := row.Scan(&id, &time); err != nil {
		log.Println("Could not insert into message log:", err)
		return
	}
	if fromid.Valid {
		fromidval = fromid.Int64
	} else {
		fromidval = -1
	}

	data := msgMessage{
		Id:       id,
		Time:     time.Format("15:04:05"),
		Date:     time.Format("2006-01-02"),
		Message:  message,
		From:     fromidval,
		FromName: fromname,
		Color:    color,
	}

	m.broadcastJson(true, true, MakeMessage("message", data), nil)
}

func (m *Meeting) broadcastUserJoinLeave(user *User, joinleave bool) {
	var what string
	if joinleave {
		what = "adduser"
	} else {
		what = "removeuser"
	}
	mu := msgUser{Name: user.Info.name, Color: user.Info.color, Id: user.Info.keyid}

	/* Broadcast to both admins and users, except for the one actually joining/leaving */
	m.broadcastJson(true, true, MakeMessage(what, mu), user)
}

func (m *Meeting) sendUserListTo(to *User) {
	var users []msgUser
	for _, u := range m.users {
		if u.Info.connected {
			mu := msgUser{Name: u.Info.name, Color: u.Info.color, Id: u.Info.keyid}
			users = append(users, mu)
		}
	}

	m.sendJsonTo(to, MakeMessage("users", msgUsers{Users: users}))
}

func (m *Meeting) sendMeetingStateTo(to *User) {
	m.sendJsonTo(to, MakeMessage("status", MakeMeetingState(m.state)))
}
func (m *Meeting) broadcastMeetingState() {
	m.broadcastJson(true, true, MakeMessage("status", MakeMeetingState(m.state)), nil)
}

func (m *Meeting) sendPollStatusTo(to *User) {
	m.sendJsonTo(to, MakeMessage("poll", m.getPollStatusStruct(to.Info.admin)))
}

func (m *Meeting) broadcastPollStatus() {
	m.broadcastJson(true, false, MakeMessage("poll", m.getPollStatusStruct(true)), nil)
	m.broadcastJson(false, true, MakeMessage("poll", m.getPollStatusStruct(false)), nil)
}

/***********************************************************************
 * Meeting administration
 ***********************************************************************/
func (m *Meeting) openOrFinishMeeting(u *User, doopen bool) {
	if doopen {
		if m.state == MeetingStateOpen {
			m.sendErrorTo(u, "Meeting is already open")
			return
		}
		if m.state == MeetingStateFinished {
			m.storeAndBroadcast(fmt.Sprintf("This meeting is being re-opened by %s", u.Info.name), nil)
		}
		m.state = MeetingStateOpen
		m.storeAndBroadcast("This meeting is now open", nil)
		m.storeAndBroadcast("Anything sent from now on will be part of the permanent record", nil)
	} else {
		if m.state == MeetingStateFinished {
			m.sendErrorTo(u, "Meeting is already finished")
			return
		}
		m.state = MeetingStateFinished
		m.storeAndBroadcast("This meeting is now finished", nil)
	}
	_, err := m.db.Exec("UPDATE membership_meeting SET state=$1 WHERE id=$2", m.state, m.meetingid)
	if err != nil {
		m.sendErrorTo(u, "Failed to update state in database")
		log.Printf("Failed to update meeting state in database: %v", err)
		return
	}
	m.broadcastMeetingState()
}

/***********************************************************************
 * Polls
 ***********************************************************************/
func (m *Meeting) getPollStatusStruct(admin bool) *msgPollStatus {
	if m.activepoll == nil {
		return nil
	}

	p := msgPollStatus{
		Question: m.activepoll.Question,
		Answers:  m.activepoll.Answers,
		Tally:    m.activepoll.Tally(),
	}
	if admin {
		p.Voted = m.activepoll.Voted()
	}

	return &p
}

func (m *Meeting) newPoll(user *User, question string, answers []string, minutes int) {
	if m.activepoll != nil {
		m.sendErrorTo(user, "There is already an active poll")
		return
	}

	m.activepoll = NewPoll(question, answers)

	m.broadcastPollStatus()
	m.storeAndBroadcast(fmt.Sprintf("A new poll has been posted for %s", question), nil)

	/* Start a timer to close the poll */
	timer := time.NewTimer(time.Duration(minutes) * time.Minute)
	go func() {
		<-timer.C
		m.polltimer <- m.activepoll
	}()
}

func (m *Meeting) castVote(question string, vote int, user *User) {
	if m.activepoll == nil {
		m.sendErrorTo(user, "There is no active poll")
		return
	}

	if question != m.activepoll.Question {
		m.sendErrorTo(user, "Vote for the wrong question received")
		return
	}

	if vote < 0 || vote >= len(m.activepoll.Answers) {
		m.sendErrorTo(user, "Invalid vote")
		return
	}

	changed := m.activepoll.CastVote(user.Info.keyid, vote)
	if changed {
		m.storeAndBroadcast(fmt.Sprintf("%s changed their vote to %s", user.Info.name, m.activepoll.Answers[vote]), nil)
	} else {
		m.storeAndBroadcast(fmt.Sprintf("%s voted %s", user.Info.name, m.activepoll.Answers[vote]), nil)
	}

	if m.activepoll.VoteCount() == len(m.users) {
		m.closePoll("All attendees have voted, poll has completed.")
	} else {
		m.broadcastPollStatus()
	}
}

func (m *Meeting) closePoll(msg string) {
	if m.activepoll == nil {
		/* Can't happen, really */
		return
	}

	tally := m.activepoll.Tally()
	m.storeAndBroadcast(msg, nil)
	for i, a := range m.activepoll.Answers {
		var plural string
		if tally[i] == 1 {
			plural = ""
		} else {
			plural = "s"
		}
		m.storeAndBroadcast(fmt.Sprintf("Answer \"%s\": %d vote%s", a, tally[i], plural), nil)
	}
	m.activepoll = nil
	m.broadcastPollStatus()
}

func (m *Meeting) abortPoll(user *User) {
	if m.activepoll == nil {
		m.sendErrorTo(user, "There is no active poll")
		return
	}

	m.activepoll = nil
	m.storeAndBroadcast("The current poll has has been aborted", nil)
	m.broadcastPollStatus()
}

func (m *Meeting) pollTimerFired(poll *Poll) {
	if m.activepoll == poll {
		m.closePoll("Poll has completed")
	}
}

/***********************************************************************
 * User administration
 ***********************************************************************/
func (m *Meeting) kickUser(user *User, targetuserid int, canrejoin bool) {
	var targetuser *User
	for _, u := range m.users {
		if u.Info.keyid == targetuserid {
			targetuser = u
			break
		}
	}
	if targetuser == nil {
		m.sendErrorTo(user, "User to kick not found")
		return
	}
	targetuser.Info.allowrejoin = canrejoin // XXX: Don't set from here!
	targetuser.Disconnect <- "You have been forcibly disconnected from this meeting"
	m.storeAndBroadcast(fmt.Sprintf("User %s has been disconnected by %s", targetuser.Info.name, user.Info.name), nil)
	m.broadcastUserJoinLeave(targetuser, false)

	/* If block rejoins, we must do so in the db as well! */
	if !canrejoin {
		_, err := m.db.Exec("UPDATE membership_membermeetingkey SET allowrejoin=false WHERE meeting_id=$1 AND key=$2 AND allowrejoin", m.meetingid, targetuser.Token())
		if err != nil {
			log.Println("Failed to set user to allow re-login", err)
			/* We will just continue because this was not a vital operation */
		}
	}
}

/***********************************************************************
 * Status reporting
 ***********************************************************************/
func (m *Meeting) reportStatus(reportchan chan *MeetingStatus) {
	status := &MeetingStatus{
		Id:    m.meetingid,
		State: MeetingStateMap[m.state],
	}
	for _, u := range m.users {
		ms := MemberStatus{
			Uid:    u.Info.authid,
			Name:   u.Info.name,
			Admin:  u.Info.admin,
			Remote: u.Remote(),
		}
		if u.Info.connected {
			status.Members = append(status.Members, ms)
		} else {
			status.DisconnectedMembers = append(status.DisconnectedMembers, ms)
		}
	}
	if status.Members == nil {
		status.Members = make([]MemberStatus, 0)
	}
	reportchan <- status
}
