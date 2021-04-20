# Protocol

The protocol is a very simple two-way websockets protocol.

## Server -> Client messages

### message

```json
{
	"type": "message",
	"data": {
		"id": <integer>,
		"time": "19:19:19",
		"date": "2021-01-31",
		"from": <integer>,
		"fromname": <string>,
		"color": <string>
	}
}
```

Represents a single message being delivered in the chat.

The value `id` is a unique identifier of this message.

The value `from` will be `-1` and `fromname` will be empty if the
message is a system message.

The value `color` is string representing a rotating color value to be
used for this message, to ensure the same user remains with the same
color.

### messages

```json
{
	"type": "messages",
	"data": [
		<message>,
		<message>
	]
}
```

Contains multiple messages, each individual one being the equivalent
of the `data` part of the `message` message.

### adduser

```json
{
	"type": "adduser",
	"data": {
		"id": <integer>,
		"name": <string>,
		"color": <string>
	}
}
```

Represents a user that should be added to the list of users in the
meeting.

The `id` field uniquely identifies this user.

### removeuser

```json
{
	"type": "removeuser",
	"data": <user>
}
```

Represents a user that shouldb e removed from the list of users based
on the id. The `data` field has the same format as in the `adduser` message.

### users

```json
{
	"type": "users",
	"data": {
		"users": [
			<user>,
			<user>
		]
	}
}
```

Represents a list of users and should replace the complete current
list of users. Each individual `<user>` has the same format as the
`data` field in the `adduser` message.

### status

```json
{
	"type": "status",
	"data": {
		"isopen": <boolean>,
		"isfinished": <boolean>
	}
}
```

Indicates the current status of this meeting. For details about the
different statuses, see the documentation of
[pgeu-system](https://github.com/pgeu/pgeu-system/).

### poll

```json
{
	"type": "poll",
	"data": {
		"question": <string>,
		"answers": [
			<string>,
			<string>
		],
		"tally": [
			<integer>,
			<integer>
		],
		"voted": [
			<integer>,
			<integer>
		]
	}
}
```

Represents an on-going poll in the meeting.

`question` is the question being asked.

`answers` is an array of up to 5 strings, each representing the
response.

`tally` is an array of integers of the same size as `answers`,
indicating how many people have voted for each answer so far.

`voted` is an array listing the ids of all users that have voted on
this poll. This field is `null` if the connected user is not an
administrator.

If no poll is active (for example, the current one is being closed),
the `data` field is set to `null`.

### disconnect
```json
{
	"type": "disconnect",
	"data": <message>
}
```

Indicates the user has been disconnected for some reason (could be the
meeting isn't open, or the user is kicked, or other reasons).

The `data` part of the message is identical to that of the `message`
message.

After the `disconnect` message is delivered, the websocket will be
closed.


### error
```json
{
	"type": "error",
	"data": <message>
}
```

Indicates an error occured, but not one that requires a disconnect.

The `data` part of the message is identical to that of the `message`
message.

## Client -> Server messages

### message
```json
{
	"type": "message",
	"message: <string>
}
```

Sends a message to the chat.

### vote
```json
{
	"type": "vote",
	"question": <string>,
	"vote": <integer>
}
```

Cast a vote in the ongoing poll.

`question` must be identical to the question of the currently running
poll, and is used to make sure a vote is not accidentally cast ont he
wrong poll in case of long network delays.

### open
```json
{
	"type": "open"
}
```

Opens the meeting.

This message is only available to connected users who are
administrators.

### finish
```json
{
	"type": "finish"
}
```

Finishes the meeting.

This message is only available to connected users who are
administrators.

### newpoll

```json
{
	"type": "newpoll",
	"question": <string>,
	"answers": [
		<string>,
		<string>
	],
	"minutes": <integer>
}
```

Starts a new poll with the question `question`, with up to 5 choices
of answers. The poll will automatically close after `minutes` minutes.

This message is only available to connected users who are
administrators.

### abortpoll
```json
{
	"type": "abortpoll"
}
```

Aborts the currently running poll and throws away the results.

This message is only available to connected users who are
administrators.

### kick
```json
{
	"type": "kick",
	"user": <integer>,
	"canrejoin": <boolean>
}
```

Kicks a user with id `id` from the chat. If `canrejoin` is set to true
the user is allowed to re-join the meeting later, otherwise not.

This message is only available to connected users who are
administrators.
