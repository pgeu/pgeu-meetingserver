package main

import (
	"database/sql"
	"fmt"
	"sync"
)

type ColorAssigner struct {
	idx    int
	mapped map[int]string
	mutex  sync.RWMutex
}

func newColorAssigner() *ColorAssigner {
	return &ColorAssigner{
		idx:    0,
		mapped: make(map[int]string),
	}
}

func (a *ColorAssigner) GetWithNull(userid sql.NullInt64) string {
	if !userid.Valid {
		return "sys"
	}
	return a.Get(int(userid.Int64))
}

func (a *ColorAssigner) Get(userid int) string {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	c, ok := a.mapped[userid]
	if ok {
		return c
	}

	a.idx++
	if a.idx > 9 {
		a.idx = 0
	}
	a.mapped[userid] = fmt.Sprintf("%d", a.idx)
	return a.mapped[userid]
}
