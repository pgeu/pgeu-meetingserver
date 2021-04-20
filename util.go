package main

import (
	"time"
)

/* Core structure containing all messages except errors */
type Msg struct {
	Messagetype string      `json:"type"`
	Data        interface{} `json:"data"`
}

/* An error */
type ErrorMsg struct {
	Messagetype string `json:"type"`
	Msg         string `json:"msg"`
}

/* A new message posted by somebody */
type msgMessage struct {
	Id       int    `json:"id"`
	Time     string `json:"time"`
	Date     string `json:"date"`
	From     int64  `json:"from"`
	FromName string `json:"fromname"`
	Color    string `json:"color"`
	Message  string `json:"message"`
}

/* Status of the meeting */
type msgMeetingState struct {
	Isopen     bool `json:"isopen"`
	Isfinished bool `json:"isfinished"`
}

/* Status of the current poll */
type msgPollStatus struct {
	Question string   `json:"question"`
	Answers  []string `json:"answers"`
	Tally    [5]int   `json:"tally"`
	Voted    []int    `json:"voted"`
}

/* Users currently in the meeting */
type msgUser struct {
	Name  string `json:"name"`
	Color string `json:"color"`
	Id    int    `json:"id"`
}
type msgUsers struct {
	Users []msgUser `json:"users"`
}

/*
 * Utility functions to make generic messages
 */

func MakeMessage(messagetype string, data interface{}) Msg {
	return Msg{Messagetype: messagetype, Data: data}
}

func MakeError(message string) ErrorMsg {
	return ErrorMsg{Messagetype: "error", Msg: message}
}

func DisconnectMessage(msg string) Msg {
	time := time.Now()
	data := msgMessage{
		Id:       -1,
		Time:     time.Format("15:04:05"),
		Date:     time.Format("2006-01-02"),
		Message:  msg,
		From:     -1,
		FromName: "",
	}

	return MakeMessage("disconnect", data)
}

func MakeMeetingState(state int) msgMeetingState {
	if state == MeetingStateOpen {
		return msgMeetingState{Isopen: true, Isfinished: false}
	} else if state == MeetingStateFinished {
		return msgMeetingState{Isopen: false, Isfinished: true}
	} else {
		return msgMeetingState{Isopen: false, Isfinished: false}
	}
}
